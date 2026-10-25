// Package auth signs users in and keeps them signed in: browser sessions,
// CSRF tokens, GitHub OAuth and email magic links.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/store"
)

const (
	// CookieName carries the raw session token over plain http (local
	// work), scoped to /app. Over https the cookie is HostCookie(CookieName):
	// __Host-artifacts_session, Secure, Path=/ and host-only, so no other
	// host, page hosts under the same domain included, can set or overwrite
	// it. Before that it was artifacts_session at /app over https too; that
	// name is still read for sessions it already holds (see Load).
	CookieName = "artifacts_session"
	cookiePath = "/app"
	// SessionTTL is how long a session lives without activity.
	SessionTTL = 30 * 24 * time.Hour
	// refreshEvery bounds how often a request extends the session.
	refreshEvery = time.Hour
)

// ErrNoSession means the request carries no valid, unexpired session.
var ErrNoSession = errors.New("no session")

// Sessions creates, loads and destroys browser sessions.
type Sessions struct {
	Store  store.AuthStore
	Now    func() time.Time
	Secure bool // set the Secure cookie attribute
}

// SecureOrigin reports whether cookies for origin may carry Secure.
func SecureOrigin(origin string) bool { return !strings.HasPrefix(origin, "http://") }

// HostCookie names and scopes one of the app's own cookies. Over https it
// is __Host-{name} at Path=/: the browser then refuses any version of it
// that is not Secure, host-only and at /, so a page on a sibling host
// ({slug}.byagent.dev) can neither plant one with Domain=byagent.dev nor
// shadow it with a narrower path. Over http (local work) the prefix is not
// allowed, and the plain name at /app is used.
func HostCookie(name string, secure bool) (cookieName, path string) {
	if secure {
		return "__Host-" + name, "/"
	}
	return name, cookiePath
}

// legacyBefore bounds the old https session cookie, artifacts_session at
// /app, which a page host under the same registrable domain could plant
// (it has no __Host- prefix). Only sessions created before this process
// started can still be under that name, because this process writes only
// the new one; a session created since is never accepted under it.
var legacyBefore = time.Now()

// SignIn links the verified identity to a user and starts a session.
func (s *Sessions) SignIn(ctx context.Context, w http.ResponseWriter, provider, subject, email, name string) (store.User, store.Tenant, error) {
	u, t, err := s.Store.SignIn(ctx, provider, subject, email, name, s.Now())
	if err != nil {
		return store.User{}, store.Tenant{}, err
	}
	if _, err := s.Create(ctx, w, u.ID, t.ID); err != nil {
		return store.User{}, store.Tenant{}, err
	}
	return u, t, nil
}

// Create starts a session for user in tenant and sets the cookie.
func (s *Sessions) Create(ctx context.Context, w http.ResponseWriter, userID, tenantID string) (store.Session, error) {
	raw := ids.NewToken()
	now := s.Now()
	sess := store.Session{ID: ids.HashKey(raw), UserID: userID, TenantID: tenantID, CreatedAt: now, ExpiresAt: now.Add(SessionTTL), LastSeenAt: now}
	if err := s.Store.CreateSession(ctx, sess); err != nil {
		return store.Session{}, err
	}
	s.setCookie(w, raw, sess.ExpiresAt)
	return sess, nil
}

// Load resolves the request's session. A session seen more than an hour ago
// is extended by SessionTTL from now and its cookie re-issued.
//
// For one release it also accepts the session cookie's old https name
// (artifacts_session at /app) for sessions that already existed, and moves
// them to the new name, so nobody is signed out by the rename.
func (s *Sessions) Load(ctx context.Context, w http.ResponseWriter, r *http.Request) (store.Session, error) {
	name, _ := HostCookie(CookieName, s.Secure)
	ck, err := r.Cookie(name)
	legacy := false
	if (err != nil || ck.Value == "") && s.Secure {
		ck, err = r.Cookie(CookieName)
		legacy = true
	}
	if err != nil || ck.Value == "" {
		return store.Session{}, ErrNoSession
	}
	sess, err := s.Store.Session(ctx, ids.HashKey(ck.Value))
	if errors.Is(err, store.ErrNotFound) {
		s.ClearCookie(w)
		return store.Session{}, ErrNoSession
	}
	if err != nil {
		return store.Session{}, err
	}
	now := s.Now()
	if !sess.ExpiresAt.After(now) {
		s.ClearCookie(w)
		return store.Session{}, ErrNoSession
	}
	if legacy {
		if !sess.CreatedAt.Before(legacyBefore) {
			// Written under the old name after the rename: not ours.
			s.clearLegacy(w)
			return store.Session{}, ErrNoSession
		}
		s.clearLegacy(w)
		s.setCookie(w, ck.Value, sess.ExpiresAt)
	}
	if now.Sub(sess.LastSeenAt) < refreshEvery {
		return sess, nil
	}
	expires := now.Add(SessionTTL)
	if err := s.Store.RefreshSession(ctx, sess.ID, now, expires); err != nil {
		return store.Session{}, err
	}
	sess.LastSeenAt, sess.ExpiresAt = now, expires
	s.setCookie(w, ck.Value, expires)
	return sess, nil
}

// Destroy deletes the request's session, if any, and clears the cookie.
func (s *Sessions) Destroy(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	s.ClearCookie(w)
	name, _ := HostCookie(CookieName, s.Secure)
	ck, err := r.Cookie(name)
	if (err != nil || ck.Value == "") && s.Secure {
		ck, err = r.Cookie(CookieName)
	}
	if err != nil || ck.Value == "" {
		return nil
	}
	return s.Store.DeleteSession(ctx, ids.HashKey(ck.Value))
}

func (s *Sessions) setCookie(w http.ResponseWriter, raw string, expires time.Time) {
	name, path := HostCookie(CookieName, s.Secure)
	http.SetCookie(w, &http.Cookie{Name: name, Value: raw, Path: path, Expires: expires,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode})
}

// ClearCookie tells the browser to drop the session cookie, under its old
// https name too.
func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	name, path := HostCookie(CookieName, s.Secure)
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1,
		HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode})
	if s.Secure {
		s.clearLegacy(w)
	}
}

func (s *Sessions) clearLegacy(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: cookiePath, MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// CSRFToken derives the form token for a session: the first 32 hex chars of
// sha256(session id). It is bound to the session and needs no storage.
func CSRFToken(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])[:32]
}

// CheckCSRF compares a submitted token against the session's in constant time.
func CheckCSRF(sessionID, given string) bool {
	return subtle.ConstantTimeCompare([]byte(CSRFToken(sessionID)), []byte(given)) == 1
}
