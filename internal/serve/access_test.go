package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/store"
)

// fakeMail records what the gate mails.
type fakeMail struct {
	mu   sync.Mutex
	sent []mailed
}

type mailed struct{ to, subject, text string }

func (m *fakeMail) SendMail(_ context.Context, to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mailed{to, subject, text})
	return nil
}

func (m *fakeMail) all() []mailed {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mailed(nil), m.sent...)
}

// accessFixture serves the fixture artifact through a gate that names what it
// was asked for, so tests can tell the gates apart.
func accessFixture(t *testing.T, tweaks ...func(*Handler)) (*http.ServeMux, *Handler, *fakeMail, string) {
	t.Helper()
	_, st, id := fixture(t)
	mail := &fakeMail{}
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"),
		ShareOrigin: "http://share.test", APIOrigin: "http://app.test", UnlockSecret: "unlock-test-secret",
		Mail: mail, mailSync: true,
		OnView: func(r *http.Request, id, path string) { viewsSeen = append(viewsSeen, id) },
		Gate: func(w http.ResponseWriter, r *http.Request, g GateView) {
			w.Header().Set("Content-Type", "text/plain")
			status := 200
			switch {
			case g.Locked:
				status = 429
			case g.Wrong || g.Expired:
				status = 403
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte("gate:" + g.Kind + " email:" + g.Email + " sent:" + boolStr(g.Sent) + " token:" + g.Token + " nomail:" + boolStr(g.NoMail) + " busy:" + boolStr(g.Busy)))
		}}
	for _, tw := range tweaks {
		tw(h)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	viewsSeen = nil
	return mux, h, mail, id
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func setPassword(t *testing.T, st store.Store, id, pw string) {
	t.Helper()
	hash, err := HashPassword(context.Background(), pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccess(t.Context(), id, store.Access{Mode: AccessPassword, PasswordHash: hash}); err != nil {
		t.Fatal(err)
	}
}

func postForm(mux *http.ServeMux, target string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return do(mux, req)
}

func postPassword(mux *http.ServeMux, id, pw string) *httptest.ResponseRecorder {
	return postForm(mux, "http://share.test/a/"+id+"/_unlock", url.Values{"password": {pw}}, nil)
}

func accessCookieOf(t *testing.T, res *httptest.ResponseRecorder, id string) *http.Cookie {
	t.Helper()
	for _, c := range res.Result().Cookies() {
		if c.Name == accessCookieName(id) && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no access cookie in %d %v", res.Code, res.Result().Cookies())
	return nil
}

func matches(t *testing.T, stored, pw string) bool {
	t.Helper()
	ok, err := passwordMatches(context.Background(), stored, pw)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestPasswordHashing(t *testing.T) {
	// The production cost, once; the rest of the suite hashes cheaply.
	cheap := PasswordCost
	PasswordCost = productionCost
	h, err := HashPassword(context.Background(), "correct horse")
	PasswordCost = cheap
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(h, "correct horse") {
		t.Fatalf("hash %q", h)
	}
	h, _ = HashPassword(context.Background(), "correct horse")
	h2, _ := HashPassword(context.Background(), "correct horse")
	if h == h2 {
		t.Fatal("two hashes of one password match: no salt")
	}
	if !matches(t, h, "correct horse") || matches(t, h, "correct horsE") || matches(t, h, "") {
		t.Fatal("match")
	}
	// A tampered row with absurd parameters is refused, not run.
	m, tc := "m="+strconv.Itoa(int(PasswordCost.MemoryKiB)), "t="+strconv.Itoa(int(PasswordCost.Time))
	if !strings.Contains(h, m+","+tc) {
		t.Fatalf("cheap hash %q", h)
	}
	for _, bad := range []string{"", "plain", strings.Replace(h, m, "m=4194304", 1), strings.Replace(h, tc, "t=999", 1), strings.Replace(h, "v=19", "v=16", 1), "$bcrypt$x$y$z$w"} {
		if matches(t, bad, "correct horse") {
			t.Fatalf("accepted %q", bad)
		}
	}
	for pw, ok := range map[string]bool{"short": false, "eightchr": true, "line\nbreak1": false, strings.Repeat("x", 129): false, "пароль-пароль": true} {
		if (ValidPassword(pw) == nil) != ok {
			t.Fatalf("ValidPassword(%q) = %v", pw, ValidPassword(pw))
		}
	}
}

func TestPasswordGateOpensWithTheRightPassword(t *testing.T) {
	mux, h, _, id := accessFixture(t)
	setPassword(t, h.Store, id, "open sesame")

	res := fetchCode(mux, "http://share.test/a/"+id+"/")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatalf("gate: %d %s", res.Code, res.Body.String())
	}
	if len(viewsSeen) != 0 {
		t.Fatalf("a gate hit counted as a page view: %v", viewsSeen)
	}
	// Assets and covers do not show a gate and do not leak.
	for _, p := range []string{"v/1/s/a.css", "_cover.svg", "v/1/"} {
		if res := fetchCode(mux, "http://share.test/a/"+id+"/"+p); res.Code == 200 && !strings.Contains(res.Body.String(), "gate:") {
			t.Fatalf("%s served without the password: %s", p, res.Body.String())
		}
	}
	if res := postPassword(mux, id, "open sesamE"); res.Code != 403 {
		t.Fatalf("wrong password: %d", res.Code)
	}
	res = postPassword(mux, id, "open sesame")
	if res.Code != 303 || res.Header().Get("Location") != "/a/"+id+"/" {
		t.Fatalf("right password: %d %v", res.Code, res.Header())
	}
	c := accessCookieOf(t, res, id)
	paths := map[string]bool{}
	for _, ck := range res.Result().Cookies() {
		if ck.Name == accessCookieName(id) {
			if !ck.HttpOnly || ck.Domain != "" || ck.SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie flags: %+v", ck)
			}
			paths[ck.Path] = true
		}
	}
	if !paths["/a/"+id+"/"] || !paths["/c/"+id+"/"] || paths["/"] {
		t.Fatalf("cookie paths %v", paths)
	}
	if strings.Contains(c.Value, "sesame") {
		t.Fatal("cookie carries the password")
	}
	res = fetchCode(mux, "http://share.test/a/"+id+"/", c)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "one") && !strings.Contains(res.Body.String(), "two") {
		t.Fatalf("unlocked page: %d %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "data-artifact") {
		t.Fatal("password holder got no comment runtime")
	}
	if len(viewsSeen) != 1 {
		t.Fatalf("views %v", viewsSeen)
	}
	if res := fetchCode(mux, "http://share.test/a/"+id+"/v/1/s/a.css", c); res.Code != 200 {
		t.Fatalf("asset: %d", res.Code)
	}
	// The comment routes admit exactly this reader.
	a, _ := h.Store.Artifact(t.Context(), id)
	req := httptest.NewRequest("GET", "http://share.test/c/"+id+"/comments", nil)
	req.AddCookie(c)
	if !Unlocked(req, a, h.UnlockSecret) || Unlocked(httptest.NewRequest("GET", "http://share.test/c/"+id+"/comments", nil), a, h.UnlockSecret) {
		t.Fatal("Unlocked disagrees with the page")
	}

	// Changing the password signs everyone out, and the gate comes back.
	setPassword(t, h.Store, id, "new password")
	res = fetchCode(mux, "http://share.test/a/"+id+"/", c)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatalf("old cookie after a password change: %d %s", res.Code, res.Body.String())
	}
	// Setting the same password again also signs everyone out: a new salt.
	c2 := accessCookieOf(t, postPassword(mux, id, "new password"), id)
	setPassword(t, h.Store, id, "new password")
	if res := fetchCode(mux, "http://share.test/a/"+id+"/", c2); !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatal("re-set password kept old grants")
	}
}

