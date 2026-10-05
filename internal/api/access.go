package api

import (
	"net/http"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

// Page access, here.now style: anyone_with_link, password, or restricted to
// an allowlist of addresses and domains whose visitors prove their address by
// a mailed code. Visitors never need an account.

// invitesPerRequest caps one invite call; invitesPerDay (Deps.InviteLimiter)
// caps a workspace, so an allowlist cannot become a mailing list.
const invitesPerRequest = 50

// accessView is the access policy as GET and PATCH return it. The password
// itself is never stored; only whether one is set is reported.
func (s *server) accessView(a store.Artifact) map[string]any {
	emails, domains := serve.SplitAllow(a.AccessAllow)
	out := map[string]any{
		"id": a.ID, "mode": serve.ModeOf(a), "visibility": a.Visibility,
		"password_set":   a.AccessMode == serve.AccessPassword,
		"allowed_emails": emails, "allowed_domains": domains,
		"access_version": a.AccessVersion, "url": s.artifactViewURL(a), "share_url": nil,
	}
	switch {
	case a.Visibility != "private" || a.AccessMode != "":
		out["share_url"] = s.artifactURL(a.ID)
	case a.ShareCode != "":
		// The code is a credential: like getArtifact, only this single read
		// by id carries it.
		out["share_url"] = serve.ShareURL(s.Cfg.ShareOrigin, a.ID, a.ShareCode)
		out["share_code"] = a.ShareCode
		out["share_code_expires_at"] = a.ShareCodeExpiresAt
	}
	return out
}

func (s *server) getAccess(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, s.accessView(a))
}

// patchAccess answers PATCH /api/artifacts/{id}/access. The allowlists are
// replaced whole, so adding one entry is read, merge, write. Every mode
// clears what the others set: one door at a time.
func (s *server) patchAccess(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if _, ok := guestFrom(r.Context()); ok {
		apierr.Write(w, r, apierr.Forbidden("guest pages are public; claim them to add a password or an allowlist"))
		return
	}
	var body struct {
		Mode           string   `json:"mode"`
		Password       *string  `json:"password"`
		AllowedEmails  []string `json:"allowed_emails"`
		AllowedDomains []string `json:"allowed_domains"`
	}
	if e := decodeJSON(w, r, 256<<10, &body); e != nil {
		apierr.Write(w, r, e)
		return
	}
	lists := len(body.AllowedEmails)+len(body.AllowedDomains) > 0
	ctx := r.Context()
	switch body.Mode {
	case "anyone_with_link":
		if body.Password != nil || lists {
			apierr.Write(w, r, apierr.Invalid("anyone_with_link takes no password or allowlist"))
			return
		}
		// Withdraw the code first and open the page last, so no step leaves
		// it more open than either end.
		if err := s.Store.SetShareCode(ctx, a.ID, "", nil); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		if err := s.Store.SetVisibility(ctx, a.ID, "public"); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	case "password":
		if lists {
			apierr.Write(w, r, apierr.Invalid("a password page has no allowlist; use restricted for that"))
			return
		}
		if s.Cfg.KeySecret == "" {
			apierr.Write(w, r, apierr.Unavailable("password pages need KEY_SIGNING_SECRET set on the server"))
			return
		}
		if body.Password == nil {
			if a.AccessMode == serve.AccessPassword {
				break // already a password page; nothing to change
			}
			apierr.Write(w, r, apierr.Invalid("password is required"))
			return
		}
		if err := serve.ValidPassword(*body.Password); err != nil {
			apierr.Write(w, r, apierr.Invalid(err.Error()))
			return
		}
		hash, err := serve.HashPassword(*body.Password)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		if err := s.Store.SetAccess(ctx, a.ID, store.Access{Mode: serve.AccessPassword, PasswordHash: hash}); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	case "restricted":
		if body.Password != nil {
			apierr.Write(w, r, apierr.Invalid("a restricted page has no password; use password mode for that"))
			return
		}
		allow, err := serve.NormalizeAllow(body.AllowedEmails, body.AllowedDomains)
		if err != nil {
			apierr.Write(w, r, apierr.Invalid(err.Error()))
			return
		}
		if len(allow) == 0 {
			// Nobody on the list: only the workspace, which is a private page
			// with no door at all.
			if err := s.Store.SetAccess(ctx, a.ID, store.Access{}); err != nil {
				apierr.Write(w, r, apierr.Internal(err))
				return
			}
			if err := s.Store.SetVisibility(ctx, a.ID, "private"); err != nil {
				apierr.Write(w, r, apierr.Internal(err))
				return
			}
			if err := s.Store.SetShareCode(ctx, a.ID, "", nil); err != nil {
				apierr.Write(w, r, apierr.Internal(err))
				return
			}
			break
		}
		if s.Cfg.KeySecret == "" {
			apierr.Write(w, r, apierr.Unavailable("restricted pages need KEY_SIGNING_SECRET set on the server"))
			return
		}
		if s.Mail == nil {
			apierr.Write(w, r, apierr.Unavailable("this server sends no email, so visitors could not get their code; set EMAIL_PROVIDER"))
			return
		}
		if err := s.Store.SetAccess(ctx, a.ID, store.Access{Mode: serve.AccessRestricted, Allow: allow}); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	default:
		apierr.Write(w, r, apierr.Invalid("mode must be anyone_with_link, password or restricted"))
		return
	}
	a, err := s.Store.Artifact(ctx, a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, s.accessView(a))
}

