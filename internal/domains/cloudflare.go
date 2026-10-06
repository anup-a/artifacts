package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Cloudflare registers hostnames as Cloudflare for SaaS custom hostnames on
// the byagent.dev zone. Customers CNAME their hostname to the zone's CNAME
// target; Cloudflare validates it over HTTP once that record is live,
// issues the certificate and sends the traffic to the fallback origin, which
// reaches this server through the tunnel with the customer's Host intact.
type Cloudflare struct {
	Token  string
	ZoneID string
	// BaseURL is the API root; empty means https://api.cloudflare.com/client/v4.
	BaseURL string
	Client  *http.Client
}

func (c *Cloudflare) Name() string { return "cloudflare" }

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cfHostname struct {
	ID                 string   `json:"id"`
	Hostname           string   `json:"hostname"`
	Status             string   `json:"status"`
	VerificationErrors []string `json:"verification_errors"`
	SSL                struct {
		Status           string `json:"status"`
		ValidationErrors []struct {
			Message string `json:"message"`
		} `json:"validation_errors"`
	} `json:"ssl"`
}

func (c *Cloudflare) base() string {
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return "https://api.cloudflare.com/client/v4"
}

// call sends one API request and decodes result into out. A 404 comes back
// as errNotFound so Remove can treat it as done.
func (c *Cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+"/zones/"+url.PathEscape(c.ZoneID)+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.Client
	if client == nil {
		client = apiClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var env struct {
		Success bool            `json:"success"`
		Errors  []cfError       `json:"errors"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&env); err != nil {
		return fmt.Errorf("cloudflare %s %s: status %d: %w", method, path, res.StatusCode, err)
	}
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if !env.Success || res.StatusCode >= 300 {
		msgs := []string{}
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
		}
		return fmt.Errorf("cloudflare %s %s: status %d: %s", method, path, res.StatusCode, strings.Join(msgs, "; "))
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

var errNotFound = errors.New("not found at the provider")

func (c *Cloudflare) Add(ctx context.Context, hostname string) (Edge, error) {
	var h cfHostname
	body := map[string]any{
		"hostname": hostname,
		"ssl": map[string]any{
			"method":   "http",
			"type":     "dv",
			"settings": map[string]any{"min_tls_version": "1.2", "http2": "on"},
		},
	}
	err := c.call(ctx, http.MethodPost, "/custom_hostnames", body, &h)
	if err != nil {
		// Already on the zone (a retry after a lost response, or a server
		// move): adopt the existing hostname rather than failing.
		existing, lerr := c.lookup(ctx, hostname)
		if lerr != nil || existing.ID == "" {
			return Edge{}, err
		}
		h = existing
	}
	return h.edge(), nil
}

func (c *Cloudflare) lookup(ctx context.Context, hostname string) (cfHostname, error) {
	var list []cfHostname
	if err := c.call(ctx, http.MethodGet, "/custom_hostnames?hostname="+url.QueryEscape(hostname), nil, &list); err != nil {
		return cfHostname{}, err
	}
	for _, h := range list {
		if strings.EqualFold(h.Hostname, hostname) {
			return h, nil
		}
	}
	return cfHostname{}, errNotFound
}

func (c *Cloudflare) Check(ctx context.Context, hostname, ref string) (Edge, error) {
	if ref == "" {
		return c.Add(ctx, hostname)
	}
	var h cfHostname
	err := c.call(ctx, http.MethodGet, "/custom_hostnames/"+url.PathEscape(ref), nil, &h)
	if errors.Is(err, errNotFound) {
		// Cloudflare deletes a hostname that stayed moved for a week.
		// Register it again so the owner only has to fix their DNS.
		return c.Add(ctx, hostname)
	}
	if err != nil {
		return Edge{}, err
	}
	if h.Status == "moved" {
		// A moved hostname stays moved until someone asks Cloudflare to look
		// again. The owner may have fixed the record since, so ask; the
		// checker's backoff keeps this to once an hour.
		var again cfHostname
		if err := c.call(ctx, http.MethodPatch, "/custom_hostnames/"+url.PathEscape(ref), map[string]any{"ssl": map[string]any{"method": "http", "type": "dv"}}, &again); err == nil && again.ID != "" {
			h = again
		}
	}
	return h.edge(), nil
}

func (c *Cloudflare) Remove(ctx context.Context, hostname, ref string) error {
	if ref == "" {
		h, err := c.lookup(ctx, hostname)
		if errors.Is(err, errNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		ref = h.ID
	}
	err := c.call(ctx, http.MethodDelete, "/custom_hostnames/"+url.PathEscape(ref), nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// edge reads Cloudflare's two statuses: the hostname's (routed to the zone)
// and the certificate's. Both must be active before traffic works.
func (h cfHostname) edge() Edge {
	e := Edge{Ref: h.ID, SSLStatus: h.SSL.Status}
	e.Ready = h.Status == "active" && h.SSL.Status == "active"
	switch h.Status {
	case "blocked", "moved", "deleted":
		e.Failed = true
	}
	switch h.SSL.Status {
	case "validation_timed_out", "issuance_timed_out", "deleted", "expired":
		e.Failed = true
	}
	msgs := append([]string{}, h.VerificationErrors...)
	for _, v := range h.SSL.ValidationErrors {
		msgs = append(msgs, v.Message)
	}
	e.Message = strings.Join(msgs, "; ")
	return e
}
