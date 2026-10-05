package app

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
)

type captureSender struct{ to, url string }

func (c *captureSender) SendMagicLink(_ context.Context, to, u string) error {
	c.to, c.url = to, u
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// env is one running app over a fresh sqlite store.
type env struct {
	t     *testing.T
	srv   *httptest.Server
	h     *Handler
	st    store.Store
	snd   *captureSender
	clock *clock
	cl    *http.Client
}

func baseConfig(dir string) config.Config {
	return config.Config{DB: "sqlite", Storage: "local", DataDir: dir, KeySecret: "test-secret",
		EmailProvider: "log", APIOrigin: "http://api.test", ShareOrigin: "http://share.test"}
}

func newEnv(t *testing.T, cfg config.Config, tweak func(*Handler)) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &clock{t: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	h := New(st, cfg, c.now)
	snd := &captureSender{}
	if h.Magic != nil {
		h.Magic.Send = snd
	}
	if tweak != nil {
		tweak(h)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, h: h, st: st, snd: snd, clock: c, cl: newClient(t)}
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func fixture(t *testing.T) *env {
	t.Helper()
	return newEnv(t, baseConfig(t.TempDir()), nil)
}

type reply struct {
	*http.Response
	Body string
}

func (e *env) do(cl *http.Client, method, path string, form url.Values) reply {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, e.srv.URL+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return reply{res, string(b)}
}

func (e *env) get(path string) reply { return e.do(e.cl, "GET", path, nil) }

// post sends a form with the CSRF token taken from the page the form lives on.
func (e *env) post(path string, from string, form url.Values) reply {
	e.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if from != "" {
		form.Set("csrf", csrfOf(e.t, e.get(from).Body))
	}
	return e.do(e.cl, "POST", path, form)
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

func csrfOf(t *testing.T, body string) string {
	t.Helper()
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csrf field in page")
	}
	return m[1]
}

// signIn walks the magic-link flow for email and leaves the cookie in the jar.
func (e *env) signIn(email string) {
	e.t.Helper()
	res := e.do(e.cl, "POST", "/app/login/email", url.Values{"email": {email}})
	if res.StatusCode != 200 || !strings.Contains(res.Body, "Check your inbox") {
		e.t.Fatalf("email start: %d %s", res.StatusCode, res.Body)
	}
	u, err := url.Parse(e.snd.url)
	if err != nil || u.Query().Get("token") == "" {
		e.t.Fatalf("magic link %q: %v", e.snd.url, err)
	}
	res = e.get(u.Path + "?" + u.RawQuery)
	if res.StatusCode != 302 || res.Header.Get("Location") != "/app/" {
		e.t.Fatalf("verify: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func (e *env) me() (store.User, store.Tenant) {
	e.t.Helper()
	sess := e.session()
	u, err := e.st.User(context.Background(), sess.UserID)
	if err != nil {
		e.t.Fatal(err)
	}
	t, err := e.st.Tenant(context.Background(), sess.TenantID)
	if err != nil {
		e.t.Fatal(err)
	}
	return u, t
}

func (e *env) session() store.Session {
	e.t.Helper()
	u, _ := url.Parse(e.srv.URL + "/app/")
	for _, c := range e.cl.Jar.Cookies(u) {
		if c.Name == auth.CookieName {
			s, err := e.st.Session(context.Background(), ids.HashKey(c.Value))
			if err != nil {
				e.t.Fatal(err)
			}
			return s
		}
	}
	e.t.Fatal("not signed in")
	return store.Session{}
}

func want(t *testing.T, res reply, status int, contains ...string) {
	t.Helper()
	if res.StatusCode != status {
		t.Fatalf("status %d, want %d: %s", res.StatusCode, status, res.Body)
	}
	for _, s := range contains {
		if !strings.Contains(res.Body, s) {
			t.Fatalf("body lacks %q:\n%s", s, res.Body)
		}
	}
}

func wantRedirect(t *testing.T, res reply, to string) {
	t.Helper()
	if res.StatusCode != 302 || res.Header.Get("Location") != to {
		t.Fatalf("got %d %q, want 302 %q", res.StatusCode, res.Header.Get("Location"), to)
	}
}

func TestUnconfiguredServerShows503(t *testing.T) {
	cfg := baseConfig(t.TempDir())
	cfg.EmailProvider = ""
	e := newEnv(t, cfg, nil)
	want(t, e.get("/app/login"), 503, "Sign-in is not configured on this server")
	want(t, e.get("/app/artifacts"), 503, "Sign-in is not configured")
	want(t, e.get("/app/login/github"), 503)
}

func TestSignInFlowSecurityHeadersAndStatics(t *testing.T) {
	e := fixture(t)
	wantRedirect(t, e.get("/app/artifacts"), "/app/login")
	res := e.get("/app/login")
	want(t, res, 200, `action="/app/login/email"`, "Share a page", "Keep the comments")
	if strings.Contains(res.Body, "Continue with GitHub") {
		t.Fatal("GitHub button shown without a client id")
	}
	for k, v := range map[string]string{
		"Content-Security-Policy": e.h.csp(), "X-Frame-Options": "DENY", "Referrer-Policy": "same-origin",
		"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
	} {
		if got := res.Header.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	e.signIn("Someone@Example.com")
	if e.snd.to != "someone@example.com" || !strings.HasPrefix(e.snd.url, "http://api.test/app/login/email/verify?token=") {
		t.Fatalf("mail %+v", e.snd)
	}
	wantRedirect(t, e.get("/app/login"), "/app/artifacts")
	wantRedirect(t, e.get("/app/"), "/app/artifacts")
	res = e.get("/app/artifacts")
	want(t, res, 200, "Publish your first page", "someone@example.com", "npx -y byagent@latest login --code ", "/skill.md", `href="/app/connect"`, `<a href="/app/keys"`)
	if strings.Contains(res.Body, "End users") {
		t.Fatal("personal tenant shows End users")
	}
	if strings.Contains(res.Body, "<style") || strings.Contains(res.Body, "onclick") {
		t.Fatal("inline style or script")
	}
	css := regexp.MustCompile(`href="(/app/static/app\.[0-9a-f]{12}\.css)"`).FindStringSubmatch(res.Body)
	js := regexp.MustCompile(`src="(/app/static/app\.[0-9a-f]{12}\.js)"`).FindStringSubmatch(res.Body)
	if css == nil || js == nil {
		t.Fatalf("no hashed assets in page")
	}
	for _, p := range []string{css[1], js[1]} {
		r := e.get(p)
		if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") || len(r.Body) < 100 {
			t.Fatalf("static %s: %d %q", p, r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	want(t, e.get("/app/static/app.000000000000.css"), 404)
	want(t, e.get("/app/nope"), 404, "That page does not exist")
}

func TestBadEmailBadLinkAndRateLimits(t *testing.T) {
	e := newEnv(t, baseConfig(t.TempDir()), func(h *Handler) {
		h.EmailLimiter = ratelimit.NewWindow(1, time.Hour)
		h.IPLimiter = ratelimit.NewWindow(2, time.Hour)
	})
	want(t, e.do(e.cl, "POST", "/app/login/email", url.Values{"email": {"not an address"}}), 400, "does not look like an email")
	want(t, e.get("/app/login/email/verify?token=bogus"), 400, "invalid, used, or expired")
	want(t, e.do(e.cl, "POST", "/app/login/email", url.Values{"email": {"a@x.io"}}), 200, "Check your inbox", "a@x.io", "Works once")
	want(t, e.do(e.cl, "POST", "/app/login/email", url.Values{"email": {"a@x.io"}}), 429, "for that address")
	want(t, e.do(e.cl, "POST", "/app/login/email", url.Values{"email": {"b@x.io"}}), 200)
	want(t, e.do(e.cl, "POST", "/app/login/email", url.Values{"email": {"c@x.io"}}), 429, "from this network")
	// A used link is single use.
	e2 := fixture(t)
	e2.signIn("a@x.io")
	u, _ := url.Parse(e2.snd.url)
	want(t, e2.get(u.Path+"?"+u.RawQuery), 400, "invalid, used, or expired")
}

func TestCSRFAndLogout(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	want(t, e.do(e.cl, "POST", "/app/keys", url.Values{"name": {"x"}}), 403, "form has expired")
	want(t, e.do(e.cl, "POST", "/app/keys", url.Values{"name": {"x"}, "csrf": {"0000"}}), 403)
	want(t, e.post("/app/keys", "/app/keys", url.Values{"name": {"x"}}), 200, "only time the key is shown")
	wantRedirect(t, e.post("/app/logout", "/app/keys", nil), "/app/login")
	wantRedirect(t, e.get("/app/keys"), "/app/login")
}

func TestSessionRefreshesAndExpires(t *testing.T) {
	e := fixture(t)
	e.signIn("a@x.io")
	first := e.session()
	e.clock.t = e.clock.t.Add(2 * time.Hour)
	want(t, e.get("/app/keys"), 200)
	if got := e.session(); !got.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("session not refreshed: %v -> %v", first.ExpiresAt, got.ExpiresAt)
	}
	e.clock.t = e.clock.t.Add(auth.SessionTTL + time.Hour)
	wantRedirect(t, e.get("/app/keys"), "/app/login")
}

func TestGitHubSignIn(t *testing.T) {
	fake := newFakeGitHub(t)
	cfg := baseConfig(t.TempDir())
	cfg.GitHubClientID, cfg.GitHubClientSecret = "cid", "csec"
	e := newEnv(t, cfg, func(h *Handler) {
		h.GitHub.AuthURL = fake.URL + "/login/oauth/authorize"
		h.GitHub.TokenURL = fake.URL + "/login/oauth/access_token"
		h.GitHub.APIURL = fake.URL
	})
	want(t, e.get("/app/login"), 200, "Continue with GitHub")
	res := e.get("/app/login/github")
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), fake.URL+"/login/oauth/authorize?") {
		t.Fatalf("start: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	state := loc.Query().Get("state")
	if loc.Query().Get("redirect_uri") != "http://api.test/app/login/github/callback" || state == "" {
		t.Fatalf("authorize params %v", loc.Query())
	}
	want(t, e.get("/app/login/github/callback?error=access_denied"), 400, "did not complete")
	want(t, e.get("/app/login/github/callback?state=wrong&code=good-code"), 400, "expired")
	wantRedirect(t, e.get("/app/login/github/callback?state="+state+"&code=good-code"), "/app/")
	want(t, e.get("/app/settings"), 200, "me@x.io", "github", "4242")

	plain := fixture(t)
	want(t, plain.get("/app/login/github"), 404)
	want(t, plain.get("/app/login/github/callback?state=x&code=y"), 404)
}

// newFakeGitHub is the minimal GitHub used by the sign-in test.
func newFakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") != "good-code" {
			_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"gho_test","token_type":"bearer"}`))
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":4242,"login":"me","name":"Me Person"}`))
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"email":"Me@X.io","primary":true,"verified":true}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFormatHelpers(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"}
	for n, s := range cases {
		if got := formatBytes(n); got != s {
			t.Errorf("bytes(%d) = %q, want %q", n, got, s)
		}
	}
	nums := map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -4321: "-4,321"}
	for n, s := range nums {
		if got := formatNumber(n); got != s {
			t.Errorf("num(%d) = %q, want %q", n, got, s)
		}
	}
	if formatTime(time.Time{}) != "" || formatTime(time.Date(2026, 9, 16, 1, 2, 3, 0, time.FixedZone("x", 3600))) != "2026-09-16 00:02" {
		t.Fatal("time format")
	}
	if got := clientIP(&http.Request{RemoteAddr: "10.0.0.1:1234", Header: http.Header{"X-Forwarded-For": {"1.2.3.4, 5.6.7.8"}}}, true); got != "1.2.3.4" {
		t.Fatalf("proxy ip %q", got)
	}
	if got := clientIP(&http.Request{RemoteAddr: "10.0.0.1:1234", Header: http.Header{"X-Forwarded-For": {"1.2.3.4"}}}, false); got != "10.0.0.1" {
		t.Fatalf("direct ip %q", got)
	}
}
