package app

import (
	"errors"
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/guest"
	"github.com/anup-a/artifacts/internal/store"
)

// claimCookie carries a claim link through sign-in: someone who opens it
// signed out is sent to /app/login, and /app/ sends them back here after.
const claimCookie = "byagent_claim"

type claimData struct {
	Code      string
	Pages     []store.Artifact
	ExpiresAt time.Time
}

// guestToClaim resolves a claim code to a guest workspace that can still be
// claimed, or explains why not.
func (h *Handler) guestToClaim(r *http.Request, code string) (store.Tenant, string) {
	gone := tr(r, "This guest link has expired or was already claimed. Guest pages last 24 hours.")
	id, err := guest.VerifyClaim(h.Cfg.KeySecret, code)
	if err != nil {
		return store.Tenant{}, tr(r, "This claim link is not valid.")
	}
	t, err := h.Store.Tenant(r.Context(), id)
	if err != nil || t.Plan != guest.Plan || !h.Now().Before(guest.ExpiresAt(t.CreatedAt)) {
		return t, gone
	}
	return t, ""
}

func (h *Handler) claimPage(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	v, err := h.load(r.Context(), w, r)
	if errors.Is(err, auth.ErrNoSession) {
		h.setAppCookie(w, claimCookie, code, 3600)
		http.Redirect(w, r, "/app/login", http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	t, msg := h.guestToClaim(r, code)
	if msg != "" {
		h.renderError(w, r, http.StatusGone, msg)
		return
	}
	rows, err := h.Store.ListArtifacts(r.Context(), t.ID, store.ArtifactFilter{Limit: guest.Artifacts * 4})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "claim", tr(r, "Keep your guest pages"), v, claimData{Code: code, Pages: rows, ExpiresAt: guest.ExpiresAt(t.CreatedAt)})
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request, v *viewer) {
	t, msg := h.guestToClaim(r, r.PathValue("code"))
	if msg != "" {
		h.renderError(w, r, http.StatusGone, msg)
		return
	}
	_, err := h.Store.ClaimGuest(r.Context(), t.ID, v.Tenant.ID, h.Now())
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, r, http.StatusGone, tr(r, "This guest link has expired or was already claimed. Guest pages last 24 hours."))
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/connect?claimed=1", http.StatusSeeOther)
}

// resumeClaim sends someone who just signed in back to the claim link they
// opened signed out. It reports whether it redirected.
func (h *Handler) resumeClaim(w http.ResponseWriter, r *http.Request) bool {
	v := h.appCookie(r, claimCookie)
	if v == "" || len(v) > 256 {
		return false
	}
	h.setAppCookie(w, claimCookie, "", -1)
	http.Redirect(w, r, "/app/claim/"+v, http.StatusFound)
	return true
}

// setAppCookie writes one of the dashboard's short-lived cookies on the
// account origin: __Host- prefixed, Secure, Path=/ and host-only over https
// (auth.HostCookie), so a page on a sibling host cannot plant one. maxAge -1
// clears it.
func (h *Handler) setAppCookie(w http.ResponseWriter, base, value string, maxAge int) {
	secure := auth.SecureOrigin(h.Cfg.APIOrigin)
	name, path := auth.HostCookie(base, secure)
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

// appCookie reads one of them, "" when absent.
func (h *Handler) appCookie(r *http.Request, base string) string {
	name, _ := auth.HostCookie(base, auth.SecureOrigin(h.Cfg.APIOrigin))
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
