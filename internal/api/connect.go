package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/connect"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

// ClientHeader names the agent or harness behind a CLI call, e.g.
// "claude-code" or "codex". It is a label, not a credential.
const ClientHeader = "X-Byagent-Client"

// clientName reads ClientHeader, keeping it only when it is something a
// version history can show: printable ASCII, at most 64 characters.
func clientName(r *http.Request) string {
	v := r.Header.Get(ClientHeader)
	if len(v) > 64 {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return ""
		}
	}
	return v
}

// pollInterval is how often the CLI is told to ask whether a browser
// approval has happened.
const pollInterval = 2 * time.Second

// connectLimited meters the unauthenticated connect routes per address.
func (s *server) connectLimited(w http.ResponseWriter, r *http.Request, scope string) bool {
	w.Header().Set("Cache-Control", "private, no-store")
	if ok, retry := s.ConnectLimiter.Allow(scope + ":" + clientIP(r, s.Cfg.TrustProxyHeaders)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return true
	}
	return false
}

// redeemConnect answers POST /api/connect: an agent trades a code someone
// made in the dashboard for a key of its own. The code works once.
func (s *server) redeemConnect(w http.ResponseWriter, r *http.Request) {
	if s.connectLimited(w, r, "redeem") {
		return
	}
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if e := decodeJSON(w, r, 4096, &body); e != nil {
		apierr.Write(w, r, e)
		return
	}
	hash := connect.Hash(body.Code)
	if hash == "" {
		apierr.Write(w, r, apierr.Invalid("code must look like ABCD-EFGH"))
		return
	}
	s.issueKey(w, r, hash, false, body.Name)
}

// startDevice answers POST /api/connect/device: the CLI asks to be
// connected and gets a code for a person to approve in the browser, plus a
// secret of its own to poll with.
func (s *server) startDevice(w http.ResponseWriter, r *http.Request) {
	if s.connectLimited(w, r, "device") {
		return
	}
	if !s.Cfg.WebEnabled() {
		apierr.Write(w, r, apierr.Unavailable("sign-in is not configured on this server; create a key there and run byagent login --token"))
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if e := decodeJSON(w, r, 4096, &body); e != nil {
		apierr.Write(w, r, e)
		return
	}
	code := connect.NewCode()
	secret, secretHash := connect.NewDeviceSecret()
	now := s.Now()
	c := store.ConnectCode{CodeSHA256: connect.Hash(code), DeviceSHA256: secretHash, Name: connect.KeyName(body.Name),
		CreatedAt: now, ExpiresAt: now.Add(connect.TTL)}
	if err := s.Store.CreateConnect(r.Context(), c); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 201, map[string]any{
		"device_code": secret,
		"user_code":   code,
		"verify_url":  s.Cfg.APIOrigin + "/app/connect?code=" + code,
		"name":        c.Name,
		"expires_at":  c.ExpiresAt.Format(time.RFC3339),
		"interval":    int(pollInterval / time.Second),
	})
}

// pollDevice answers POST /api/connect/device/token. Until someone approves
// the code it says pending; once approved it hands over the key, once.
func (s *server) pollDevice(w http.ResponseWriter, r *http.Request) {
	if s.connectLimited(w, r, "poll") {
		return
	}
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if e := decodeJSON(w, r, 4096, &body); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if body.DeviceCode == "" {
		apierr.Write(w, r, apierr.Invalid("device_code is required"))
		return
	}
	s.issueKey(w, r, connect.HashDeviceSecret(body.DeviceCode), true, "")
}

func (s *server) issueKey(w http.ResponseWriter, r *http.Request, hash string, byDevice bool, name string) {
	plain, keyHash := ids.NewKey()
	k, err := s.Store.RedeemConnect(r.Context(), hash, byDevice, keyHash, plain[:9], connect.KeyName(name), s.Now())
	switch {
	case errors.Is(err, store.ErrPending):
		apierr.WriteJSON(w, 202, map[string]any{"status": "pending", "interval": int(pollInterval / time.Second)})
		return
	case errors.Is(err, store.ErrNotFound):
		apierr.Write(w, r, &apierr.Error{Code: "NOT_FOUND", Status: 404,
			Message: "this connect code has expired, was already used, or does not exist; codes work once and last 10 minutes"})
		return
	case err != nil:
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	t, err := s.Store.Tenant(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	funnel("agent_connected", "tenant", t.ID, "device", byDevice)
	apierr.WriteJSON(w, 201, map[string]any{
		"status":    "approved",
		"token":     plain,
		"api":       s.Cfg.APIOrigin,
		"key":       map[string]any{"id": k.ID, "name": k.Name, "prefix": k.Prefix},
		"workspace": map[string]any{"id": t.ID, "name": t.Name},
	})
}
