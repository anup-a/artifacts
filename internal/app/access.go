package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/anup-a/byagent/internal/serve"
	"github.com/anup-a/byagent/internal/store"
)

// accessForm is the Access card's state.
type accessForm struct {
	// Mode is anyone_with_link, password or restricted, as the API names it.
	Mode string
	// Workspace is a private page with no door: restricted to nobody.
	Workspace   bool
	PasswordSet bool
	// Allow is the allowlist one entry a line, domains as "@acme.com".
	Allow          string
	Emails, Domain int
	// Mail is whether this server can send restricted visitors their code.
	Mail bool
}

func (h *Handler) accessFormOf(a store.Artifact) accessForm {
	emails, domains := serve.SplitAllow(a.AccessAllow)
	return accessForm{
		Mode:        serve.ModeOf(a),
		Workspace:   a.Visibility == "private" && a.AccessMode == "" && a.ShareCode == "",
		PasswordSet: a.AccessMode == serve.AccessPassword,
		Allow:       strings.Join(a.AccessAllow, "\n"),
		Emails:      len(emails), Domain: len(domains),
		Mail: h.Cfg.EmailProvider != "" && h.Cfg.KeySecret != "",
	}
}

// splitAllowText reads the allowlist box: one entry a line (commas and spaces
// also separate). "@acme.com" and "acme.com" are domains, anything else with
// an @ an address.
func splitAllowText(text string) (emails, domains []string) {
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t' || r == ';' }) {
		if strings.HasPrefix(f, "@") || !strings.Contains(f, "@") {
			domains = append(domains, f)
		} else {
			emails = append(emails, f)
		}
	}
	return emails, domains
}

// setAccess is the Access card's save: the same three modes as
// PATCH /api/artifacts/{id}/access, with the same rules.
func (h *Handler) setAccess(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, tr(r, "That artifact does not exist."))
		return
	}
	fail := func(msg string) {
		h.renderArtifactWith(w, r, 400, v, a.ID, artifactOpts{AccessError: msg})
	}
	// Saving may hash a password, which is deliberately expensive.
	if ok, _ := h.AccessLimiter.Allow(v.Session.UserID); !ok {
		h.renderArtifactWith(w, r, 429, v, a.ID, artifactOpts{AccessError: tr(r, "Too many changes in a short time. Wait a minute, then try again.")})
		return
	}
	ctx := r.Context()
	switch r.PostForm.Get("mode") {
	case "anyone_with_link":
		if err := h.Store.SetShareCode(ctx, a.ID, "", nil); err != nil {
			h.fail(w, r, err)
			return
		}
		if err := h.Store.SetVisibility(ctx, a.ID, "public"); err != nil {
			h.fail(w, r, err)
			return
		}
	case "password":
		if h.Cfg.KeySecret == "" {
			fail(tr(r, "Password pages need %s set on the server.", "KEY_SIGNING_SECRET"))
			return
		}
		pw := r.PostForm.Get("password")
		if pw == "" && a.AccessMode == serve.AccessPassword {
			break // keep the current password
		}
		if pw == "" {
			fail(tr(r, "Enter a password."))
			return
		}
		if err := serve.ValidPassword(pw); err != nil {
			fail("The " + err.Error() + ".")
			return
		}
		hash, err := serve.HashPassword(ctx, pw)
		if errors.Is(err, serve.ErrBusy) {
			h.renderArtifactWith(w, r, 503, v, a.ID, artifactOpts{AccessError: tr(r, "Too many passwords are being set right now. Try again in a moment.")})
			return
		}
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if err := h.Store.SetAccess(ctx, a.ID, store.Access{Mode: serve.AccessPassword, PasswordHash: hash}); err != nil {
			h.fail(w, r, err)
			return
		}
	case "restricted":
		allow, err := serve.NormalizeAllow(splitAllowText(r.PostForm.Get("allow")))
		if err != nil {
			fail(strings.ToUpper(err.Error()[:1]) + err.Error()[1:] + ".")
			return
		}
		if len(allow) == 0 {
			if err := h.Store.SetAccess(ctx, a.ID, store.Access{}); err != nil {
				h.fail(w, r, err)
				return
			}
			if err := h.Store.SetVisibility(ctx, a.ID, "private"); err != nil {
				h.fail(w, r, err)
				return
			}
			if err := h.Store.SetShareCode(ctx, a.ID, "", nil); err != nil {
				h.fail(w, r, err)
				return
			}
			break
		}
		if !h.accessFormOf(a).Mail {
			fail(tr(r, "This server cannot send email, so the people on the list could not get their code."))
			return
		}
		if err := h.Store.SetAccess(ctx, a.ID, store.Access{Mode: serve.AccessRestricted, Allow: allow}); err != nil {
			h.fail(w, r, err)
			return
		}
	default:
		fail(tr(r, "Choose who can open this page."))
		return
	}
	h.backToArtifact(w, r, v, a.ID, "#access")
}