func TestPasswordGrantCannotBeForgedOrMoved(t *testing.T) {
	mux, h, _, id := accessFixture(t)
	setPassword(t, h.Store, id, "open sesame")
	c := accessCookieOf(t, postPassword(mux, id, "open sesame"), id)
	parts := strings.Split(c.Value, ".")

	a, _ := h.Store.Artifact(t.Context(), id)
	past := time.Now().Add(-time.Minute).Unix()
	for name, v := range map[string]string{
		"empty":        "",
		"garbage":      "x.y.z",
		"longer exp":   "9999999999." + parts[1] + "." + parts[2],
		"email added":  parts[0] + ".c2FtQGFjbWUuY29t." + parts[2],
		"expired":      accessValue(h.UnlockSecret, a, "", past),
		"other secret": accessValue("guess", a, "", time.Now().Add(time.Hour).Unix()),
	} {
		res := fetchCode(mux, "http://share.test/a/"+id+"/", &http.Cookie{Name: accessCookieName(id), Value: v})
		if !strings.Contains(res.Body.String(), "gate:password") {
			t.Fatalf("%s cookie opened the page: %d", name, res.Code)
		}
	}
	// A grant for one page opens no other, even with the same password.
	other := "accessother1"
	sib := a
	sib.ID, sib.Slug = other, ""
	if err := h.Store.CreateArtifact(t.Context(), sib); err != nil {
		t.Fatal(err)
	}
	setPassword(t, h.Store, other, "open sesame")
	if res := fetchCode(mux, "http://share.test/a/"+other+"/", &http.Cookie{Name: accessCookieName(other), Value: c.Value}); !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatal("a grant opened another page")
	}
	// A share code link means nothing on a password page.
	if res := fetchCode(mux, "http://share.test/a/"+id+"/?k=000000"); !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatalf("k on a password page: %d %s", res.Code, res.Body.String())
	}
	// Without a signing secret no grant is ever honoured.
	h.UnlockSecret = ""
	if res := fetchCode(mux, "http://share.test/a/"+id+"/", c); !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatal("grant honoured with no secret")
	}
}

