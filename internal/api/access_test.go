package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anup-a/artifacts/internal/ratelimit"
	"github.com/anup-a/artifacts/internal/serve"
)

type captureMail struct {
	mu   sync.Mutex
	sent []struct{ to, subject, text string }
}

func (m *captureMail) SendMail(_ context.Context, to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, struct{ to, subject, text string }{to, subject, text})
	return nil
}

func (m *captureMail) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// waitMail waits for the n-th message: the gate mails off the request.
func (m *captureMail) waitMail(t *testing.T, n int) (to, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		if len(m.sent) >= n {
			s := m.sent[n-1]
			m.mu.Unlock()
			return s.to, s.text
		}
		m.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no mail %d", n)
	return "", ""
}

func newAccessServer(t *testing.T) (string, Deps, string, *captureMail) {
	t.Helper()
	mail := &captureMail{}
	srv, d, tok := newTestServerWith(t, false, func(d *Deps) { d.Mail = mail })
	return srv.URL, d, tok, mail
}

func patchAccess(t *testing.T, base, tok, id, body string) (int, map[string]any) {
	t.Helper()
	req := jsonReq(t, "PATCH", base+"/api/artifacts/"+id+"/access", body, map[string]string{"Authorization": "Bearer " + tok})
	return do(t, req)
}

// noFollow posts or gets without following redirects, the way a test needs
// to see the unlock's 303 and its cookies.
var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func formPost(t *testing.T, target string, form url.Values) *http.Response {
	t.Helper()
	res, err := noFollow.PostForm(target, form)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}

func grantCookie(t *testing.T, res *http.Response, id string) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == "artifact_access_"+id && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no grant: %d %v", res.StatusCode, res.Cookies())
	return nil
}

func TestAccessModesThroughTheAPI(t *testing.T) {
	base, d, tok, _ := newAccessServer(t)
	id := publishOne(t, base, tok, "<p>hello world</p>")

	code, a := do(t, authReq(t, "GET", base+"/api/artifacts/"+id+"/access", tok))
	if code != 200 || a["mode"] != "anyone_with_link" || a["visibility"] != "public" || a["password_set"] != false || a["share_url"] != "http://share.test/a/"+id+"/" {
		t.Fatalf("default: %d %v", code, a)
	}
	if code, b := do(t, authReq(t, "GET", base+"/api/artifacts/"+id, tok)); code != 200 || b["access"] != "anyone_with_link" {
		t.Fatalf("artifact json: %v", b)
	}

	// Password: the page goes private and no response ever carries the
	// password or its hash.
	code, a = patchAccess(t, base, tok, id, `{"mode":"password","password":"hunter2hunter2"}`)
	raw, _ := json.Marshal(a)
	if code != 200 || a["mode"] != "password" || a["visibility"] != "private" || a["password_set"] != true || strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "argon") {
		t.Fatalf("password: %d %s", code, raw)
	}
	if a["share_url"] != "http://share.test/a/"+id+"/" {
		t.Fatalf("share_url %v", a["share_url"])
	}
	_, b := do(t, authReq(t, "GET", base+"/api/artifacts/"+id, tok))
	raw, _ = json.Marshal(b)
	if b["access"] != "password" || strings.Contains(string(raw), "argon") {
		t.Fatalf("artifact json leaks: %s", raw)
	}
	// Keeping the mode without a new password keeps the password.
	ver := a["access_version"]
	if code, a = patchAccess(t, base, tok, id, `{"mode":"password"}`); code != 200 || a["access_version"] != ver {
		t.Fatalf("keep: %d %v", code, a)
	}
	// A share code would be a second way in.
	if code, _ := do(t, jsonReq(t, "POST", base+"/api/artifacts/"+id+"/share-code", `{}`, map[string]string{"Authorization": "Bearer " + tok})); code != 400 {
		t.Fatalf("share code on a password page: %d", code)
	}

	// Visitors: the gate, then the page.
	res, _ := http.Get(base + "/a/" + id + "/")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 404 || strings.Contains(string(body), "hello world") {
		// No web gate is mounted in this server, so the refusal is a 404.
		t.Fatalf("visitor without password: %d %s", res.StatusCode, body)
	}
	unlock := formPost(t, base+"/a/"+id+"/_unlock", url.Values{"password": {"hunter2hunter2"}})
	if unlock.StatusCode != 303 {
		t.Fatalf("unlock: %d", unlock.StatusCode)
	}
	c := grantCookie(t, unlock, id)
	req, _ := http.NewRequest("GET", base+"/a/"+id+"/", nil)
	req.AddCookie(c)
	res, _ = http.DefaultClient.Do(req)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "hello world") {
		t.Fatalf("visitor with password: %d %s", res.StatusCode, body)
	}

	// Readers who got through comment like code holders do.
	req = jsonReq(t, "POST", base+"/c/"+id+"/comments", `{"author_name":"Pat","body":"nice","anchor":`+anchor+`,"version":1}`, nil)
	req.AddCookie(c)
	if code, th := do(t, req); code != 201 || th["comments"].([]any)[0].(map[string]any)["author_name"] != "Pat" {
		t.Fatalf("comment: %d %v", code, th)
	}
	if code, _ := do(t, jsonReq(t, "POST", base+"/c/"+id+"/comments", `{"author_name":"Eve","body":"x","anchor":`+anchor+`,"version":1}`, nil)); code != 404 {
		t.Fatalf("comment without password: %d", code)
	}

	// Back to anyone with the link: public, password gone.
	code, a = patchAccess(t, base, tok, id, `{"mode":"anyone_with_link"}`)
	if code != 200 || a["mode"] != "anyone_with_link" || a["visibility"] != "public" || a["password_set"] != false {
		t.Fatalf("link: %d %v", code, a)
	}
	st, _ := d.Store.Artifact(context.Background(), id)
	if st.AccessPassword != "" {
		t.Fatal("password hash left behind")
	}
}

