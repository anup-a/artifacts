package app

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/i18n"
	"github.com/anup-a/artifacts/internal/ratelimit"
)

// clientIP mirrors the API's rule: proxy headers count only when the server
// is told it sits behind a proxy that overwrites them.
func clientIP(r *http.Request, trustProxy bool) string {
	return ratelimit.ClientKey(clientAddr(r, trustProxy))
}

// clientAddr is the caller's address, before bucketing.
func clientAddr(r *http.Request, trustProxy bool) string {
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

type loginData struct {
	GitHub bool
	Email  bool
	Error  string
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Sessions.Load(r.Context(), w, r); err == nil {
		http.Redirect(w, r, "/app/artifacts", http.StatusFound)
		return
	}
	h.renderLogin(w, r, 200, "")
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, msg string) {
	h.render(w, r, status, "login", tr(r, "Sign in"), nil, loginData{GitHub: h.GitHub != nil, Email: h.Magic != nil, Error: msg})
}

func (h *Handler) githubStart(w http.ResponseWriter, r *http.Request) {
	if h.GitHub == nil {
		h.renderError(w, r, 404, tr(r, "GitHub sign-in is not enabled on this server."))
		return
	}
	url, err := h.GitHub.Start(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (h *Handler) githubCallback(w http.ResponseWriter, r *http.Request) {
	if h.GitHub == nil {
		h.renderError(w, r, 404, tr(r, "GitHub sign-in is not enabled on this server."))
		return
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		h.renderLogin(w, r, 400, tr(r, "GitHub did not complete the sign-in. Try again."))
		return
	}
	p, err := h.GitHub.Callback(r.Context(), q.Get("state"), q.Get("code"))
	switch {
	case errors.Is(err, auth.ErrBadState):
		h.renderLogin(w, r, 400, tr(r, "That sign-in attempt expired. Try again."))
		return
	case errors.Is(err, auth.ErrNoVerifiedEmail):
		h.renderLogin(w, r, 400, tr(r, "Your GitHub account has no verified email address. Add one on GitHub and try again."))
		return
	case err != nil:
		h.fail(w, r, err)
		return
	}
	if _, _, err := h.Sessions.SignIn(r.Context(), w, "github", p.Subject, p.Email, p.Name); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/", http.StatusFound)
}

func (h *Handler) emailStart(w http.ResponseWriter, r *http.Request) {
	if h.Magic == nil {
		h.renderError(w, r, 404, tr(r, "Email sign-in is not enabled on this server."))
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, 400, tr(r, "The form could not be read."))
		return
	}
	email, err := auth.NormalizeEmail(r.PostForm.Get("email"))
	if err != nil {
		h.renderLogin(w, r, 400, tr(r, "That does not look like an email address."))
		return
	}
	ip := clientIP(r, h.Cfg.TrustProxyHeaders)
	// The network's budget first: a client that has run out stops here and
	// cannot fill the email limiter with addresses it makes up.
	if ok, _ := h.IPLimiter.Allow(ip); !ok {
		h.renderError(w, r, 429, tr(r, "Too many sign-in links requested from this network. Try again in an hour."))
		return
	}
	if ok, _ := h.EmailLimiter.Allow(email); !ok {
		h.renderError(w, r, 429, tr(r, "Too many sign-in links requested for that address. Try again in an hour."))
		return
	}
	if err := h.Magic.Issue(r.Context(), email, i18n.Lang(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "check_inbox", tr(r, "Check your inbox"), nil, struct{ Email string }{email})
}

func (h *Handler) emailVerify(w http.ResponseWriter, r *http.Request) {
	if h.Magic == nil {
		h.renderError(w, r, 404, tr(r, "Email sign-in is not enabled on this server."))
		return
	}
	email, err := h.Magic.Verify(r.Context(), r.URL.Query().Get("token"))
	if errors.Is(err, auth.ErrBadLink) {
		h.renderLogin(w, r, 400, tr(r, "That sign-in link is invalid, used, or expired. Request a new one."))
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if _, _, err := h.Sessions.SignIn(r.Context(), w, "email", email, email, ""); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request, _ *viewer) {
	if err := h.Sessions.Destroy(r.Context(), w, r); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/login", http.StatusFound)
}

func (h *Handler) switchTenant(w http.ResponseWriter, r *http.Request, v *viewer) {
	err := h.Store.SwitchTenant(r.Context(), v.Session.ID, v.User.ID, r.PostForm.Get("tenant_id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That workspace does not exist or you are not a member."))
		return
	}
	http.Redirect(w, r, "/app/artifacts", http.StatusFound)
}
