package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The bare root belongs to the web app when one is mounted, and stays a JSON
// 404 for API-only deployments.
func TestRootBelongsToTheWebAppWhenMounted(t *testing.T) {
	_, d, _ := newTestServer(t, true)
	d.Web = func(m *http.ServeMux) {
		m.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("landing")) })
	}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(b) != "landing" {
		t.Fatalf("got %d %q, want 200 landing", res.StatusCode, b)
	}
}

func TestRootIsNotFoundWithoutWeb(t *testing.T) {
	srv, _, _ := newTestServer(t, true)
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", res.StatusCode)
	}
}
