package app

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/anup-a/byagent/internal/auth"
	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/connect"
	"github.com/anup-a/byagent/internal/store"
)

// connectCookie carries a CLI's approval link through sign-in, the way
// claimCookie carries a claim link.
const connectCookie = "byagent_connect"

type connectData struct {
	// Prompt is a fresh connect prompt; nil when none could be made.
	Prompt *promptView
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
			h.setAppCookie(w, connectCookie, connect.Normalize(code), int(connect.TTL/time.Second))
		}
		http.Redirect(w, r, "/app/login", http.StatusFound)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if code == "" {
		h.render(w, r, 200, "connect", tr(r, "Connect an agent"), v, connectData{Prompt: h.mintPrompt(r, v), Claimed: r.URL.Query().Get("claimed") == "1"})
		return
	}
	c, msg := h.pendingByCode(r, code)
	if msg != "" {
		h.renderError(w, r, http.StatusGone, msg)
		return
	}
	d := connectData{Pending: &pendingConnect{Code: displayCode(code), Name: c.Name, ExpiresAt: c.ExpiresAt}}
	h.render(w, r, 200, "connect", tr(r, "Approve an agent"), v, d)
}

// pendingByCode finds a CLI-started request that still waits for approval.
func (h *Handler) pendingByCode(r *http.Request, code string) (store.ConnectCode, string) {
	gone := tr(r, "This connect code has expired or was already used. Run %s again for a new one.", "byagent login --browser")
	hash := connect.Hash(code)
	if hash == "" {
		return store.ConnectCode{}, tr(r, "This connect code is not valid.")
	}
	c, err := h.Store.ConnectByCode(r.Context(), hash, h.Now())
	if err != nil || c.DeviceSHA256 == "" || c.ApprovedAt != nil {
		return c, gone
	}
	return c, ""
}

// promptView is a ready-to-paste connect prompt and the code inside it.
type promptView struct {
	Text, Code string
	ExpiresAt  time.Time
}

// mintPrompt makes a one-time code for this workspace, approved as it is
// made (the person asking is signed in), and the prompt that carries it.
// Pages that show a prompt mint one on every view: codes are hashes that
// expire in ten minutes, so an unused one costs a row until the next GC.
// It returns nil when the workspace has minted too many recently.
func (h *Handler) mintPrompt(r *http.Request, v *viewer) *promptView {
	if ok, _ := h.ConnectLimiter.Allow(v.Tenant.ID); !ok {
		return nil
	}
	code := connect.NewCode()
	now := h.Now()
	c := store.ConnectCode{CodeSHA256: connect.Hash(code), TenantID: v.Tenant.ID, Email: v.User.Email,
		CreatedAt: now, ExpiresAt: now.Add(connect.TTL), ApprovedAt: &now}
	if err := h.Store.CreateConnect(r.Context(), c); err != nil {
		slog.Error("connect code", "err", err)
		return nil
	}
	return &promptView{Text: connectPrompt(h.Cfg, code), Code: code, ExpiresAt: c.ExpiresAt}
}

// createConnect answers the old "Create connect prompt" form; the page now
// shows a prompt straight away, so this just shows it again.
func (h *Handler) createConnect(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.render(w, r, 200, "connect", tr(r, "Connect an agent"), v, connectData{Prompt: h.mintPrompt(r, v), Claimed: r.PostForm.Get("claimed") == "1"})
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
		h.renderError(w, r, http.StatusGone, tr(r, "This connect code has expired or was already used. Run %s again for a new one.", "byagent login --browser"))
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "connect", tr(r, "Agent connected"), v, connectData{Approved: c.Name})
}

// connectPrompt is what a person pastes into their agent. It names the
// one-time code, never a key, and points at the skill for everything after.
func connectPrompt(cfg config.Config, code string) string {
	login := "npx -y byagent@latest login --code " + code
	if cfg.APIOrigin != defaultAPI {
		login += " --api " + cfg.APIOrigin
	}
	// Step 3 makes it outlast this chat: init writes a line about byagent
	// into the instructions file every later session reads.
	return "Set up byagent so you can publish pages I can comment on.\n\n" +
		"1. Run: " + login + "\n" +
		"2. Read " + cfg.ShareOrigin + "/skill.md and follow it.\n" +
		"3. Run: npx -y byagent@latest init --global\n\n" +
		"The code works once in the next 10 minutes. Never ask me for a key."
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
func (h *Handler) resumeConnect(w http.ResponseWriter, r *http.Request) bool {
	v := h.appCookie(r, connectCookie)
	if connect.Normalize(v) == "" {
		return false
	}
	h.setAppCookie(w, connectCookie, "", -1)
	http.Redirect(w, r, "/app/connect?code="+url.QueryEscape(displayCode(v)), http.StatusFound)
	return true
}