func passwordFrom(mux *http.ServeMux, id, pw, ip string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://share.test/a/"+id+"/_unlock", strings.NewReader(url.Values{"password": {pw}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = ip + ":1234"
	return do(mux, r)
}

func TestPasswordGuessingIsMeteredPerClientAndPage(t *testing.T) {
	mux, h, _, id := accessFixture(t)
	setPassword(t, h.Store, id, "open sesame")
	// One client guesses far past its own budget...
	for i := range 5 * passwordPerClient {
		want := 403
		if i >= passwordPerClient {
			want = 429
		}
		if res := passwordFrom(mux, id, "wrong guess", "198.51.100.1"); res.Code != want {
			t.Fatalf("guess %d: %d", i, res.Code)
		}
	}
	// ...and is locked out even with the right password,
	if res := passwordFrom(mux, id, "open sesame", "198.51.100.1"); res.Code != 429 || !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatalf("not locked: %d", res.Code)
	}
	// but its refused guesses never touched the page's budget: everyone
	// else still gets in.
	if res := passwordFrom(mux, id, "open sesame", "203.0.113.9"); res.Code != 303 {
		t.Fatalf("one client locked everyone out: %d", res.Code)
	}
	// The page-wide cap is a backstop that takes several clients to reach.
	if passwordPerPage <= passwordPerClient {
		t.Fatalf("page cap %d is reachable by one client", passwordPerPage)
	}
	used := passwordPerClient + 1
	for i := 0; used < passwordPerPage; i++ {
		for j := 0; j < passwordPerClient && used < passwordPerPage; j++ {
			passwordFrom(mux, id, "nope nope", "192.0.2."+strconv.Itoa(10+i))
			used++
		}
	}
	if res := passwordFrom(mux, id, "open sesame", "192.0.2.200"); res.Code != 429 || !strings.Contains(res.Body.String(), "gate:password") {
		t.Fatalf("page backstop: %d %s", res.Code, res.Body.String())
	}
}

// With every argon2 slot taken, a check waits a short while and then answers
// busy rather than queueing without bound.
func TestPasswordCheckAnswersBusyWhenSlotsAreFull(t *testing.T) {
	mux, h, _, id := accessFixture(t)
	setPassword(t, h.Store, id, "open sesame")
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	res := passwordFrom(mux, id, "open sesame", "198.51.100.7")
	_, hashErr := HashPassword(context.Background(), "whatever1")
	for range cap(hashSlots) {
		<-hashSlots
	}
	if !strings.Contains(res.Body.String(), "busy:yes") || len(res.Result().Cookies()) != 0 {
		t.Fatalf("busy: %d %s", res.Code, res.Body.String())
	}
	if hashErr != ErrBusy {
		t.Fatalf("HashPassword with no slot: %v", hashErr)
	}
	if res := passwordFrom(mux, id, "open sesame", "198.51.100.7"); res.Code != 303 {
		t.Fatalf("after the slots free up: %d", res.Code)
	}
}

