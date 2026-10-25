package app

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anup-a/byagent/internal/serve"
)

func TestGateOnCustomDomain(t *testing.T) {
	e := fixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "https://docs.example.com/team/", nil)
	e.h.GateArtifactAccess(w, r, serve.GateView{ArtifactID: "aaaaaaaaaaaa", Action: "/team/_unlock", AssetOrigin: "http://share.test"})
	body := w.Body.String()
	for _, s := range []string{`action="/team/_unlock"`, `href="http://share.test/app/static/app.`, `src="http://share.test/app/static/app.`} {
		if !strings.Contains(body, s) {
			t.Fatalf("gate lacks %q:\n%s", s, body)
		}
	}
	w = httptest.NewRecorder()
	e.h.GateArtifactAccess(w, r, serve.GateView{ArtifactID: "aaaaaaaaaaaa"})
	if !strings.Contains(w.Body.String(), `action="/a/aaaaaaaaaaaa/_unlock"`) {
		t.Fatal("share-origin gate lost its action")
	}
}
