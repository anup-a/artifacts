package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeGitHub imitates the three GitHub endpoints the sign-in touches.
type fakeGitHub struct {
	srv      *httptest.Server
	code     string
	emails   string
	tokenReq url.Values
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{code: "good-code", emails: `[{"email":"old@x.io","primary":false,"verified":true},{"email":"Me@X.io","primary":true,"verified":true}]`}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.tokenReq = r.PostForm
		if r.Header.Get("Accept") != "application/json" || r.PostForm.Get("code") != f.code {
			w.Write([]byte(`{"error":"bad_verification_code"}`))
			return
		}
		w.Write([]byte(`{"access_token":"gho_test","token_type":"bearer"}`))
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_test" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"id":4242,"login":"me","name":"Me Person"}`))
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gho_test" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(f.emails))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func githubFor(t *testing.T) (*GitHub, *fakeGitHub, *clock) {
	t.Helper()
	f := newFakeGitHub(t)
	c := &clock{t: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	g := &GitHub{ClientID: "cid", ClientSecret: "csec", Store: newStore(t), Now: c.now,
		RedirectURL: "http://api.test/app/login/github/callback",
		AuthURL:     f.srv.URL + "/login/oauth/authorize", TokenURL: f.srv.URL + "/login/oauth/access_token", APIURL: f.srv.URL}
	return g, f, c
}

func TestGitHubStartThenCallback(t *testing.T) {
	g, f, _ := githubFor(t)
	redirect, err := g.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(redirect)
	if err != nil || !strings.HasPrefix(redirect, f.srv.URL+"/login/oauth/authorize?") {
		t.Fatalf("%q %v", redirect, err)
	}
	q := u.Query()
	state := q.Get("state")
	if q.Get("client_id") != "cid" || q.Get("scope") != "read:user user:email" || q.Get("redirect_uri") != g.RedirectURL || len(state) != 43 {
		t.Fatalf("query %v", q)
	}
	p, err := g.Callback(context.Background(), state, "good-code")
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "4242" || p.Email != "me@x.io" || p.Name != "Me Person" {
		t.Fatalf("%+v", p)
	}
	if f.tokenReq.Get("client_secret") != "csec" || f.tokenReq.Get("redirect_uri") != g.RedirectURL {
		t.Fatalf("token request %v", f.tokenReq)
	}
	// State is single use.
	if _, err := g.Callback(context.Background(), state, "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatal(err)
	}
}

func TestGitHubCallbackRejectsBadStateExpiredStateAndBadCode(t *testing.T) {
	g, _, c := githubFor(t)
	if _, err := g.Callback(context.Background(), "made-up", "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatal(err)
	}
	redirect, _ := g.Start(context.Background())
	u, _ := url.Parse(redirect)
	state := u.Query().Get("state")
	c.t = c.t.Add(StateTTL + time.Second)
	if _, err := g.Callback(context.Background(), state, "good-code"); !errors.Is(err, ErrBadState) {
		t.Fatal(err)
	}
	redirect, _ = g.Start(context.Background())
	u, _ = url.Parse(redirect)
	if _, err := g.Callback(context.Background(), u.Query().Get("state"), "wrong"); err == nil || errors.Is(err, ErrBadState) {
		t.Fatalf("bad code: %v", err)
	}
}

func TestGitHubNeedsVerifiedEmail(t *testing.T) {
	g, f, _ := githubFor(t)
	f.emails = `[{"email":"me@x.io","primary":true,"verified":false}]`
	redirect, _ := g.Start(context.Background())
	u, _ := url.Parse(redirect)
	if _, err := g.Callback(context.Background(), u.Query().Get("state"), "good-code"); !errors.Is(err, ErrNoVerifiedEmail) {
		t.Fatal(err)
	}
	// Without a primary, any verified address will do.
	f.emails = `[{"email":"a@x.io","primary":false,"verified":false},{"email":"b@x.io","primary":false,"verified":true}]`
	redirect, _ = g.Start(context.Background())
	u, _ = url.Parse(redirect)
	p, err := g.Callback(context.Background(), u.Query().Get("state"), "good-code")
	if err != nil || p.Email != "b@x.io" {
		t.Fatalf("%+v %v", p, err)
	}
}
