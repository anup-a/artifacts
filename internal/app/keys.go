package app

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

const maxNameLen = 64

type keysData struct {
	Rows   []store.Key
	NewKey string
	Error  string
	API    string // this server's API origin, for the login line
}

func (h *Handler) keys(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.renderKeys(w, r, 200, v, "", "")
}

func (h *Handler) renderKeys(w http.ResponseWriter, r *http.Request, status int, v *viewer, newKey, msg string) {
	rows, err := h.Store.Keys(r.Context(), v.Tenant.ID, h.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := keysData{Rows: rows, NewKey: newKey, Error: msg, API: h.Cfg.APIOrigin}
	h.render(w, r, status, "keys", tr(r, "API keys"), v, d)
}

// cleanName trims and bounds a user-supplied label; ok is false when empty
// or too long.
func cleanName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", false
	}
	return name, true
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request, v *viewer) {
	name, ok := cleanName(r.PostForm.Get("name"))
	if !ok {
		h.renderKeys(w, r, 400, v, "", tr(r, "Give the key a name of 1 to 64 characters."))
		return
	}
	if ok, _ := h.KeyLimiter.Allow(v.Tenant.ID); !ok {
		h.renderError(w, r, 429, tr(r, "Too many keys created recently. Try again in an hour."))
		return
	}
	plain, hash := ids.NewKey()
	if _, err := h.Store.CreateTenantKey(r.Context(), v.Tenant.ID, hash, v.User.Email, name, plain[:9], h.Now()); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderKeys(w, r, 200, v, plain, "")
}

func (h *Handler) revokeKey(w http.ResponseWriter, r *http.Request, v *viewer) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderError(w, r, 404, tr(r, "That key does not exist."))
		return
	}
	if err := h.Store.RevokeKey(r.Context(), v.Tenant.ID, id, h.Now()); err != nil {
		h.storeErr(w, r, err, tr(r, "That key does not exist."))
		return
	}
	http.Redirect(w, r, "/app/keys", http.StatusFound)
}
