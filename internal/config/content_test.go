package config

import "testing"

func TestContentDomain(t *testing.T) {
	base := map[string]string{"ADMIN_TOKEN": "x", "PUBLIC_SHARE_ORIGIN": "https://byagent.dev", "PUBLIC_API_ORIGIN": "https://app.byagent.dev"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	c, err := FromEnv(env(base))
	if err != nil || c.ContentDomain != "" || c.ContentScheme != "" {
		t.Fatalf("unset must stay off: %v %q %q", err, c.ContentDomain, c.ContentScheme)
	}
	for raw, want := range map[string][2]string{
		"byagent.page":                   {"https", "byagent.page"},
		" BYAGENT.PAGE. ":                {"https", "byagent.page"},
		"https://byagent.page/":          {"https", "byagent.page"},
		"http://page.localhost:8080":     {"http", "page.localhost:8080"},
		"page.localhost:8080":            {"https", "page.localhost:8080"},
		"https://pages.example.com:8443": {"https", "pages.example.com:8443"},
	} {
		c, err := FromEnv(env(with(map[string]string{"CONTENT_DOMAIN": raw})))
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if c.ContentScheme != want[0] || c.ContentDomain != want[1] {
			t.Fatalf("%q: got %q %q, want %v", raw, c.ContentScheme, c.ContentDomain, want)
		}
	}
	// A bare domain takes the share origin's scheme.
	local, err := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "CONTENT_DOMAIN": "page.localhost:8080"}))
	if err != nil || local.ContentScheme != "http" {
		t.Fatalf("local: %v %q", err, local.ContentScheme)
	}
	// The share host itself: pages at {slug}.byagent.dev.
	for _, raw := range []string{"byagent.dev", "https://byagent.dev", "BYAGENT.DEV."} {
		c, err := FromEnv(env(with(map[string]string{"CONTENT_DOMAIN": raw})))
		if err != nil || c.ContentDomain != "byagent.dev" || c.ContentScheme != "https" || !c.ContentShared {
			t.Fatalf("%q: %v %q %v", raw, err, c.ContentDomain, c.ContentShared)
		}
	}
	if c, _ := FromEnv(env(with(map[string]string{"CONTENT_DOMAIN": "byagent.page"}))); c.ContentShared {
		t.Fatal("a separate content domain is not shared")
	}
	for _, bad := range []string{
		"app.byagent.dev",       // the API host: pages beside its cookies
		"dev",                   // byagent.dev and app.byagent.dev sit under it
		"pages.byagent.dev",     // under the share host: could set its cookies
		"pages.app.byagent.dev", // under the API host
		"https://byagent.page/a/", "ftp://byagent.page", "user@byagent.page", "by_agent.page", "byagent.page?x=1",
	} {
		if _, err := FromEnv(env(with(map[string]string{"CONTENT_DOMAIN": bad}))); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
