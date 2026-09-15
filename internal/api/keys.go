package api

import (
	"encoding/json"
	"net/http"
	"net/mail"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/ids"
)

func (s *server) createKey(w http.ResponseWriter, r *http.Request) {
	if s.adminKey != nil {
		apierr.Write(w, r, apierr.NotFound("endpoint"))
		return
	}
	if ok, retry := s.KeyLimiter.Allow("ip:" + clientIP(r)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		apierr.Write(w, r, apierr.Invalid("body must be JSON {email}"))
		return
	}
	if _, err := mail.ParseAddress(body.Email); err != nil || len(body.Email) > 254 {
		apierr.Write(w, r, apierr.Invalid("email is invalid"))
		return
	}
	plain, hash := ids.NewKey()
	k, err := s.Store.CreateKey(r.Context(), hash, body.Email)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 201, map[string]any{
		"key":        plain,
		"email":      k.Email,
		"created_at": k.CreatedAt,
		"quota":      map[string]any{"artifacts": s.Cfg.Limits.ArtifactsPerKey, "version_bytes": s.Cfg.Limits.VersionBytes, "versions_kept": s.Cfg.Limits.VersionsKept},
	})
}
