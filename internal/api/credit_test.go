package api

import (
	"strings"
	"testing"
)

// A public page links back to byagent from the served HTML, on its root
// page only. (Guest pages carry the guest badge instead: guest_test.go.)
func TestPublicRootPageCarriesTheCredit(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	files := []pubFile{{"index.html", "<html><body><p>hi</p></body></html>"}, {"more.html", "<body>more</body>"}}
	code, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "Report", files, nil))
	if code != 201 {
		t.Fatalf("publish: %d %v", code, b)
	}
	root := srv.URL + "/a/" + b["id"].(string) + "/"
	page := pageBody(t, root)
	if !strings.Contains(page, ">Published with byagent</a></body>") || !strings.Contains(page, `href="http://share.test/"`) {
		t.Fatalf("no credit before </body>: %s", page)
	}
	if strings.Contains(pageBody(t, root+"more.html"), "Published with byagent") {
		t.Error("the credit is on a page under the root")
	}

}
