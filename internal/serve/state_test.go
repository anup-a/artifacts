package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/ratelimit"
	"github.com/anup-a/byagent/internal/store"
)

// stateFixture is the fixture's page on the share origin, with a handler
// whose state routes can be called directly.
func stateFixture(t *testing.T) (*http.ServeMux, *Handler, store.Store, string) {
	t.Helper()
	_, st, id := fixture(t)
	h := &Handler{Store: st, Blobs: fixtureBlobs, Runtime: []byte("runtime"),
		ShareOrigin: "http://share.test", APIOrigin: "http://app.test", UnlockSecret: "unlock-test-secret"}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, h, st, id
}

func stateReq(method, target, origin, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	} else {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	return req
}

func stateBody(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON (%d): %s", res.Code, res.Body.String())
	}
	return out
}

func TestStateReadWriteOnShareOrigin(t *testing.T) {
	mux, _, _, id := stateFixture(t)
	path := "http://share.test/a/" + id + "/_state"

	res := do(mux, stateReq("GET", path, "", ""))
	if b := stateBody(t, res); res.Code != 200 || b["revision"].(float64) != 0 || len(b["items"].(map[string]any)) != 0 {
		t.Fatalf("empty read: %d %v", res.Code, b)
	}
	if cc := res.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("cache-control %q", cc)
	}
	res = do(mux, stateReq("POST", path, "http://share.test", `{"items":{"todo:1":{"done":true},"count":3,"note":"hi"}}`))
	if b := stateBody(t, res); res.Code != 200 || b["revision"].(float64) != 1 {
		t.Fatalf("write: %d %v", res.Code, b)
	}
	res = do(mux, stateReq("POST", path, "http://share.test", `{"items":{"note":null,"count":4}}`))
	if b := stateBody(t, res); res.Code != 200 || b["revision"].(float64) != 2 {
		t.Fatalf("patch: %d %v", res.Code, b)
	}
	res = do(mux, stateReq("GET", path, "", ""))
	b := stateBody(t, res)
	items := b["items"].(map[string]any)
	if b["revision"].(float64) != 2 || len(items) != 2 || items["count"].(float64) != 4 || items["todo:1"].(map[string]any)["done"] != true {
		t.Fatalf("read back: %v", b)
	}
	// since=<current> answers the revision alone; an older one, everything.
	if b := stateBody(t, do(mux, stateReq("GET", path+"?since=2", "", ""))); b["items"] != nil || b["revision"].(float64) != 2 {
		t.Fatalf("since current: %v", b)
	}
	if b := stateBody(t, do(mux, stateReq("GET", path+"?since=1", "", ""))); b["items"] == nil {
		t.Fatalf("since older: %v", b)
	}
}

func TestStateRejectsBadWrites(t *testing.T) {
	mux, _, st, id := stateFixture(t)
	path := "http://share.test/a/" + id + "/_state"
	many := map[string]int{}
	for i := 0; i <= StatePatchMax; i++ {
		many[fmt.Sprintf("k%d", i)] = i
	}
	manyJSON, _ := json.Marshal(map[string]any{"items": many})
	for name, tc := range map[string]struct {
		body string
		code int
	}{
		"not json":      {`nope`, 400},
		"no items":      {`{"items":{}}`, 400},
		"bad key":       {`{"items":{"a b":1}}`, 400},
		"slash key":     {`{"items":{"a/b":1}}`, 400},
		"long key":      {`{"items":{"` + strings.Repeat("k", StateKeyMax+1) + `":1}}`, 400},
		"too many keys": {string(manyJSON), 400},
		"big value":     {`{"items":{"k":"` + strings.Repeat("x", StateValueMax) + `"}}`, 413},
		"big body":      {`{"items":{"k":"` + strings.Repeat("x", StateBodyMax) + `"}}`, 413},
	} {
		res := do(mux, stateReq("POST", path, "http://share.test", tc.body))
		if res.Code != tc.code {
			t.Errorf("%s: %d %s", name, res.Code, res.Body.String())
		}
	}
	if s, _ := st.State(t.Context(), id); s.Revision != 0 {
		t.Fatalf("a refused write changed state: %+v", s)
	}
	// A key at the limit is fine.
	if res := do(mux, stateReq("POST", path, "http://share.test", `{"items":{"`+strings.Repeat("k", StateKeyMax)+`":1}}`)); res.Code != 200 {
		t.Fatalf("max key: %d %s", res.Code, res.Body.String())
	}
}

func TestStatePageCap(t *testing.T) {
	mux, h, st, id := stateFixture(t)
	path := "http://share.test/a/" + id + "/_state"
	set := map[string]string{}
	for i := 0; i < StatePageKeys; i++ {
		set[fmt.Sprintf("k%04d", i)] = "1"
	}
	if _, err := st.PatchState(t.Context(), id, set, nil, store.StateLimits{}, h.now()); err != nil {
		t.Fatal(err)
	}
	res := do(mux, stateReq("POST", path, "http://share.test", `{"items":{"one-more":1}}`))
	if res.Code != 413 || !strings.Contains(res.Body.String(), "full") {
		t.Fatalf("over the cap: %d %s", res.Code, res.Body.String())
	}
	// Changing an existing key, or deleting one, still works when full.
	if res := do(mux, stateReq("POST", path, "http://share.test", `{"items":{"k0001":2,"k0002":null}}`)); res.Code != 200 {
		t.Fatalf("rewrite when full: %d %s", res.Code, res.Body.String())
	}
}

