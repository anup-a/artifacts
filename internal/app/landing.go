package app

import (
	"net/http"
	"net/url"
	"strings"
)

// landingData is the handful of facts the page cannot hardcode: where this
// deployment's dashboard and published links actually live.
type landingData struct {
	APIOrigin  string // what to pass to `byagent login --api`
	APIHost    string // the same without the scheme, for prose
	AppURL     string // dashboard front door
	KeysURL    string // where a key is created
	ShareHost  string // host published links are served from
	ExampleID  string // a plausible artifact id for the samples
	ExampleURL string // what a published link looks like here
	SkillURL   string // the agent skill this server serves
	SignIn     bool   // a sign-in provider is configured
}

// exampleID is a made-up artifact id, the same shape the server issues.
const exampleID = "k4m2Qv9pXz1A"

// originHost is the host part of a configured origin, for comparing against
// the Host header of a request.
func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return origin
	}
	return u.Host
}

// landing answers the bare root. It is the one page in this package written
// for the share origin — byagent.dev/ — where somebody arrives holding a link
// and wants to know what published it. The API origin's root stays the
// dashboard's front door, because that is the host a sign-in email lands on.
func (h *Handler) landing(w http.ResponseWriter, r *http.Request) {
	apiHost := originHost(h.Cfg.APIOrigin)
	if apiHost != originHost(h.Cfg.ShareOrigin) && strings.EqualFold(r.Host, apiHost) {
		http.Redirect(w, r, "/app/", http.StatusFound)
		return
	}
	// The page is the same for everyone, so it may sit in a shared cache;
	// this overrides the no-store the app headers set for signed-in pages.
	w.Header().Set("Cache-Control", "public, max-age=300")
	h.render(w, r, 200, "landing", "byagent", nil, landingData{
		APIOrigin:  h.Cfg.APIOrigin,
		APIHost:    apiHost,
		AppURL:     h.Cfg.APIOrigin + "/app/",
		KeysURL:    h.Cfg.APIOrigin + "/app/keys",
		ShareHost:  originHost(h.Cfg.ShareOrigin),
		ExampleID:  exampleID,
		ExampleURL: h.Cfg.ShareOrigin + "/a/" + exampleID + "/",
		SkillURL:   h.Cfg.ShareOrigin + "/skill.md",
		SignIn:     h.Cfg.WebEnabled(),
	})
}
