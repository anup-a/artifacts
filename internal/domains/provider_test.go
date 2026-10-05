package domains

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare is just enough of the custom hostnames API.
type fakeCloudflare struct {
	mu      sync.Mutex
	hosts   map[string]map[string]any // id -> hostname object
	auth    []string
	patched int
}

func (f *fakeCloudflare) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		write := func(status int, success bool, result any) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"success": success, "errors": []any{}, "result": result})
		}
		const prefix = "/zones/zone1/custom_hostnames"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected path %s", r.URL.Path)
			write(404, false, nil)
			return
		}
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
		switch {
		case r.Method == "POST" && id == "":
			var in struct {
				Hostname string         `json:"hostname"`
				SSL      map[string]any `json:"ssl"`
			}
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &in)
			if in.SSL["method"] != "http" || in.SSL["type"] != "dv" {
				t.Errorf("ssl settings: %v", in.SSL)
			}
			for _, h := range f.hosts {
				if h["hostname"] == in.Hostname {
					w.WriteHeader(409)
					json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 1406, "message": "Duplicate custom hostname found."}}})
					return
				}
			}
			newID := "ch-" + in.Hostname
			obj := map[string]any{"id": newID, "hostname": in.Hostname, "status": "pending", "verification_errors": []string{"custom hostname does not CNAME to this zone."}, "ssl": map[string]any{"status": "initializing"}}
			f.hosts[newID] = obj
			write(200, true, obj)
		case r.Method == "GET" && id == "":
			out := []any{}
			for _, h := range f.hosts {
				if h["hostname"] == r.URL.Query().Get("hostname") {
					out = append(out, h)
				}
			}
			write(200, true, out)
		case r.Method == "GET":
			h, ok := f.hosts[id]
			if !ok {
				write(404, false, nil)
				return
			}
			write(200, true, h)
		case r.Method == "PATCH":
			f.patched++
			write(200, true, f.hosts[id])
		case r.Method == "DELETE":
			if _, ok := f.hosts[id]; !ok {
				write(404, false, nil)
				return
			}
			delete(f.hosts, id)
			write(200, true, map[string]any{"id": id})
		default:
			write(405, false, nil)
		}
	})
}

func TestCloudflareProvider(t *testing.T) {
	ctx := context.Background()
	fake := &fakeCloudflare{hosts: map[string]map[string]any{}}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	cf := &Cloudflare{Token: "tok", ZoneID: "zone1", BaseURL: srv.URL}

	e, err := cf.Add(ctx, "docs.example.com")
	if err != nil || e.Ref != "ch-docs.example.com" || e.Ready || e.Failed || !strings.Contains(e.Message, "CNAME") {
		t.Fatalf("add: %v %+v", err, e)
	}
	// A second add adopts the existing hostname instead of failing.
	if e, err := cf.Add(ctx, "docs.example.com"); err != nil || e.Ref != "ch-docs.example.com" {
		t.Fatalf("re-add: %v %+v", err, e)
	}
	fake.mu.Lock()
	fake.hosts["ch-docs.example.com"]["status"] = "active"
	fake.hosts["ch-docs.example.com"]["verification_errors"] = []string{}
	fake.hosts["ch-docs.example.com"]["ssl"] = map[string]any{"status": "active"}
	fake.mu.Unlock()
	if e, err := cf.Check(ctx, "docs.example.com", "ch-docs.example.com"); err != nil || !e.Ready || e.SSLStatus != "active" {
		t.Fatalf("check: %v %+v", err, e)
	}
	fake.mu.Lock()
	fake.hosts["ch-docs.example.com"]["status"] = "moved"
	fake.mu.Unlock()
	if e, err := cf.Check(ctx, "docs.example.com", "ch-docs.example.com"); err != nil || e.Ready || !e.Failed || fake.patched != 1 {
		t.Fatalf("moved: %v %+v patched=%d", err, e, fake.patched)
	}
	if err := cf.Remove(ctx, "docs.example.com", "ch-docs.example.com"); err != nil {
		t.Fatal(err)
	}
	// Removing twice, or checking a hostname Cloudflare dropped, still works.
	if err := cf.Remove(ctx, "docs.example.com", "ch-docs.example.com"); err != nil {
		t.Fatal(err)
	}
	if e, err := cf.Check(ctx, "docs.example.com", "ch-docs.example.com"); err != nil || e.Ref != "ch-docs.example.com" {
		t.Fatalf("check after delete re-adds: %v %+v", err, e)
	}
	for _, a := range fake.auth {
		if a != "Bearer tok" {
			t.Fatalf("auth header %q", a)
		}
	}
}

func TestCloudflareErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
	}))
	defer srv.Close()
	cf := &Cloudflare{Token: "bad", ZoneID: "zone1", BaseURL: srv.URL}
	if _, err := cf.Add(context.Background(), "docs.example.com"); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("want the API error, got %v", err)
	}
}

func TestFlyProvider(t *testing.T) {
	var mu sync.Mutex
	certs := map[string]map[string]any{}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth = r.Header.Get("Authorization")
		var in struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		host, _ := in.Variables["hostname"].(string)
		switch {
		case strings.Contains(in.Query, "addCertificate"):
			if in.Variables["appId"] != "artifacts-share" {
				t.Errorf("app %v", in.Variables["appId"])
			}
			if _, ok := certs[host]; ok {
				json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "Hostname already exists on app"}}})
				return
			}
			certs[host] = map[string]any{"hostname": host, "clientStatus": "Awaiting configuration", "configured": false}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"addCertificate": map[string]any{"certificate": certs[host]}}})
		case strings.Contains(in.Query, "deleteCertificate"):
			delete(certs, host)
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"deleteCertificate": map[string]any{"app": map[string]any{"name": "artifacts-share"}}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"app": map[string]any{"certificate": certs[host]}}})
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	fly := &Fly{Token: "FlyV1 fm2_abc", App: "artifacts-share", Endpoint: srv.URL}
	e, err := fly.Add(ctx, "docs.example.com")
	if err != nil || e.Ready || e.Ref != "docs.example.com" {
		t.Fatalf("add: %v %+v", err, e)
	}
	if auth != "FlyV1 fm2_abc" {
		t.Fatalf("auth %q", auth)
	}
	if e, err := fly.Add(ctx, "docs.example.com"); err != nil || e.Ref != "docs.example.com" {
		t.Fatalf("re-add: %v %+v", err, e)
	}
	mu.Lock()
	certs["docs.example.com"]["clientStatus"] = "Ready"
	certs["docs.example.com"]["configured"] = true
	mu.Unlock()
	if e, err := fly.Check(ctx, "docs.example.com", ""); err != nil || !e.Ready {
		t.Fatalf("check: %v %+v", err, e)
	}
	if err := fly.Remove(ctx, "docs.example.com", ""); err != nil {
		t.Fatal(err)
	}
	// Checking a hostname the app lost registers it again.
	if e, err := fly.Check(ctx, "docs.example.com", ""); err != nil || e.Ready {
		t.Fatalf("check after remove: %v %+v", err, e)
	}
	plain := &Fly{Token: "legacy", App: "artifacts-share", Endpoint: srv.URL}
	plain.Check(ctx, "docs.example.com", "")
	if auth != "Bearer legacy" {
		t.Fatalf("legacy auth %q", auth)
	}
}
