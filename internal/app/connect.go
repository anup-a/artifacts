package app

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/config"
	"github.com/anup-a/artifacts/internal/connect"
	"github.com/anup-a/artifacts/internal/store"
)

// connectCookie carries a CLI's approval link through sign-in, the way
// claimCookie carries a claim link.
const connectCookie = "byagent_connect"

type connectData struct {
	// Prompt is the text to paste into an agent, holding a fresh Code.
	Prompt, Code string
	ExpiresAt    time.Time
	// Claimed is set right after guest pages were kept: the agent that made
	// them lost its guest key and needs connecting.
	Claimed bool
	// Pending is a CLI-started request waiting for this viewer's approval.
	Pending *pendingConnect
	// Approved names the key a just-approved request became.
	Approved string
}

type pendingConnect struct {
	Code, Name string
	ExpiresAt  time.Time
}

// connectPage shows the Connect agent page, or, with ?code=, the approval
// for a CLI that is waiting. A signed-out visitor with a code is sent to sign
// in and brought back.
func (h *Handler) connectPage(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	v, err := h.load(r.Context(), w, r)
	if errors.Is(err, auth.ErrNoSession) {
		if connect.Normalize(code) != "" {
			http.SetCookie(w, &http.Cookie{Name: connectCookie, Value: connect.Normalize(code), Path: "/app", MaxAge: int(connect.TTL / time.Second),
				HttpOnly: true, Secure: auth.SecureOrigin(h.Cfg.APIOrigin), SameSite: http.SameSiteLaxMode})
		}
		http.Redirect(w, r, "/app/login", http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if code == "" {
		h.render(w, r, 200, "connect", "Connect an agent", v, connectData{Claimed: r.URL.Query().Get("claimed") == "1"})
		return
	}
	c, msg := h.pendingByCode(r, code)
	if msg != "" {
		h.renderError(w, r, http.StatusGone, msg)
		return
	}
	d := connectData{Pending: &pendingConnect{Code: displayCode(code), Name: c.Name, ExpiresAt: c.ExpiresAt}}
	h.render(w, r, 200, "connect", "Approve an agent", v, d)
}

// pendingByCode finds a CLI-started request that still waits for approval.
func (h *Handler) pendingByCode(r *http.Request, code string) (store.ConnectCode, string) {
	const gone = "This connect code has expired or was already used. Run byagent login --browser again for a new one."
	hash := connect.Hash(code)
	if hash == "" {
		return store.ConnectCode{}, "This connect code is not valid."
	}
	c, err := h.Store.ConnectByCode(r.Context(), hash, h.Now())
	if err != nil || c.DeviceSHA256 == "" || c.ApprovedAt != nil {
		return c, gone
	}
	return c, ""
}

// createConnect makes a one-time code for this workspace and shows the
// prompt that carries it. The code is approved as it is made: the person
// asking is signed in.
func (h *Handler) createConnect(w http.ResponseWriter, r *http.Request, v *viewer) {
	if ok, _ := h.KeyLimiter.Allow(v.Tenant.ID); !ok {
		h.renderError(w, r, 429, "Too many keys and connect codes made recently. Try again in an hour.")
		return
	}
	code := connect.NewCode()
	now := h.Now()
	c := store.ConnectCode{CodeSHA256: connect.Hash(code), TenantID: v.Tenant.ID, Email: v.User.Email,
		CreatedAt: now, ExpiresAt: now.Add(connect.TTL), ApprovedAt: &now}
	if err := h.Store.CreateConnect(r.Context(), c); err != nil {
		h.fail(w, r, err)
		return
	}
	d := connectData{Code: code, ExpiresAt: c.ExpiresAt, Prompt: connectPrompt(h.Cfg, v.Tenant.Name, code),
		Claimed: r.PostForm.Get("claimed") == "1"}
	h.render(w, r, 200, "connect", "Connect an agent", v, d)
}

// approveConnect binds a waiting CLI request to this workspace. The CLI,
// polling, then gets its key.
func (h *Handler) approveConnect(w http.ResponseWriter, r *http.Request, v *viewer) {
	code := r.PostForm.Get("code")
	c, msg := h.pendingByCode(r, code)
	if msg != "" {
		h.renderError(w, r, http.StatusGone, msg)
		return
	}
	err := h.Store.ApproveConnect(r.Context(), c.CodeSHA256, v.Tenant.ID, v.User.Email, h.Now())
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, r, http.StatusGone, "This connect code has expired or was already used. Run byagent login --browser again for a new one.")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "connect", "Agent connected", v, connectData{Approved: c.Name})
}

// connectPrompt is what a person pastes into their agent. It names the
// one-time code, never a key, and points at the skill for everything after.
func connectPrompt(cfg config.Config, workspace, code string) string {
	login := "npx -y byagent@latest login --code " + code
	if cfg.APIOrigin != defaultAPI {
		login += " --api " + cfg.APIOrigin
	}
	return "Connect to my byagent workspace \"" + workspace + "\" so you can publish pages I can comment on.\n\n" +
		"1. Run: " + login + "\n" +
		"   The code works once and expires in 10 minutes. It saves a key named after you, so never ask me for a key or print one.\n" +
		"2. Read " + cfg.ShareOrigin + "/skill.md and follow it from now on."
}

// defaultAPI is the origin the CLI uses when given none.
const defaultAPI = "https://app.byagent.dev"

func displayCode(raw string) string {
	n := connect.Normalize(raw)
	if n == "" {
		return raw
	}
	return n[:4] + "-" + n[4:]
}

// resumeConnect sends someone who just signed in back to the approval link
// they opened signed out. It reports whether it redirected.
func resumeConnect(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(connectCookie)
	if err != nil || connect.Normalize(c.Value) == "" {
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: connectCookie, Value: "", Path: "/app", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/app/connect?code="+url.QueryEscape(displayCode(c.Value)), http.StatusFound)
	return true
}
