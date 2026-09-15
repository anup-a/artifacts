package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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
