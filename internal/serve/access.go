package serve

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/anup-a/byagent/internal/auth"
	"github.com/anup-a/byagent/internal/i18n"
	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/ratelimit"
	"github.com/anup-a/byagent/internal/store"
)

// Page access beyond the share code: a password, or an allowlist checked by
// email. Both open the same way a code does: the visitor proves something once
// at the gate, gets a signed cookie scoped to this one page, and from then on
// reads it and comments on it like any code holder. Nothing here knows the
// host it is served on: cookies carry no Domain and every redirect and form
// action is a path, so the same gate works behind any hostname.

// Access modes as stored. "" means visibility and the share code decide.
const (
	AccessPassword   = "password"
	AccessRestricted = "restricted"
)

const (
	// accessCodeTTL is how long a mailed code and link stay good.
	accessCodeTTL = 15 * time.Minute
	// accessCodeAttempts is how many wrong codes burn a mailed code. Six digits
	// with five guesses is a one in two hundred thousand chance per mail, and
	// mails are metered below.
	accessCodeAttempts = 5
	// Password guesses are metered per page and client address first, then
	// per page overall as a backstop. One address runs out of its own budget
	// long before it could use up the page's, so it cannot lock everyone out
	// on its own; it takes three addresses guessing flat out.
	passwordPerClient = 10
	passwordPerPage   = 30
	passwordWindow    = 15 * time.Minute
	// Codes mailed: per page and address, per client, per page. The per-page
	// meter only counts mail that is actually sent.
	mailPerAddress = 3
	mailPerClient  = 10
	mailPerPage    = 50
	mailWindow     = time.Hour
	// Defaults for the daily caps on gate mail, per workspace and for the
	// whole server, when the handler is not given its own.
	mailPerWorkspaceDay = 200
	mailPerServerDay    = 2000
	// Code guesses per page and client, on top of the per-code limit.
	codePerClient = 20
)

// Limits on what an owner may set.
const (
	PasswordMinLen = 8
	PasswordMaxLen = 128
	// AllowMax caps the allowlist: it rides on every read of the artifact.
	AllowMax = 500
)

// accessGuards are the meters on the access gate. NewHandler-style defaults
// are built in Register.
type accessGuards struct {
	passwordClient, passwordPage *ratelimit.Limiter
	mailAddress, mailClient      *ratelimit.Limiter
	mailPage, codeClient         *ratelimit.Limiter
	mailWorkspace, mailServer    *ratelimit.Limiter
}

func newAccessGuards(now func() time.Time, perWorkspaceDay, perServerDay int) *accessGuards {
	if perWorkspaceDay <= 0 {
		perWorkspaceDay = mailPerWorkspaceDay
	}
	if perServerDay <= 0 {
		perServerDay = mailPerServerDay
	}
	g := &accessGuards{
		passwordClient: ratelimit.NewWindow(passwordPerClient, passwordWindow),
		passwordPage:   ratelimit.NewWindow(passwordPerPage, passwordWindow),
		mailAddress:    ratelimit.NewWindow(mailPerAddress, mailWindow),
		mailClient:     ratelimit.NewWindow(mailPerClient, mailWindow),
		mailPage:       ratelimit.NewWindow(mailPerPage, mailWindow),
		codeClient:     ratelimit.NewWindow(codePerClient, passwordWindow),
		mailWorkspace:  ratelimit.NewWindow(perWorkspaceDay, 24*time.Hour),
		mailServer:     ratelimit.NewWindow(perServerDay, 24*time.Hour),
	}
	if now != nil {
		for _, l := range []*ratelimit.Limiter{g.passwordClient, g.passwordPage, g.mailAddress, g.mailClient, g.mailPage, g.codeClient, g.mailWorkspace, g.mailServer} {
			l.SetNow(now)
		}
	}
	return g
}

// ---- Passwords ----

// PasswordCost is the argon2id work new hashes use: the OWASP baseline of
// two passes over 19 MiB. Checking always uses the cost stored in the hash,
// so raising it later leaves existing passwords working. Tests lower it.
var PasswordCost = struct{ Time, MemoryKiB uint32 }{2, 19 * 1024}

const (
	argonThreads = 1
	argonKeyLen  = 32
)

// hashSlots bounds how many argon2 hashes run at once, so a burst of guesses
// against many pages cannot turn into a burst of memory. A caller waits at
// most hashWait for one, and not past its request, then gets ErrBusy.
var hashSlots = make(chan struct{}, 4)

const hashWait = 2 * time.Second

