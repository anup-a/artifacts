package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
		var k store.Key
		if s.adminKey != nil {
			if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Cfg.AdminToken)) != 1 {
				apierr.Write(w, r, apierr.Unauthorized())
				return
			}
			k = *s.adminKey
		} else {
			got, err := s.Store.KeyByHash(r.Context(), ids.HashKey(tok))
			if err != nil || got.RevokedAt != nil {
				apierr.Write(w, r, apierr.Unauthorized())
				return
			}
			k = got
		}
		next(w, r.WithContext(withKey(r.Context(), k)))
	}
}

// clientIP honours Cloudflare / proxy headers, falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	if v := r.Header.Get("CF-Connecting-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
