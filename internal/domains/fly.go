package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Fly asks Fly.io for a certificate on the app serving byagent, the API
// equivalent of `fly certs add`, through the same GraphQL API flyctl uses.
// Customers CNAME their hostname to <app>.fly.dev (or point an apex at the
// app's addresses); Fly validates it once the record resolves to the app and
// issues the certificate.
type Fly struct {
	Token string
	App   string
	// Endpoint is the GraphQL URL; empty means https://api.fly.io/graphql.
	Endpoint string
	Client   *http.Client
}

func (f *Fly) Name() string { return "fly" }

const flyCertFields = `hostname clientStatus configured`

type flyCert struct {
	Hostname     string `json:"hostname"`
	ClientStatus string `json:"clientStatus"`
	Configured   bool   `json:"configured"`
}

func (f *Fly) query(ctx context.Context, q string, vars map[string]any, out any) error {
	b, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	endpoint := f.Endpoint
	if endpoint == "" {
		endpoint = "https://api.fly.io/graphql"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	// Tokens from `fly tokens create` carry their own scheme ("FlyV1 ...");
	// older personal tokens are bearer tokens.
	if strings.HasPrefix(f.Token, "FlyV1 ") {
		req.Header.Set("Authorization", f.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+f.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	client := f.Client
	if client == nil {
		client = apiClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&env); err != nil {
		return fmt.Errorf("fly graphql: status %d: %w", res.StatusCode, err)
	}
	if len(env.Errors) > 0 {
		msgs := []string{}
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("fly graphql: %s", strings.Join(msgs, "; "))
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("fly graphql: status %d", res.StatusCode)
	}
	return json.Unmarshal(env.Data, out)
}

func (f *Fly) Add(ctx context.Context, hostname string) (Edge, error) {
	var out struct {
		AddCertificate struct {
			Certificate *flyCert `json:"certificate"`
		} `json:"addCertificate"`
	}
	err := f.query(ctx, `mutation($appId: ID!, $hostname: String!) { addCertificate(appId: $appId, hostname: $hostname) { certificate { `+flyCertFields+` } } }`,
		map[string]any{"appId": f.App, "hostname": hostname}, &out)
	if err != nil {
		// Fly refuses a hostname the app already has; read that one back.
		if c, rerr := f.read(ctx, hostname); rerr == nil && c != nil {
			return c.edge(), nil
		}
		return Edge{}, err
	}
	if out.AddCertificate.Certificate == nil {
		return Edge{}, fmt.Errorf("fly graphql: no certificate returned for %s", hostname)
	}
	return out.AddCertificate.Certificate.edge(), nil
}

func (f *Fly) Check(ctx context.Context, hostname, _ string) (Edge, error) {
	c, err := f.read(ctx, hostname)
	if err != nil {
		return Edge{}, err
	}
	if c == nil {
		return f.Add(ctx, hostname)
	}
	return c.edge(), nil
}

// read returns the app's certificate for hostname, or nil when it has none.
func (f *Fly) read(ctx context.Context, hostname string) (*flyCert, error) {
	var out struct {
		App struct {
			Certificate *flyCert `json:"certificate"`
		} `json:"app"`
	}
	err := f.query(ctx, `query($appName: String!, $hostname: String!) { app(name: $appName) { certificate(hostname: $hostname) { `+flyCertFields+` } } }`,
		map[string]any{"appName": f.App, "hostname": hostname}, &out)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil, nil
	}
	return out.App.Certificate, err
}

func (f *Fly) Remove(ctx context.Context, hostname, _ string) error {
	var out map[string]any
	err := f.query(ctx, `mutation($appId: ID!, $hostname: String!) { deleteCertificate(appId: $appId, hostname: $hostname) { app { name } } }`,
		map[string]any{"appId": f.App, "hostname": hostname}, &out)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil
	}
	return err
}

func (c flyCert) edge() Edge {
	e := Edge{Ref: c.Hostname, SSLStatus: c.ClientStatus}
	e.Ready = c.Configured && strings.EqualFold(c.ClientStatus, "Ready")
	if !e.Ready {
		e.Message = "Fly reports: " + c.ClientStatus
	}
	return e
}
