package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The bare root redirects to the dashboard when the web app is mounted and
// stays a JSON 404 for API-only deployments.
func TestRootRedirectsToDashboardWhenWebMounted(t *testing.T) {
	_, d, _ := newTestServer(t, true)
	d.Web = func(*http.ServeMux) {}
	h, err := NewHandler(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "http://api.test/app/" {
		t.Fatalf("got %d %q, want 302 to http://api.test/app/", res.StatusCode, res.Header.Get("Location"))
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