type inviteFailure struct {
	Email string `json:"email"`
	Error string `json:"error"`
}

type inviteSkip struct {
	Email  string `json:"email"`
	Reason string `json:"reason"`
}

// sendInvites answers POST /api/artifacts/{id}/access/invites by mailing each
// listed address a pointer to the page. Only addresses on the allowlist by
// name are mailed: a domain entry admits its people but never mails them, so
// "@gmail.com" cannot be turned into a way to mail strangers. The mail says
// nothing the page owner wrote, only where the page is and how to open it.
func (s *server) sendInvites(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if _, ok := guestFrom(r.Context()); ok {
		apierr.Write(w, r, apierr.Forbidden("guest pages cannot send invites"))
		return
	}
	if a.AccessMode != serve.AccessRestricted {
		apierr.Write(w, r, apierr.Invalid("invites are for restricted pages; set the page to restricted first"))
		return
	}
	if s.Mail == nil {
		apierr.Write(w, r, apierr.Unavailable("this server sends no email; set EMAIL_PROVIDER"))
		return
	}
	var body struct {
		Emails []string `json:"emails"`
	}
	if e := decodeJSON(w, r, 64<<10, &body); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if len(body.Emails) == 0 || len(body.Emails) > invitesPerRequest {
		apierr.Write(w, r, apierr.Invalid("emails must list 1 to 50 addresses"))
		return
	}
	k, _ := KeyFrom(r.Context())
	sent, failed, skipped := []string{}, []inviteFailure{}, []inviteSkip{}
	seen := map[string]bool{}
	for _, raw := range body.Emails {
		email, err := auth.NormalizeEmail(raw)
		if err != nil {
			skipped = append(skipped, inviteSkip{Email: strings.TrimSpace(raw), Reason: "invalid"})
			continue
		}
		if seen[email] {
			continue
		}
		seen[email] = true
		if !listedByName(a.AccessAllow, email) {
			skipped = append(skipped, inviteSkip{Email: email, Reason: "not_allowed"})
			continue
		}
		if ok, _ := s.InviteLimiter.Allow(k.TenantID); !ok {
			failed = append(failed, inviteFailure{Email: email, Error: "rate_limited"})
			continue
		}
		if err := s.Mail.SendMail(r.Context(), email, "A page was shared with you on byagent", inviteText(s.artifactURL(a.ID))); err != nil {
			failed = append(failed, inviteFailure{Email: email, Error: "send_failed"})
			continue
		}
		sent = append(sent, email)
	}
	apierr.WriteJSON(w, 200, map[string]any{"notifications": map[string]any{"sent": sent, "failed": failed, "skipped": skipped}})
}

func listedByName(allow []string, email string) bool {
	for _, e := range allow {
		if e == email {
			return true
		}
	}
	return false
}

func inviteText(url string) string {
	return "A page on byagent was shared with this email address.\n\n" + url + "\n\n" +
		"Open it and enter this address. We will email you a one-time code that opens it. You do not need an account.\n\n" +
		"If you were not expecting this, you can ignore this email.\n"
}