func TestStateRefusesOtherOrigins(t *testing.T) {
	mux, _, _, id := stateFixture(t)
	path := "http://share.test/a/" + id + "/_state"
	for _, req := range []*http.Request{
		stateReq("POST", path, "http://evil.test", `{"items":{"a":1}}`),
		stateReq("GET", path, "http://evil.test", ""),
		httptest.NewRequest("GET", path, nil), // no Origin, no Sec-Fetch-Site
	} {
		if res := do(mux, req); res.Code != 403 {
			t.Fatalf("%s from %q: %d", req.Method, req.Header.Get("Origin"), res.Code)
		}
	}
}

func TestStateFollowsTheAccessGate(t *testing.T) {
	mux, _, st, id := stateFixture(t)
	ctx := t.Context()
	path := "http://share.test/a/" + id + "/_state"
	if err := st.SetVisibility(ctx, id, "private"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareCode(ctx, id, "481523", nil); err != nil {
		t.Fatal(err)
	}
	if res := do(mux, stateReq("GET", path, "", "")); res.Code != 404 {
		t.Fatalf("locked read: %d", res.Code)
	}
	if res := do(mux, stateReq("POST", path, "http://share.test", `{"items":{"a":1}}`)); res.Code != 404 {
		t.Fatalf("locked write: %d", res.Code)
	}
	cookie := unlockCookieOf(t, postCode(mux, id, "481523"), id)
	req := stateReq("POST", path, "http://share.test", `{"items":{"a":1}}`)
	req.AddCookie(cookie)
	if res := do(mux, req); res.Code != 200 {
		t.Fatalf("unlocked write: %d %s", res.Code, res.Body.String())
	}
	// Turning state off closes the routes; deleting the page answers 410.
	if err := st.SetStateEnabled(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	req = stateReq("GET", path, "", "")
	req.AddCookie(cookie)
	if res := do(mux, req); res.Code != 404 {
		t.Fatalf("state off: %d", res.Code)
	}
	_ = st.SetStateEnabled(ctx, id, true)
	_ = st.SoftDelete(ctx, id, time.Now())
	req = stateReq("GET", path, "", "")
	req.AddCookie(cookie)
	if res := do(mux, req); res.Code != 410 {
		t.Fatalf("deleted: %d", res.Code)
	}
}

// On a page host the route names no page: the host is the page, so a
// request there reaches that page's state and no other.
func TestStateOnPageHostIsThatPageOnly(t *testing.T) {
	_, h, st, id := stateFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	k, _ := st.CreateKey(ctx, "h2", "e2")
	other := "serve0000002"
	if err := st.CreateArtifact(ctx, store.Artifact{ID: other, KeyID: k.ID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PatchState(ctx, other, map[string]string{"secret": `"other page"`}, nil, store.StateLimits{}, now); err != nil {
		t.Fatal(err)
	}
	onHost := func(req *http.Request) *httptest.ResponseRecorder {
		req = WithPageHost(req, PageHost{Origin: "http://one.page.test", ArtifactID: id})
		w := httptest.NewRecorder()
		h.ServePage(w, req)
		return w
	}
	res := onHost(stateReq("POST", "http://one.page.test/_state", "http://one.page.test", `{"items":{"mine":1}}`))
	if res.Code != 200 {
		t.Fatalf("own write: %d %s", res.Code, res.Body.String())
	}
	b := stateBody(t, onHost(stateReq("GET", "http://one.page.test/_state", "", "")))
	items := b["items"].(map[string]any)
	if len(items) != 1 || items["mine"] == nil {
		t.Fatalf("page host read %v", b)
	}
	// Another page's origin may neither write nor read here.
	if res := onHost(stateReq("POST", "http://one.page.test/_state", "http://two.page.test", `{"items":{"x":1}}`)); res.Code != 403 {
		t.Fatalf("cross-page write: %d", res.Code)
	}
	if res := onHost(stateReq("GET", "http://one.page.test/_state", "http://two.page.test", "")); res.Code != 403 {
		t.Fatalf("cross-page read: %d", res.Code)
	}
	if s, _ := st.State(ctx, other); len(s.Items) != 1 || s.Revision != 1 {
		t.Fatalf("the other page's state moved: %+v", s)
	}
}

func TestStateIsRateLimitedPerClient(t *testing.T) {
	mux, h, _, id := stateFixture(t)
	h.state.writes = ratelimit.New(2)
	path := "http://share.test/a/" + id + "/_state"
	for i, want := range []int{200, 200, 429} {
		if res := do(mux, stateReq("POST", path, "http://share.test", `{"items":{"a":1}}`)); res.Code != want {
			t.Fatalf("write %d: %d", i, res.Code)
		}
	}
	// Reads have their own meter.
	if res := do(mux, stateReq("GET", path, "", "")); res.Code != 200 {
		t.Fatalf("read after writes ran out: %d", res.Code)
	}
}
