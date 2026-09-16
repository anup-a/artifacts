package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8)
		rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(apierr.WithRequestID(r.Context(), id)))
	})
}

func (s *server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		tok := strings.TrimPrefix(authHeader, "Bearer ")
		if tok == "" || tok == authHeader {
			apierr.Write(w, r, apierr.Unauthorized())
			return
		}
		k, ok := s.authenticate(r, tok)
		if !ok {
			apierr.Write(w, r, apierr.Unauthorized())
			return
		}
		next(w, r.WithContext(withKey(r.Context(), k)))
	}
}

// authenticate resolves a bearer token to a key. ADMIN_TOKEN, when configured,
// is accepted next to keys created in the app; revoked keys are rejected.
func (s *server) authenticate(r *http.Request, tok string) (store.Key, bool) {
	if s.adminKey != nil && subtle.ConstantTimeCompare([]byte(tok), []byte(s.Cfg.AdminToken)) == 1 {
		return *s.adminKey, true
	}
	k, err := s.Store.KeyByHash(r.Context(), ids.HashKey(tok))
	if err != nil || k.RevokedAt != nil {
		return store.Key{}, false
	}
	if err := s.Store.TouchKey(r.Context(), k.ID, s.Now()); err != nil {
		slog.Warn("touch key", "key", k.ID, "err", err)
	}
	return k, true
}

// clientIP identifies the caller for rate-limit bucketing.
//
// Proxy headers are attacker-controlled on a directly-reachable server, so a
// client could mint a fresh bucket per request just by varying X-Forwarded-For.
// They are therefore consulted only when trustProxy is set (TRUST_PROXY_HEADERS),
// which is correct only when the server is reachable exclusively through a proxy
// that overwrites them (e.g. Cloudflare). Otherwise the transport peer address
// wins.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := r.Header.Get("CF-Connecting-IP"); v != "" {
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