func TestAccessPatchValidation(t *testing.T) {
	base, _, tok, _ := newAccessServer(t)
	id := publishOne(t, base, tok, "<p>x</p>")
	for _, body := range []string{
		`{"mode":"password"}`,
		`{"mode":"password","password":"short"}`,
		`{"mode":"password","password":"line\nbreak!!"}`,
		`{"mode":"password","password":"longenough","allowed_emails":["a@b.co"]}`,
		`{"mode":"restricted","allowed_emails":["not-an-email"]}`,
		`{"mode":"restricted","allowed_domains":["*.acme.com"]}`,
		`{"mode":"restricted","allowed_emails":["a@b.co"],"password":"longenough"}`,
		`{"mode":"anyone_with_link","password":"longenough"}`,
		`{"mode":"everyone"}`,
		`{"mode":`,
	} {
		if code, b := patchAccess(t, base, tok, id, body); code != 400 {
			t.Fatalf("%s: %d %v", body, code, b)
		}
	}
}

func TestAccessIsOwnerOnly(t *testing.T) {
	base, d, tok, _ := newAccessServer(t)
	id := publishOne(t, base, tok, "<p>x</p>")
	stranger := mintKey(t, d, "personal")
	if code, _ := do(t, authReq(t, "GET", base+"/api/artifacts/"+id+"/access", stranger)); code != 404 {
		t.Fatalf("stranger read: %d", code)
	}
	if code, _ := patchAccess(t, base, stranger, id, `{"mode":"password","password":"longenough"}`); code != 404 {
		t.Fatalf("stranger patch: %d", code)
	}
	if code, _ := do(t, authReq(t, "GET", base+"/api/artifacts/"+id+"/access", "")); code != 401 {
		t.Fatalf("anonymous read: %d", code)
	}
}

var mailCodeRe = regexp.MustCompile(`\n    (\d{6})\n`)