func TestGateFormsRefuseCrossSitePosts(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	setPassword(t, h.Store, id, "open sesame")
	if res := postForm(mux, "http://share.test/a/"+id+"/_unlock", url.Values{"password": {"open sesame"}}, map[string]string{"Origin": "https://evil.example"}); res.Code != 404 {
		t.Fatalf("cross-site password post: %d", res.Code)
	}
	if res := postForm(mux, "http://share.test/a/"+id+"/_unlock", url.Values{"password": {"open sesame"}}, map[string]string{"Origin": "http://share.test"}); res.Code != 303 {
		t.Fatalf("same-site password post: %d", res.Code)
	}
	// The gate is no-referrer, so its own forms arrive with Origin: null;
	// Fetch Metadata tells them apart from another site's.
	if res := postForm(mux, "http://share.test/a/"+id+"/_unlock", url.Values{"password": {"open sesame"}}, map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"}); res.Code != 303 {
		t.Fatalf("gate's own post: %d", res.Code)
	}
	if res := postForm(mux, "http://share.test/a/"+id+"/_unlock", url.Values{"password": {"open sesame"}}, map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"}); res.Code != 404 {
		t.Fatalf("cross-site post with a null origin: %d", res.Code)
	}
	restrict(t, h.Store, id, "sam@acme.com")
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/email", url.Values{"email": {"sam@acme.com"}}, map[string]string{"Origin": "https://evil.example"}); res.Code != 404 || len(mail.all()) != 0 {
		t.Fatalf("cross-site mail request: %d %v", res.Code, mail.all())
	}
}

func restrict(t *testing.T, st store.Store, id string, allow ...string) {
	t.Helper()
	if err := st.SetAccess(t.Context(), id, store.Access{Mode: AccessRestricted, Allow: allow}); err != nil {
		t.Fatal(err)
	}
}

var codeRe = regexp.MustCompile(`\n    (\d{6})\n`)
var linkRe = regexp.MustCompile(`(http://share\.test/a/[^/]+/_access/link\?t=[A-Za-z0-9]+)`)

func requestCode(t *testing.T, mux *http.ServeMux, id, email string) *httptest.ResponseRecorder {
	t.Helper()
	return postForm(mux, "http://share.test/a/"+id+"/_access/email", url.Values{"email": {email}}, nil)
}

func lastMail(t *testing.T, mail *fakeMail) (code, link string) {
	t.Helper()
	all := mail.all()
	if len(all) == 0 {
		t.Fatal("nothing mailed")
	}
	m := all[len(all)-1]
	cm, lm := codeRe.FindStringSubmatch(m.text), linkRe.FindStringSubmatch(m.text)
	if cm == nil || lm == nil {
		t.Fatalf("mail without code or link: %q", m.text)
	}
	return cm[1], lm[1]
}

func TestRestrictedPageLetsInVerifiedAddressesOnly(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	restrict(t, h.Store, id, "@acme.com", "sam@other.org")

	res := fetchCode(mux, "http://share.test/a/"+id+"/")
	if !strings.Contains(res.Body.String(), "gate:email") || strings.Contains(res.Body.String(), "acme") {
		t.Fatalf("gate: %s", res.Body.String())
	}

	// An address off the list gets the same answer as one on it, and no mail.
	res = requestCode(t, mux, id, "Mallory@Evil.com")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "email:mallory@evil.com sent:yes") {
		t.Fatalf("off-list answer: %d %s", res.Code, res.Body.String())
	}
	if len(mail.all()) != 0 {
		t.Fatal("mailed an address off the list")
	}
	// Subdomains are not the domain.
	requestCode(t, mux, id, "x@eu.acme.com")
	if len(mail.all()) != 0 {
		t.Fatal("subdomain treated as the domain")
	}

	res = requestCode(t, mux, id, "Jo@ACME.com")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "email:jo@acme.com sent:yes") {
		t.Fatalf("on-list answer: %s", res.Body.String())
	}
	if m := mail.all()[0]; m.to != "jo@acme.com" || !strings.Contains(m.subject, "byagent") {
		t.Fatalf("mail %+v", m)
	}
	code, _ := lastMail(t, mail)

	// The code is for this address only.
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"sam@other.org"}, "code": {code}}, nil); res.Code != 403 {
		t.Fatalf("code used for another address: %d", res.Code)
	}
	res = postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"jo@acme.com"}, "code": {code}}, nil)
	if res.Code != 303 {
		t.Fatalf("right code: %d %s", res.Code, res.Body.String())
	}
	c := accessCookieOf(t, res, id)
	if res := fetchCode(mux, "http://share.test/a/"+id+"/", c); res.Code != 200 || strings.Contains(res.Body.String(), "gate:") {
		t.Fatalf("verified visitor: %d %s", res.Code, res.Body.String())
	}
	a, _ := h.Store.Artifact(t.Context(), id)
	req := httptest.NewRequest("POST", "http://share.test/c/"+id+"/comments", nil)
	req.AddCookie(c)
	if got := VisitorEmail(req, a, h.UnlockSecret); got != "jo@acme.com" {
		t.Fatalf("VisitorEmail %q", got)
	}
	// A code works once.
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"jo@acme.com"}, "code": {code}}, nil); res.Code != 403 {
		t.Fatalf("code reused: %d", res.Code)
	}

	// Taking the domain off the list takes the visitor off the page at once.
	restrict(t, h.Store, id, "sam@other.org")
	if res := fetchCode(mux, "http://share.test/a/"+id+"/", c); !strings.Contains(res.Body.String(), "gate:email") {
		t.Fatalf("removed visitor still in: %s", res.Body.String())
	}
	// The cookie cannot be rewritten to a listed address: the email is signed.
	parts := strings.Split(c.Value, ".")
	forged := parts[0] + ".c2FtQG90aGVyLm9yZw." + parts[2] // sam@other.org
	if res := fetchCode(mux, "http://share.test/a/"+id+"/", &http.Cookie{Name: accessCookieName(id), Value: forged}); !strings.Contains(res.Body.String(), "gate:email") {
		t.Fatal("forged address accepted")
	}
}

