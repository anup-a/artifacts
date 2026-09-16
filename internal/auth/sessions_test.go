package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/store"
)

func newStore(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, config.Config{DB: "sqlite", Storage: "local", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func sessionsFor(t *testing.T) (*Sessions, *clock, store.Store) {
	t.Helper()
	st := newStore(t)
	c := &clock{t: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	return &Sessions{Store: st, Now: c.now, Secure: true}, c, st
}

func cookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func requestWith(c *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/app/", nil)
	if c != nil {
		r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	return r
}

func TestSignInCreatesSessionAndCookie(t *testing.T) {
	s, c, st := sessionsFor(t)
	rec := httptest.NewRecorder()
	u, tenant, err := s.SignIn(context.Background(), rec, "email", "a@x.io", "A@x.io ", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "a@x.io" || tenant.Kind != "personal" {
		t.Fatalf("%+v %+v", u, tenant)
	}
	ck := cookieFrom(t, rec)
	if !ck.HttpOnly || !ck.Secure || ck.Path != "/app" || ck.SameSite != http.SameSiteLaxMode || !ck.Expires.Equal(c.t.Add(SessionTTL)) {
		t.Fatalf("cookie %+v", ck)
	}
	// The raw cookie value is never stored: only its hash is.
	if _, err := st.Session(context.Background(), ck.Value); err == nil {
		t.Fatal("raw cookie value found in store")
	}
	sess, err := s.Load(context.Background(), httptest.NewRecorder(), requestWith(ck))
	if err != nil || sess.UserID != u.ID || sess.TenantID != tenant.ID {
		t.Fatalf("%+v %v", sess, err)
	}
}

func TestLoadRejectsMissingBogusAndExpired(t *testing.T) {
	s, c, _ := sessionsFor(t)
	if _, err := s.Load(context.Background(), httptest.NewRecorder(), requestWith(nil)); err != ErrNoSession {
		t.Fatal(err)
	}
	bogus := &http.Cookie{Name: CookieName, Value: "nope"}
	if _, err := s.Load(context.Background(), httptest.NewRecorder(), requestWith(bogus)); err != ErrNoSession {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if _, _, err := s.SignIn(context.Background(), rec, "email", "a@x.io", "a@x.io", ""); err != nil {
		t.Fatal(err)
	}
	ck := cookieFrom(t, rec)
	c.t = c.t.Add(SessionTTL + time.Second)
	rec = httptest.NewRecorder()
	if _, err := s.Load(context.Background(), rec, requestWith(ck)); err != ErrNoSession {
		t.Fatal(err)
	}
	// An expired session clears the cookie so the browser stops sending it.
	if cl := cookieFrom(t, rec); cl.MaxAge != -1 {
		t.Fatalf("expected cleared cookie, got %+v", cl)
	}
}

func TestLoadSlidesExpiryAtMostHourly(t *testing.T) {
	s, c, st := sessionsFor(t)
	rec := httptest.NewRecorder()
	if _, _, err := s.SignIn(context.Background(), rec, "email", "a@x.io", "a@x.io", ""); err != nil {
		t.Fatal(err)
	}
	ck := cookieFrom(t, rec)
	first := c.t
	// Within the hour: no refresh, no new cookie.
	c.t = first.Add(30 * time.Minute)
	rec = httptest.NewRecorder()
	sess, err := s.Load(context.Background(), rec, requestWith(ck))
	if err != nil || !sess.ExpiresAt.Equal(first.Add(SessionTTL)) || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("early refresh: %+v %v cookies=%d", sess, err, len(rec.Result().Cookies()))
	}
	// After the hour: expiry slides and the cookie is re-issued.
	c.t = first.Add(2 * time.Hour)
	rec = httptest.NewRecorder()
	sess, err = s.Load(context.Background(), rec, requestWith(ck))
	if err != nil || !sess.ExpiresAt.Equal(c.t.Add(SessionTTL)) || !sess.LastSeenAt.Equal(c.t) {
		t.Fatalf("slide: %+v %v", sess, err)
	}
	if got := cookieFrom(t, rec); !got.Expires.Equal(c.t.Add(SessionTTL)) || got.Value != ck.Value {
		t.Fatalf("cookie after slide %+v", got)
	}
	stored, _ := st.Session(context.Background(), sess.ID)
	if !stored.ExpiresAt.Equal(c.t.Add(SessionTTL)) {
		t.Fatalf("store not updated: %+v", stored)
	}
	// Session survives past the original expiry thanks to the slide.
	c.t = first.Add(SessionTTL + time.Hour)
	if _, err := s.Load(context.Background(), httptest.NewRecorder(), requestWith(ck)); err != nil {
		t.Fatal(err)
	}
}

func TestDestroyDeletesSessionAndClearsCookie(t *testing.T) {
	s, _, _ := sessionsFor(t)
	rec := httptest.NewRecorder()
	if _, _, err := s.SignIn(context.Background(), rec, "github", "42", "a@x.io", "A"); err != nil {
		t.Fatal(err)
	}
	ck := cookieFrom(t, rec)
	rec = httptest.NewRecorder()
	if err := s.Destroy(context.Background(), rec, requestWith(ck)); err != nil {
		t.Fatal(err)
	}
	if cl := cookieFrom(t, rec); cl.MaxAge != -1 || cl.Value != "" {
		t.Fatalf("cookie not cleared: %+v", cl)
	}
	if _, err := s.Load(context.Background(), httptest.NewRecorder(), requestWith(ck)); err != ErrNoSession {
		t.Fatal(err)
	}
	// Destroying without a cookie is a no-op, not an error.
	if err := s.Destroy(context.Background(), httptest.NewRecorder(), requestWith(nil)); err != nil {
		t.Fatal(err)
	}
}

func TestInsecureCookieForPlainHTTP(t *testing.T) {
	s, _, _ := sessionsFor(t)
	s.Secure = false
	rec := httptest.NewRecorder()
	if _, _, err := s.SignIn(context.Background(), rec, "email", "a@x.io", "a@x.io", ""); err != nil {
		t.Fatal(err)
	}
	if ck := cookieFrom(t, rec); ck.Secure {
		t.Fatal("Secure set on plain http origin")
	}
	if SecureOrigin("http://localhost:8080") || !SecureOrigin("https://api.example.com") {
		t.Fatal("SecureOrigin")
	}
}

func TestCSRF(t *testing.T) {
	tok := CSRFToken("abc")
	if len(tok) != 32 || strings.ToLower(tok) != tok {
		t.Fatal(tok)
	}
	if !CheckCSRF("abc", tok) || CheckCSRF("abc", tok[:31]+"0") || CheckCSRF("abd", tok) || CheckCSRF("abc", "") {
		t.Fatal("csrf compare")
	}
}
