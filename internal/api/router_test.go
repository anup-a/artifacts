package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUnknownRouteReturnsEnvelope(t *testing.T) {
	srv, _, _ := newTestServer(t, false)

	resp, err := http.Get(srv.URL + "/api/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected Content-Type to start with application/json, got %q", ct)
	}

	rid := resp.Header.Get("X-Request-Id")
	if rid == "" {
		t.Error("expected X-Request-Id header to be non-empty")
	}

	// Read body to check for NOT_FOUND
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, "NOT_FOUND") {
		t.Errorf("expected body to contain NOT_FOUND, got: %s", body)
	}
}