func TestRestrictedAccessThroughTheAPI(t *testing.T) {
	base, _, tok, mail := newAccessServer(t)
	id := publishOne(t, base, tok, "<p>hello world</p>")

	code, a := patchAccess(t, base, tok, id, `{"mode":"restricted","allowed_emails":["Sam@Acme.com"," pat@x.org"],"allowed_domains":["@Corp.example"]}`)
	if code != 200 || a["mode"] != "restricted" || a["visibility"] != "private" {
		t.Fatalf("restricted: %d %v", code, a)
	}
	emails, domains := a["allowed_emails"].([]any), a["allowed_domains"].([]any)
	if len(emails) != 2 || emails[0] != "pat@x.org" || emails[1] != "sam@acme.com" || len(domains) != 1 || domains[0] != "corp.example" {
		t.Fatalf("lists %v %v", emails, domains)
	}

	// A visitor proves their address and comments under it, whatever name
	// they type.
	if res := formPost(t, base+"/a/"+id+"/_access/email", url.Values{"email": {"lee@corp.example"}}); res.StatusCode != 404 {
		// No web gate mounted: the gate routes refuse rather than render.
		t.Fatalf("gate without a renderer: %d", res.StatusCode)
	}

	// Empty lists: only the workspace, which is a plain private page.
	code, a = patchAccess(t, base, tok, id, `{"mode":"restricted"}`)
	if code != 200 || a["mode"] != "restricted" || a["visibility"] != "private" || a["share_url"] != nil || len(a["allowed_emails"].([]any)) != 0 {
		t.Fatalf("owner only: %d %v", code, a)
	}
	_ = mail
}

func TestRestrictedVisitorsCommentAsTheirAddress(t *testing.T) {
	mail := &captureMail{}
	srv, _, tok := newTestServerWith(t, false, func(d *Deps) {
		d.Mail = mail
		// A gate renderer, as the web app mounts one.
		d.WebGate = func(w http.ResponseWriter, r *http.Request, g serve.GateView) { w.WriteHeader(200) }
	})
	base := srv.URL
	id := publishOne(t, base, tok, "<p>hello world</p>")
	if code, a := patchAccess(t, base, tok, id, `{"mode":"restricted","allowed_domains":["corp.example"]}`); code != 200 {
		t.Fatalf("%d %v", code, a)
	}
	if res := formPost(t, base+"/a/"+id+"/_access/email", url.Values{"email": {"lee@corp.example"}}); res.StatusCode != 200 {
		t.Fatalf("ask for a code: %d", res.StatusCode)
	}
	to, text := mail.waitMail(t, 1)
	m := mailCodeRe.FindStringSubmatch(text)
	if to != "lee@corp.example" || m == nil || !strings.Contains(text, "http://share.test/a/"+id+"/_access/link?t=") {
		t.Fatalf("mail to %s: %q", to, text)
	}
	res := formPost(t, base+"/a/"+id+"/_access/code", url.Values{"email": {"lee@corp.example"}, "code": {m[1]}})
	if res.StatusCode != 303 {
		t.Fatalf("code: %d", res.StatusCode)
	}
	c := grantCookie(t, res, id)
	req := jsonReq(t, "POST", base+"/c/"+id+"/comments", `{"author_name":"The CEO","body":"ship it","anchor":`+anchor+`,"version":1}`, nil)
	req.AddCookie(c)
	code, th := do(t, req)
	if code != 201 {
		t.Fatalf("comment: %d %v", code, th)
	}
	if name := th["comments"].([]any)[0].(map[string]any)["author_name"]; name != "lee@corp.example" {
		t.Fatalf("author %v", name)
	}
	req = jsonReq(t, "POST", base+"/c/"+id+"/comments/"+th["id"].(string)+"/reply", `{"author_name":"Someone else","body":"agreed"}`, nil)
	req.AddCookie(c)
	if code, cm := do(t, req); code != 201 || cm["author_name"] != "lee@corp.example" {
		t.Fatalf("reply: %d %v", code, cm)
	}
	// Off the list, off the comments.
	if code, _ := patchAccess(t, base, tok, id, `{"mode":"restricted","allowed_emails":["sam@acme.com"]}`); code != 200 {
		t.Fatal(code)
	}
	req = jsonReq(t, "GET", base+"/c/"+id+"/comments", "", nil)
	req.AddCookie(c)
	if code, _ := do(t, req); code != 404 {
		t.Fatalf("removed visitor read comments: %d", code)
	}
}

func TestRestrictedNeedsMail(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	id := publishOne(t, srv.URL, tok, "<p>x</p>")
	if code, b := patchAccess(t, srv.URL, tok, id, `{"mode":"restricted","allowed_emails":["sam@acme.com"]}`); code != 503 {
		t.Fatalf("restricted with no mail: %d %v", code, b)
	}
	// Owner only needs no mail.
	if code, b := patchAccess(t, srv.URL, tok, id, `{"mode":"restricted"}`); code != 200 {
		t.Fatalf("owner only: %d %v", code, b)
	}
}

