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
	const gone = "This guest link has expired or was already claimed. Guest pages last 24 hours."
	id, err := guest.VerifyClaim(h.Cfg.KeySecret, code)
	if err != nil {
		return store.Tenant{}, "This claim link is not valid."
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
		http.SetCookie(w, &http.Cookie{Name: claimCookie, Value: code, Path: "/app", MaxAge: 3600,
			HttpOnly: true, Secure: auth.SecureOrigin(h.Cfg.APIOrigin), SameSite: http.SameSiteLaxMode})
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
	h.render(w, r, 200, "claim", "Keep your guest pages", v, claimData{Code: code, Pages: rows, ExpiresAt: guest.ExpiresAt(t.CreatedAt)})
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request, v *viewer) {
	t, msg := h.guestToClaim(r, r.PathValue("code"))
	if msg != "" {
		h.renderError(w, r, http.StatusGone, msg)
		return
	}
	_, err := h.Store.ClaimGuest(r.Context(), t.ID, v.Tenant.ID, h.Now())
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, r, http.StatusGone, "This guest link has expired or was already claimed. Guest pages last 24 hours.")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/keys?claimed=1", http.StatusSeeOther)
}

// resumeClaim sends someone who just signed in back to the claim link they
// opened signed out. It reports whether it redirected.
func resumeClaim(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(claimCookie)
	if err != nil || c.Value == "" || len(c.Value) > 256 {
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: claimCookie, Value: "", Path: "/app", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/app/claim/"+c.Value, http.StatusFound)
	return true
}
