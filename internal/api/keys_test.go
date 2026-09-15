package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCreateKeyManaged(t *testing.T) {
	srv, _, _ := newTestServer(t, true)
	res, err := http.Post(srv.URL+"/api/keys", "application/json", strings.NewReader(`{"email":"a@b.co"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 201 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct{ Key string }
	json.NewDecoder(res.Body).Decode(&body)
	if !strings.HasPrefix(body.Key, "ak_") {
		t.Fatalf("key %q", body.Key)
	}
	// key works
	req, _ := http.NewRequest("GET", srv.URL+"/api/artifacts", nil)
	req.Header.Set("Authorization", "Bearer "+body.Key)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 200 {
		t.Fatalf("auth with new key: %d", res.StatusCode)
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Fatal("missing request id")
	}
}

func TestCreateKeyDisabledSelfHost(t *testing.T) {
	srv, _, _ := newTestServer(t, false)
	res, _ := http.Post(srv.URL+"/api/keys", "application/json", strings.NewReader(`{"email":"a@b.co"}`))
	if res.StatusCode != 404 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestCreateKeyValidation(t *testing.T) {
	srv, _, _ := newTestServer(t, true)
	res, _ := http.Post(srv.URL+"/api/keys", "application/json", strings.NewReader(`{"email":"nope"}`))
	if res.StatusCode != 400 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	res, _ := http.Get(srv.URL + "/api/artifacts")
	if res.StatusCode != 401 {
		t.Fatalf("no auth: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/artifacts", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 401 {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 200 {
		t.Fatalf("admin token: %d", res.StatusCode)
	}
}