func TestAccessInvites(t *testing.T) {
	base, d, tok, mail := newAccessServer(t)
	id := publishOne(t, base, tok, "<p>x</p>")
	invite := func(body string) (int, map[string]any) {
		return do(t, jsonReq(t, "POST", base+"/api/artifacts/"+id+"/access/invites", body, map[string]string{"Authorization": "Bearer " + tok}))
	}
	if code, _ := invite(`{"emails":["sam@acme.com"]}`); code != 400 {
		t.Fatalf("invite on a public page: %d", code)
	}
	if code, _ := patchAccess(t, base, tok, id, `{"mode":"restricted","allowed_emails":["sam@acme.com"],"allowed_domains":["gmail.com"]}`); code != 200 {
		t.Fatal(code)
	}
	code, b := invite(`{"emails":["Sam@acme.com","sam@acme.com","stranger@gmail.com","nope"]}`)
	n := b["notifications"].(map[string]any)
	sent, skipped := n["sent"].([]any), n["skipped"].([]any)
	if code != 200 || len(sent) != 1 || sent[0] != "sam@acme.com" || len(skipped) != 2 {
		t.Fatalf("invites: %d %v", code, b)
	}
	// A domain entry admits but never mails: no way to mail all of gmail.
	if skipped[0].(map[string]any)["reason"] != "not_allowed" || skipped[0].(map[string]any)["email"] != "stranger@gmail.com" {
		t.Fatalf("skipped %v", skipped)
	}
	if mail.count() != 1 || !strings.Contains(mail.sent[0].text, "http://share.test/a/"+id+"/") {
		t.Fatalf("mail %v", mail.sent)
	}
	if code, _ := invite(`{"emails":[]}`); code != 400 {
		t.Fatalf("empty: %d", code)
	}
	many := `{"emails":[` + strings.TrimSuffix(strings.Repeat(`"a@b.co",`, invitesPerRequest+1), ",") + `]}`
	if code, _ := invite(many); code != 400 {
		t.Fatalf("too many: %d", code)
	}
	_ = d
}

func TestAccessInvitesAreMeteredPerWorkspace(t *testing.T) {
	mail := &captureMail{}
	srv, _, tok := newTestServerWith(t, false, func(d *Deps) {
		d.Mail = mail
		d.InviteLimiter = ratelimit.NewWindow(2, time.Hour)
	})
	id := publishOne(t, srv.URL, tok, "<p>x</p>")
	if code, _ := patchAccess(t, srv.URL, tok, id, `{"mode":"restricted","allowed_emails":["a@acme.com","b@acme.com","c@acme.com"]}`); code != 200 {
		t.Fatal(code)
	}
	_, b := do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/access/invites", `{"emails":["a@acme.com","b@acme.com","c@acme.com"]}`, map[string]string{"Authorization": "Bearer " + tok}))
	n := b["notifications"].(map[string]any)
	failed := n["failed"].([]any)
	if len(n["sent"].([]any)) != 2 || len(failed) != 1 || failed[0].(map[string]any)["error"] != "rate_limited" || mail.count() != 2 {
		t.Fatalf("metering: %v", b)
	}
}

func TestGuestsGetNoAccessControl(t *testing.T) {
	srv, _, _ := newGuestServer(t)
	g := newGuest(t, srv.URL)
	tok := g["token"].(string)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Try", []pubFile{{"index.html", "<body>hi</body>"}}, nil))
	id := b["id"].(string)
	if code, _ := patchAccess(t, srv.URL, tok, id, `{"mode":"password","password":"longenough"}`); code != 403 {
		t.Fatalf("guest password: %d", code)
	}
	if code, _ := do(t, jsonReq(t, "POST", srv.URL+"/api/artifacts/"+id+"/access/invites", `{"emails":["a@b.co"]}`, map[string]string{"Authorization": "Bearer " + tok})); code != 403 {
		t.Fatalf("guest invites: %d", code)
	}
}