func TestRestrictedCodeBurnsAfterWrongGuesses(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	restrict(t, h.Store, id, "sam@acme.com")
	requestCode(t, mux, id, "sam@acme.com")
	code, _ := lastMail(t, mail)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for i := range accessCodeAttempts {
		if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"sam@acme.com"}, "code": {wrong}}, nil); res.Code != 403 {
			t.Fatalf("guess %d: %d", i, res.Code)
		}
	}
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"sam@acme.com"}, "code": {code}}, nil); res.Code != 403 {
		t.Fatalf("burnt code still opened the page: %d", res.Code)
	}
	// A fresh code works, and asking again spends the older one.
	requestCode(t, mux, id, "sam@acme.com")
	first, _ := lastMail(t, mail)
	requestCode(t, mux, id, "sam@acme.com")
	second, _ := lastMail(t, mail)
	if first != second {
		if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"sam@acme.com"}, "code": {first}}, nil); res.Code != 403 {
			t.Fatalf("superseded code: %d", res.Code)
		}
	}
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"sam@acme.com"}, "code": {second}}, nil); res.Code != 303 {
		t.Fatalf("fresh code: %d", res.Code)
	}
	// Mail is metered per address: the third request above was the limit.
	if res := requestCode(t, mux, id, "sam@acme.com"); res.Code != 429 {
		t.Fatalf("mail not metered: %d", res.Code)
	}
}

