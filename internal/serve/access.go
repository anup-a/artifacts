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

	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
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
	// Password guesses are metered per page and address, and per page overall
	// so that many addresses still share one budget.
	passwordPerClient = 10
	passwordPerPage   = 100
	passwordWindow    = 15 * time.Minute
	// Codes mailed: per page and address, per client, per page.
	mailPerAddress = 3
	mailPerClient  = 10
	mailPerPage    = 50
	mailWindow     = time.Hour
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
}

func newAccessGuards(now func() time.Time) *accessGuards {
	g := &accessGuards{
		passwordClient: ratelimit.NewWindow(passwordPerClient, passwordWindow),
		passwordPage:   ratelimit.NewWindow(passwordPerPage, passwordWindow),
		mailAddress:    ratelimit.NewWindow(mailPerAddress, mailWindow),
		mailClient:     ratelimit.NewWindow(mailPerClient, mailWindow),
		mailPage:       ratelimit.NewWindow(mailPerPage, mailWindow),
		codeClient:     ratelimit.NewWindow(codePerClient, passwordWindow),
	}
	if now != nil {
		for _, l := range []*ratelimit.Limiter{g.passwordClient, g.passwordPage, g.mailAddress, g.mailClient, g.mailPage, g.codeClient} {
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
// against many pages cannot turn into a burst of memory.
var hashSlots = make(chan struct{}, 4)

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

// HashPassword returns the argon2id hash of pw in the PHC string format.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	cost := PasswordCost
	hashSlots <- struct{}{}
	key := argon2.IDKey([]byte(pw), salt, cost.Time, cost.MemoryKiB, argonThreads, argonKeyLen)
	<-hashSlots
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, cost.MemoryKiB, cost.Time, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// passwordMatches checks pw against a stored hash in constant time. Anything
// it cannot parse, or with parameters past what HashPassword writes, fails.
func passwordMatches(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || m > 64*1024 || t == 0 || t > 10 || p == 0 || p > 4 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 64 {
		return false
	}
	hashSlots <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-hashSlots
	return subtle.ConstantTimeCompare(got, want) == 1
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

func accessCookieName(id string) string { return "artifact_access_" + id }

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
	c, err := r.Cookie(accessCookieName(a.ID))
	if err != nil {
		return "", false
	}
	parts := strings.Split(c.Value, ".")
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

func (h *Handler) setAccessCookie(w http.ResponseWriter, r *http.Request, a store.Artifact, email string) {
	exp := h.now().Add(unlockTTL).Unix()
	for _, path := range unlockCookiePaths(a.ID) {
		http.SetCookie(w, &http.Cookie{
			Name:     accessCookieName(a.ID),
			Value:    accessValue(h.UnlockSecret, a, email, exp),
			Path:     path,
			HttpOnly: true,
			Secure:   r.TLS != nil || strings.HasPrefix(h.ShareOrigin, "https://"),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(unlockTTL.Seconds()),
		})
	}
}

func clearAccessCookie(w http.ResponseWriter, id string) {
	for _, path := range unlockCookiePaths(id) {
		http.SetCookie(w, &http.Cookie{Name: accessCookieName(id), Value: "", Path: path, HttpOnly: true, MaxAge: -1})
	}
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
	ok1, _ := h.access.passwordClient.Allow(a.ID + "|" + h.clientIP(r))
	ok2, _ := h.access.passwordPage.Allow(a.ID)
	if !ok1 || !ok2 {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	if given == "" || len(given) > 4*PasswordMaxLen || h.UnlockSecret == "" || !passwordMatches(a.AccessPassword, given) {
		g.Wrong = true
		h.renderGate(w, r, g)
		return
	}
	h.setAccessCookie(w, r, a, "")
	http.Redirect(w, r, "/a/"+a.ID+"/", http.StatusSeeOther)
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
	ok1, _ := h.access.mailClient.Allow(h.clientIP(r))
	ok2, _ := h.access.mailAddress.Allow(a.ID + "|" + email)
	ok3, _ := h.access.mailPage.Allow(a.ID)
	if !ok1 || !ok2 || !ok3 {
		g.Locked = true
		h.renderGate(w, r, g)
		return
	}
	g.Email, g.Sent = email, true
	if AllowedEmail(a.AccessAllow, email) {
		if err := h.mailAccessCode(r.Context(), a, email); err != nil {
			slog.Error("access code", "artifact", a.ID, "err", err)
		}
	}
	h.renderGate(w, r, g)
}

func (h *Handler) mailAccessCode(ctx context.Context, a store.Artifact, email string) error {
	token, code := ids.NewToken(), ids.NewShareCode()
	now := h.now()
	c := store.AccessCode{TokenSHA256: ids.HashKey(token), ArtifactID: a.ID, Email: email,
		CodeSHA256: accessCodeHash(h.UnlockSecret, a.ID, email, code), AccessVersion: a.AccessVersion,
		CreatedAt: now, ExpiresAt: now.Add(accessCodeTTL)}
	if err := h.Store.CreateAccessCode(ctx, c); err != nil {
		return err
	}
	// The link is built on the share origin, never on the Host the request
	// came in on: a forged Host would otherwise mail a live token to a server
	// of the attacker's choosing.
	link := h.ShareOrigin + "/a/" + a.ID + "/" + accessLinkPath + "?t=" + url.QueryEscape(token)
	text := "Your code to open a page shared with you on byagent:\n\n    " + code + "\n\n" +
		"Enter it on the page, or open this link in the browser you want to read it in:\n\n" + link + "\n\n" +
		"The code and the link work once and stop working in 15 minutes. If you did not ask for this, ignore this email.\n"
	h.sendMail(email, "Your byagent code: "+code, text)
	return nil
}

// sendMail hands a message to the mailer off the request, so how long the
// provider takes does not tell a visitor whether their address was mailed.
func (h *Handler) sendMail(to, subject, text string) {
	if h.mailSync {
		if err := h.Mail.SendMail(context.Background(), to, subject, text); err != nil {
			slog.Error("send mail", "err", err)
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.Mail.SendMail(ctx, to, subject, text); err != nil {
			slog.Error("send mail", "err", err)
		}
	}()
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
	http.Redirect(w, r, "/a/"+a.ID+"/", http.StatusSeeOther)
}

// accessLinkPath is the mailed link. GET only shows a button: mail scanners
// open links to check them, and a link spent on a scanner's visit would never
// reach the person it was sent to. The POST the button makes spends it.
const accessLinkPath = "_access/link"

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