// ErrBusy means every password slot stayed taken: answer 503, try later.
var ErrBusy = errors.New("too many password checks at once; try again in a moment")

func takeHashSlot(ctx context.Context) error {
	t := time.NewTimer(hashWait)
	defer t.Stop()
	select {
	case hashSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrBusy
	case <-t.C:
		return ErrBusy
	}
}

// ValidPassword checks an owner's new password.
func ValidPassword(pw string) error {
	if !utf8.ValidString(pw) {
		return errors.New("password must be valid UTF-8")
	}
	n := utf8.RuneCountInString(pw)
	if n < PasswordMinLen || n > PasswordMaxLen {
		return fmt.Errorf("password must be %d to %d characters", PasswordMinLen, PasswordMaxLen)
	}
	for _, r := range pw {
		if unicode.IsControl(r) {
			return errors.New("password must not contain control characters or line breaks")
		}
	}
	return nil
}

// HashPassword returns the argon2id hash of pw in the PHC string format, or
// ErrBusy when no slot frees up in time.
func HashPassword(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	cost := PasswordCost
	if err := takeHashSlot(ctx); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, cost.Time, cost.MemoryKiB, argonThreads, argonKeyLen)
	<-hashSlots
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, cost.MemoryKiB, cost.Time, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// passwordMatches checks pw against a stored hash in constant time. Anything
// it cannot parse, or with parameters past what HashPassword writes, fails.
// The error is ErrBusy when no slot frees up in time.
func passwordMatches(ctx context.Context, stored, pw string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false, nil
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || m > 64*1024 || t == 0 || t > 10 || p == 0 || p > 4 {
		return false, nil
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 64 {
		return false, nil
	}
	if err := takeHashSlot(ctx); err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-hashSlots
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ---- Allowlists ----

// NormalizeAllow turns the owner's emails and domains into the stored list:
// lowercase addresses and "@domain" entries, deduplicated and sorted. A
// domain may be given as "acme.com" or "@acme.com"; it admits addresses at
// exactly that domain, not its subdomains.
func NormalizeAllow(emails, domains []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range emails {
		e, err := auth.NormalizeEmail(raw)
		if err != nil {
			return nil, fmt.Errorf("%q is not an email address", raw)
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, raw := range domains {
		d, err := normalizeDomain(raw)
		if err != nil {
			return nil, err
		}
		if !seen["@"+d] {
			seen["@"+d] = true
			out = append(out, "@"+d)
		}
	}
	if len(out) > AllowMax {
		return nil, fmt.Errorf("an allowlist holds at most %d entries", AllowMax)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeDomain(raw string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimPrefix(d, "@")
	bad := fmt.Errorf("%q is not an email domain", raw)
	if d == "" || len(d) > 253 || !strings.Contains(d, ".") || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") || strings.Contains(d, "..") {
		return "", bad
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", bad
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", bad
			}
		}
	}
	return d, nil
}

// SplitAllow separates a stored allowlist into addresses and bare domains.
func SplitAllow(allow []string) (emails, domains []string) {
	emails, domains = []string{}, []string{}
	for _, e := range allow {
		if d, ok := strings.CutPrefix(e, "@"); ok {
			domains = append(domains, d)
		} else {
			emails = append(emails, e)
		}
	}
	return emails, domains
}

// AllowedEmail reports whether a normalized address is on the list, by
// itself or by its exact domain.
func AllowedEmail(allow []string, email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at < 1 {
		return false
	}
	domain := email[at:]
	for _, e := range allow {
		if e == email || e == domain {
			return true
		}
	}
	return false
}

// ---- Grants ----

// accessCookie is the kind of cookie a password or email grant is kept in
// (see cookie.go).
const accessCookie = "artifact_access"

// accessCookieName is the share origin's name for it, per page.
func accessCookieName(id string) string { return accessCookie + "_" + id }

// accessMAC signs one grant: the page, its door, the door's version and
// password hash, who (for restricted) and until when. Changing any of them,
// rotating the password included, turns every outstanding cookie into noise.
func accessMAC(secret string, a store.Artifact, email string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "access|%s|%s|%d|%s|%s|%d", a.ID, a.AccessMode, a.AccessVersion, a.AccessPassword, email, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func accessValue(secret string, a store.Artifact, email string, exp int64) string {
	return strconv.FormatInt(exp, 10) + "." + base64.RawURLEncoding.EncodeToString([]byte(email)) + "." + accessMAC(secret, a, email, exp)
}

// accessGrant reads a valid grant for a password or restricted page. For a
// restricted page it returns the verified address, which must still be on the
// allowlist: taking someone off the list takes them off the page.
func accessGrant(r *http.Request, a store.Artifact, secret string, now time.Time) (string, bool) {
	if secret == "" || (a.AccessMode != AccessPassword && a.AccessMode != AccessRestricted) {
		return "", false
	}
	v := pageCookie(r, accessCookie, a.ID)
	if v == "" {
		return "", false
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() >= exp {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	email := string(raw)
	if !hmac.Equal([]byte(parts[2]), []byte(accessMAC(secret, a, email, exp))) {
		return "", false
	}
	if a.AccessMode == AccessRestricted && !AllowedEmail(a.AccessAllow, email) {
		return "", false
	}
	if a.AccessMode == AccessPassword && email != "" {
		return "", false
	}
	return email, true
}

// VisitorEmail is the verified address of a restricted page's visitor, or ""
// for anyone else. The comment routes sign comments with it.
func VisitorEmail(r *http.Request, a store.Artifact, secret string) string {
	if a.AccessMode != AccessRestricted {
		return ""
	}
	email, _ := accessGrant(r, a, secret, time.Now())
	return email
}

// setAccessCookie keeps a password or restricted grant for this page only,
// through the page-cookie helpers (cookie.go): host-only on the page's own
// host, path-scoped on the share origin.
func (h *Handler) setAccessCookie(w http.ResponseWriter, r *http.Request, a store.Artifact, email string) {
	exp := h.now().Add(unlockTTL).Unix()
	h.setPageCookie(w, r, accessCookie, a.ID, accessValue(h.UnlockSecret, a, email, exp), int(unlockTTL.Seconds()))
}

func (h *Handler) clearAccessCookie(w http.ResponseWriter, r *http.Request, id string) {
	h.clearPageCookie(w, r, accessCookie, id)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().UTC()
}

// ModeOf names an artifact's access the way the API does. anyone_with_link
// covers a public page and a private one shared by code, since the code rides
// on the link; restricted with an empty allowlist is a private page only its
// workspace can open.
func ModeOf(a store.Artifact) string {
	switch {
	case a.AccessMode == AccessPassword:
		return "password"
	case a.AccessMode == AccessRestricted:
		return "restricted"
	case a.Visibility != "private" || a.ShareCode != "":
		return "anyone_with_link"
	}
	return "restricted"
}

// gateKind is what the gate asks a visitor of this artifact for.
func gateKind(a store.Artifact) string {
	switch a.AccessMode {
	case AccessPassword:
		return "password"
	case AccessRestricted:
		return "email"
	}
	return "code"
}

// hasDoor reports whether anyone besides the workspace may get in.
func hasDoor(a store.Artifact) bool {
	return a.ShareCode != "" || a.AccessMode == AccessPassword || a.AccessMode == AccessRestricted
}

func (h *Handler) clientIP(r *http.Request) string {
	if h.TrustProxy {
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

// crossSite reports a browser form post from another site. The gate's forms
// spend guesses and send mail, so another page must not be able to submit
// them on a visitor's behalf. Requests without an Origin (non-browsers, old
// browsers) fall through to the meters.
//
// Sec-Fetch-Site is asked first: the gate is served with no-referrer, under
// which browsers send "Origin: null" even for the gate's own forms, so the
// Origin header alone would refuse the people the forms are for.
func crossSite(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "cross-site"
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	return err != nil || !strings.EqualFold(u.Host, r.Host)
}

// ---- Password gate ----

// tryPassword checks a password posted to the gate.
func (h *Handler) tryPassword(w http.ResponseWriter, r *http.Request, a store.Artifact, given string) {
	g := GateView{ArtifactID: a.ID, Kind: "password"}
	// The client's own budget first: a client that has run out stops here
	// and never touches the page-wide budget everyone else shares.
	if ok, _ := h.access.passwordClient.Allow(a.ID + "|" + h.clientIP(r)); !ok {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	if ok, _ := h.access.passwordPage.Allow(a.ID); !ok {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	if given == "" || len(given) > 4*PasswordMaxLen || h.UnlockSecret == "" {
		g.Wrong = true
		h.renderGate(w, r, g)
		return
	}
	match, err := passwordMatches(r.Context(), a.AccessPassword, given)
	if err != nil {
		g.Busy = true
		h.renderGate(w, r, g)
		return
	}
	if !match {
		g.Wrong = true
		h.renderGate(w, r, g)
		return
	}
	h.setAccessCookie(w, r, a, "")
	http.Redirect(w, r, PageBase(r, a.ID), http.StatusSeeOther)
}

// ---- Restricted gate ----

func accessCodeHash(secret, id, email, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "access-code|%s|%s|%s", id, email, normalizeCode(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// restrictedArtifact loads the artifact behind a restricted gate form, or
// answers the request itself.
func (h *Handler) restrictedArtifact(w http.ResponseWriter, r *http.Request) (store.Artifact, bool) {
	a, err := h.Store.Artifact(r.Context(), r.PathValue("id"))
	if err != nil || a.DeletedAt != nil || a.AccessMode != AccessRestricted || h.Gate == nil {
		h.refuse(w, r)
		return a, false
	}
	if crossSite(r) {
		h.refuse(w, r)
		return a, false
	}
	if err := r.ParseForm(); err != nil {
		h.renderGate(w, r, GateView{ArtifactID: a.ID, Kind: "email", Wrong: true})
		return a, false
	}
	return a, true
}

// requestAccessCode mails a one-time code and link to an address on the
// allowlist. The answer is the same whether or not the address is on it, so
// the gate cannot be used to read the list.
func (h *Handler) requestAccessCode(w http.ResponseWriter, r *http.Request) {
	a, ok := h.restrictedArtifact(w, r)
	if !ok {
		return
	}
	g := GateView{ArtifactID: a.ID, Kind: "email"}
	if h.Mail == nil || h.UnlockSecret == "" {
		g.NoMail = true
		h.renderGate(w, r, g)
		return
	}
	email, err := auth.NormalizeEmail(r.PostFormValue("email"))
	if err != nil {
		g.Wrong = true
		h.renderGate(w, r, g)
		return
	}
	// The client's budget first, then the address's. Both are charged the
	// same whether or not the address is listed, so being refused here says
	// nothing about the list.
	if ok, _ := h.access.mailClient.Allow(h.clientIP(r)); !ok {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	if ok, _ := h.access.mailAddress.Allow(a.ID + "|" + email); !ok {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	// Everything that depends on the list, the page's and workspace's mail
	// budgets included, happens after the answer and off the request: the
	// answer and its timing are the same for every address.
	lang := i18n.Lang(r)
	h.later(func() {
		if err := h.mailAccessCode(context.Background(), a, email, lang); err != nil {
			slog.Error("access code", "artifact", a.ID, "err", err)
		}
	})
	g.Email, g.Sent = email, true
	h.renderGate(w, r, g)
}

// later runs fn after the request, or in it under tests.
func (h *Handler) later(fn func()) {
	if h.mailSync {
		fn()
		return
	}
	go fn()
}

// mailAccessCode mails a code to a listed address, if the page, workspace and
// server budgets still allow it. An unlisted or over-budget address is
// dropped silently: the visitor was already told the same thing either way.
func (h *Handler) mailAccessCode(ctx context.Context, a store.Artifact, email, lang string) error {
	if !AllowedEmail(a.AccessAllow, email) {
		return nil
	}
	if ok, _ := h.access.mailPage.Allow(a.ID); !ok {
		slog.Warn("access mail: page budget spent", "artifact", a.ID)
		return nil
	}
	if ok, _ := h.access.mailWorkspace.Allow(a.TenantID); !ok {
		slog.Warn("access mail: workspace daily cap reached", "artifact", a.ID, "tenant", a.TenantID)
		return nil
	}
	if ok, _ := h.access.mailServer.Allow("all"); !ok {
		slog.Warn("access mail: server daily cap reached", "artifact", a.ID)
		return nil
	}
	token, code := ids.NewToken(), ids.NewShareCode()
	now := h.now()
	c := store.AccessCode{TokenSHA256: ids.HashKey(token), ArtifactID: a.ID, Email: email,
		CodeSHA256: accessCodeHash(h.UnlockSecret, a.ID, email, code), AccessVersion: a.AccessVersion,
		CreatedAt: now, ExpiresAt: now.Add(accessCodeTTL)}
	if err := h.Store.CreateAccessCode(ctx, c); err != nil {
		return err
	}
	// The link is built from configuration (the page's own host, or the
	// share origin), never from the Host the request came in on: a forged
	// Host would otherwise mail a live token to a server of the attacker's
	// choosing.
	link := h.pageURLs().Page(a.ID, a.Slug) + accessLinkPath + "?t=" + url.QueryEscape(token)
	// In the language of the browser that asked for it.
	text := i18n.T(lang, "Your code to open a page shared with you on byagent:") + "\n\n    " + code + "\n\n" +
		i18n.T(lang, "Enter it on the page, or open this link in the browser you want to read it in:") + "\n\n" + link + "\n\n" +
		i18n.T(lang, "The code and the link work once and stop working in 15 minutes. If you did not ask for this, ignore this email.") + "\n"
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return h.Mail.SendMail(sendCtx, email, i18n.T(lang, "Your byagent code: %s", code), text)
}

// submitAccessCode checks a mailed code typed into the gate.
func (h *Handler) submitAccessCode(w http.ResponseWriter, r *http.Request) {
	a, ok := h.restrictedArtifact(w, r)
	if !ok {
		return
	}
	email, err := auth.NormalizeEmail(r.PostFormValue("email"))
	if err != nil {
		h.renderGate(w, r, GateView{ArtifactID: a.ID, Kind: "email", Wrong: true})
		return
	}
	g := GateView{ArtifactID: a.ID, Kind: "email", Email: email, Sent: true}
	if ok, _ := h.access.codeClient.Allow(a.ID + "|" + h.clientIP(r)); !ok {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	now := h.now()
	given := r.PostFormValue("code")
	c, err := h.Store.LiveAccessCode(r.Context(), a.ID, email, now)
	if err == nil {
		err = h.Store.SpendAccessAttempt(r.Context(), c.TokenSHA256, now, accessCodeAttempts)
	}
	if err != nil || normalizeCode(given) == "" || !hmac.Equal([]byte(c.CodeSHA256), []byte(accessCodeHash(h.UnlockSecret, a.ID, email, given))) {
		g.Wrong = true
		h.renderGate(w, r, g)
		return
	}
	h.admitByCode(w, r, a, c, g)
}

// admitByCode spends a matched code and lets its address in, if the page
// still admits it: the door may have changed since the mail went out.
func (h *Handler) admitByCode(w http.ResponseWriter, r *http.Request, a store.Artifact, c store.AccessCode, g GateView) {
	if c.ArtifactID != a.ID || c.AccessVersion != a.AccessVersion || !AllowedEmail(a.AccessAllow, c.Email) ||
		h.Store.ConsumeAccessCode(r.Context(), c.TokenSHA256, h.now()) != nil {
		g.Wrong = true
		h.renderGate(w, r, g)
		return
	}
	h.setAccessCookie(w, r, a, c.Email)
	http.Redirect(w, r, PageBase(r, a.ID), http.StatusSeeOther)
}

// accessLinkPath is the mailed link. GET only shows a button: mail scanners
// open links to check them, and a link spent on a scanner's visit would never
// reach the person it was sent to. The POST the button makes spends it.
const accessLinkPath = "_access/link"

// accessEmailPath asks for a code by address; accessCodePath sends it back.
const (
	accessEmailPath = "_access/email"
	accessCodePath  = "_access/code"
)

func (h *Handler) liveLink(r *http.Request, a store.Artifact, token string) (store.AccessCode, bool) {
	if token == "" || len(token) > 128 {
		return store.AccessCode{}, false
	}
	c, err := h.Store.AccessCodeByToken(r.Context(), ids.HashKey(token))
	if err != nil || c.ArtifactID != a.ID || c.ConsumedAt != nil || !h.now().Before(c.ExpiresAt) {
		return c, false
	}
	return c, true
}

func (h *Handler) openAccessLink(w http.ResponseWriter, r *http.Request) {
	a, err := h.Store.Artifact(r.Context(), r.PathValue("id"))
	if err != nil || a.DeletedAt != nil || a.AccessMode != AccessRestricted || h.Gate == nil {
		h.refuse(w, r)
		return
	}
	token := r.URL.Query().Get("t")
	c, ok := h.liveLink(r, a, token)
	if !ok {
		h.renderGate(w, r, GateView{ArtifactID: a.ID, Kind: "email", Expired: true})
		return
	}
	h.renderGate(w, r, GateView{ArtifactID: a.ID, Kind: "email", Email: c.Email, Token: token})
}

func (h *Handler) confirmAccessLink(w http.ResponseWriter, r *http.Request) {
	a, ok := h.restrictedArtifact(w, r)
	if !ok {
		return
	}
	c, ok := h.liveLink(r, a, r.PostFormValue("t"))
	if !ok {
		h.renderGate(w, r, GateView{ArtifactID: a.ID, Kind: "email", Expired: true})
		return
	}
	h.admitByCode(w, r, a, c, GateView{ArtifactID: a.ID, Kind: "email", Expired: true})
}
