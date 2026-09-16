package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestWebRegistrarAndViewHook checks that a Deps.Web registrar is mounted
// ahead of the JSON catch-all and that serving an artifact index reports
// the view through Deps.OnView.
func TestWebRegistrarAndViewHook(t *testing.T) {
	_, d, tok := newTestServer(t, false)
	views := make(chan string, 1)
	d.Web = func(mux *http.ServeMux) {
		mux.HandleFunc("GET /app/{$}", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("web app")) })
	}
	d.OnView = func(id string) { views <- id }
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(h)
	defer web.Close()

	resp, err := http.Get(web.URL + "/app/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "web app" {
		t.Fatalf("web route: %d %q", resp.StatusCode, body)
	}

	id := publishOne(t, web.URL, tok, "<h1>hi</h1>")
	resp, err = http.Get(web.URL + "/a/" + id + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("serve: %d", resp.StatusCode)
	}
	select {
	case got := <-views:
		if got != id {
			t.Fatalf("view hook got %q, want %q", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("view hook not called")
	}
}

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