func TestRestrictedLinkSurvivesScannersAndWorksOnce(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	restrict(t, h.Store, id, "sam@acme.com")
	// A forged Host must not steer the mailed link anywhere else.
	r := httptest.NewRequest("POST", "http://evil.example/a/"+id+"/_access/email", strings.NewReader(url.Values{"email": {"sam@acme.com"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	do(mux, r)
	_, link := lastMail(t, mail)
	if !strings.HasPrefix(link, "http://share.test/a/"+id+"/") {
		t.Fatalf("link %q", link)
	}
	u, _ := url.Parse(link)
	token := u.Query().Get("t")

	// Opening it (as a mail scanner would) only shows a button.
	for range 3 {
		res := fetchCode(mux, link)
		if res.Code != 200 || !strings.Contains(res.Body.String(), "token:"+token) || len(res.Result().Cookies()) != 0 {
			t.Fatalf("link GET: %d %s", res.Code, res.Body.String())
		}
	}
	// The token belongs to this page only.
	sib, _ := h.Store.Artifact(t.Context(), id)
	sib.ID, sib.Slug = "accessother2", ""
	if err := h.Store.CreateArtifact(t.Context(), sib); err != nil {
		t.Fatal(err)
	}
	restrict(t, h.Store, sib.ID, "sam@acme.com")
	if res := postForm(mux, "http://share.test/a/"+sib.ID+"/_access/link", url.Values{"t": {token}}, nil); res.Code != 403 {
		t.Fatalf("token used on another page: %d", res.Code)
	}
	res := postForm(mux, "http://share.test/a/"+id+"/_access/link", url.Values{"t": {token}}, nil)
	if res.Code != 303 {
		t.Fatalf("link POST: %d %s", res.Code, res.Body.String())
	}
	accessCookieOf(t, res, id)
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/link", url.Values{"t": {token}}, nil); res.Code != 403 {
		t.Fatalf("link reused: %d", res.Code)
	}
	if res := fetchCode(mux, link); res.Code != 403 {
		t.Fatalf("spent link GET: %d", res.Code)
	}
}

func TestRestrictedCodeDiesWithTheDoor(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	restrict(t, h.Store, id, "sam@acme.com")
	requestCode(t, mux, id, "sam@acme.com")
	code, _ := lastMail(t, mail)
	// Switching to a password and back moves the access version: the mailed
	// code was issued under the old door and opens nothing now.
	setPassword(t, h.Store, id, "open sesame")
	restrict(t, h.Store, id, "sam@acme.com")
	if res := postForm(mux, "http://share.test/a/"+id+"/_access/code", url.Values{"email": {"sam@acme.com"}, "code": {code}}, nil); res.Code != 403 {
		t.Fatalf("code outlived its door: %d", res.Code)
	}
}

func TestRestrictedGateWithoutMail(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	restrict(t, h.Store, id, "sam@acme.com")
	h.Mail = nil
	res := requestCode(t, mux, id, "sam@acme.com")
	if !strings.Contains(res.Body.String(), "nomail:yes") || len(mail.all()) != 0 {
		t.Fatalf("no-mail gate: %s", res.Body.String())
	}
}

func TestNormalizeAllow(t *testing.T) {
	got, err := NormalizeAllow([]string{" Sam@Acme.com ", "sam@acme.com"}, []string{"@ACME.com", "other.org", "acme.com"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "@acme.com,@other.org,sam@acme.com" {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"", "localhost", "acme..com", "-acme.com", "ac me.com", "acme.com/x", "*.acme.com", "@"} {
		if _, err := NormalizeAllow(nil, []string{bad}); err == nil {
			t.Fatalf("accepted domain %q", bad)
		}
	}
	for _, bad := range []string{"not an email", "a@b@c.com", "<a@b.com>"} {
		if _, err := NormalizeAllow([]string{bad}, nil); err == nil {
			t.Fatalf("accepted email %q", bad)
		}
	}
	many := make([]string, AllowMax+1)
	for i := range many {
		many[i] = "u" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + "@acme.com"
	}
	if _, err := NormalizeAllow(many, nil); err == nil {
		t.Fatal("no cap on the allowlist")
	}
	emails, domains := SplitAllow(got)
	if len(emails) != 1 || len(domains) != 2 || domains[0] != "acme.com" {
		t.Fatalf("split %v %v", emails, domains)
	}
	if !AllowedEmail(got, "x@acme.com") || AllowedEmail(got, "x@notacme.com") || AllowedEmail(got, "acme.com") {
		t.Fatal("AllowedEmail")
	}
}

func requestCodeFrom(mux *http.ServeMux, id, email, ip string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://share.test/a/"+id+"/_access/email", strings.NewReader(url.Values{"email": {email}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = ip + ":1234"
	return do(mux, r)
}

// gateAnswer is what a visitor can see of an answer: status, cookies and the
// gate's state, with the address they typed taken out.
func gateAnswer(res *httptest.ResponseRecorder, email string) string {
	return strconv.Itoa(res.Code) + " " + strconv.Itoa(len(res.Result().Cookies())) + " " + strings.Replace(res.Body.String(), email, "<email>", 1)
}

func TestGateMailMetersChargeOnlyWhatTheyShould(t *testing.T) {
	mux, h, mail, id := accessFixture(t)
	restrict(t, h.Store, id, "sam@acme.com", "lee@acme.com")
	h.access.mailPage = ratelimit.NewWindow(2, mailWindow)

	// Unlisted addresses, from many clients, never spend the page's budget.
	for i := range 20 {
		requestCodeFrom(mux, id, "x"+strconv.Itoa(i)+"@evil.example", "198.51.100."+strconv.Itoa(i))
	}
	requestCodeFrom(mux, id, "sam@acme.com", "203.0.113.1")
	if len(mail.all()) != 1 {
		t.Fatalf("unlisted requests spent the page budget: %d mails", len(mail.all()))
	}

	// A client past its own budget is refused before anything else is
	// charged: its refused requests do not spend the address's budget.
	ip := "203.0.113.50"
	for i := range mailPerClient {
		requestCodeFrom(mux, id, "y"+strconv.Itoa(i)+"@evil.example", ip)
	}
	for range 5 {
		if res := requestCodeFrom(mux, id, "lee@acme.com", ip); res.Code != 429 {
			t.Fatalf("client budget: %d", res.Code)
		}
	}
	listed := requestCodeFrom(mux, id, "lee@acme.com", "203.0.113.51")
	if len(mail.all()) != 2 {
		t.Fatalf("refused requests spent lee's budget: %d mails", len(mail.all()))
	}

	// The page's budget is now spent. A listed address gets exactly the
	// answer an unlisted one does, and no mail.
	spent := requestCodeFrom(mux, id, "sam@acme.com", "203.0.113.52")
	unlisted := requestCodeFrom(mux, id, "nobody@evil.example", "203.0.113.53")
	if len(mail.all()) != 2 {
		t.Fatal("mailed past the page budget")
	}
	if a, b, c := gateAnswer(listed, "lee@acme.com"), gateAnswer(spent, "sam@acme.com"), gateAnswer(unlisted, "nobody@evil.example"); a != b || b != c {
		t.Fatalf("answers differ:\n%s\n%s\n%s", a, b, c)
	}
}

func TestGateMailDailyCaps(t *testing.T) {
	mux, h, mail, id := accessFixture(t, func(h *Handler) { h.MailPerWorkspaceDay, h.MailPerDay = 2, 3 })
	restrict(t, h.Store, id, "@acme.com")
	for i := range 4 {
		requestCodeFrom(mux, id, "u"+strconv.Itoa(i)+"@acme.com", "198.51.100."+strconv.Itoa(i))
	}
	if n := len(mail.all()); n != 2 {
		t.Fatalf("workspace cap: %d mails", n)
	}
	// Another workspace still has its own budget, up to the server's.
	a, _ := h.Store.Artifact(t.Context(), id)
	k, err := h.Store.CreateKey(t.Context(), "other-key", "o@x.io")
	if err != nil {
		t.Fatal(err)
	}
	other := a
	other.ID, other.KeyID, other.TenantID, other.Slug = "accessother3", k.ID, "", ""
	if err := h.Store.CreateArtifact(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	restrict(t, h.Store, other.ID, "@acme.com")
	if o, _ := h.Store.Artifact(t.Context(), other.ID); o.TenantID == a.TenantID {
		t.Fatal("same workspace")
	}
	for i := range 3 {
		requestCodeFrom(mux, other.ID, "v"+strconv.Itoa(i)+"@acme.com", "192.0.2."+strconv.Itoa(i))
	}
	if n := len(mail.all()); n != 3 {
		t.Fatalf("server cap: %d mails", n)
	}
}

// slowStore holds CreateAccessCode until released, to show the request does
// not wait for it.
type slowStore struct {
	store.Store
	release chan struct{}
}

func (s slowStore) CreateAccessCode(ctx context.Context, c store.AccessCode) error {
	<-s.release
	return s.Store.CreateAccessCode(ctx, c)
}

// The answer comes back before the list is even consulted: storing the code
// and mailing it happen after, so a listed and an unlisted address take the
// same time.
func TestGateAnswersBeforeTouchingStoreOrMail(t *testing.T) {
	release := make(chan struct{})
	mux, h, mail, id := accessFixture(t, func(h *Handler) {
		h.mailSync = false
		h.Store = slowStore{Store: h.Store, release: release}
	})
	restrict(t, h.Store, id, "sam@acme.com")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- requestCodeFrom(mux, id, "sam@acme.com", "198.51.100.1") }()
	select {
	case res := <-done:
		if !strings.Contains(res.Body.String(), "sent:yes") {
			t.Fatalf("answer: %s", res.Body.String())
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the request waited for the store")
	}
	if len(mail.all()) != 0 {
		t.Fatal("mailed before the code was stored")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for len(mail.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(mail.all()) != 1 {
		t.Fatal("the code was never mailed")
	}
}
