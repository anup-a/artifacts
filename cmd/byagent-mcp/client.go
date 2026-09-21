package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// apiClient talks to the byagent owner API with Bearer auth.
// Routes match internal/api/router.go — do not invent endpoints.
type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newClient(cfg config) *apiClient {
	return &apiClient{
		base:  cfg.API,
		token: cfg.Token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}
}

type listParams struct {
	Limit   int
	Cursor  string
	EndUser string
	Project string
	Query   string
}

func (c *apiClient) listArtifacts(ctx context.Context, p listParams) (json.RawMessage, error) {
	q := url.Values{}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	}
	if p.EndUser != "" {
		q.Set("end_user", p.EndUser)
	}
	if p.Project != "" {
		q.Set("project", p.Project)
	}
	if p.Query != "" {
		q.Set("q", p.Query)
	}
	path := "/api/artifacts"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *apiClient) getArtifact(ctx context.Context, id string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, "/api/artifacts/"+url.PathEscape(id), nil)
}

type commentsParams struct {
	Status string // open|resolved|all (API default open)
	Since  string // RFC3339
}

func (c *apiClient) listComments(ctx context.Context, id string, p commentsParams) (json.RawMessage, error) {
	q := url.Values{}
	if p.Status != "" {
		q.Set("status", p.Status)
	}
	if p.Since != "" {
		q.Set("since", p.Since)
	}
	path := "/api/artifacts/" + url.PathEscape(id) + "/comments"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *apiClient) reply(ctx context.Context, id, thread, body string) (json.RawMessage, error) {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return nil, err
	}
	path := "/api/artifacts/" + url.PathEscape(id) + "/comments/" + url.PathEscape(thread) + "/reply"
	return c.do(ctx, http.MethodPost, path, payload)
}

func (c *apiClient) resolve(ctx context.Context, id, thread string) (json.RawMessage, error) {
	path := "/api/artifacts/" + url.PathEscape(id) + "/comments/" + url.PathEscape(thread) + "/resolve"
	return c.do(ctx, http.MethodPost, path, nil)
}

func (c *apiClient) do(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("API %s %s: %s", method, path, msg)
	}
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("API %s %s: non-JSON response", method, path)
	}
	return json.RawMessage(raw), nil
}
