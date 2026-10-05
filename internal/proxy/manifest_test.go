package proxy

import (
	"strings"
	"testing"
)

const goodManifest = `{"routes": {
  "chat": {"upstream": "https://openrouter.ai/api/v1/chat/completions", "methods": ["POST"],
           "headers": {"Authorization": "Bearer {{OPENROUTER_API_KEY}}", "X-Title": "My page"}},
  "db": {"upstream": "https://xyz.supabase.co/rest/v1", "method": "get",
         "headers": {"apikey": "{{ SUPABASE_KEY }}"}, "query": {"key": "{{MAPS_KEY}}"},
         "paths": ["/todos", "/items/*"]}
}}`

func TestParseGood(t *testing.T) {
	m, err := Parse([]byte(goodManifest), "byagent.dev", "app.byagent.dev")
	if err != nil {
		t.Fatal(err)
	}
	chat := m.Routes["chat"]
	if chat.Upstream.Host != "openrouter.ai" || len(chat.Methods) != 1 || chat.Methods[0] != "POST" {
		t.Fatalf("chat: %+v", chat)
	}
	if got := chat.Vars(); len(got) != 1 || got[0] != "OPENROUTER_API_KEY" {
		t.Fatalf("chat vars: %v", got)
	}
	db := m.Routes["db"]
	if !db.Allows("GET") || db.Allows("POST") {
		t.Fatal("methods")
	}
	if got := db.Vars(); strings.Join(got, ",") != "MAPS_KEY,SUPABASE_KEY" {
		t.Fatalf("db vars: %v", got)
	}
	if got := Expand(db.Headers["Apikey"], map[string]string{"SUPABASE_KEY": "s$1ecret"}); got != "s$1ecret" {
		t.Fatalf("expand: %q", got)
	}
	for sub, want := range map[string]bool{"": true, "/todos": true, "/todos/1": false, "/items/9": true, "/items": false, "/items/": false, "/other": false} {
		if db.MatchPath(sub) != want {
			t.Errorf("MatchPath(%q) != %v", sub, want)
		}
	}
	if chat.MatchPath("/anything") {
		t.Error("a route without paths takes no sub-path")
	}
}

func TestParseRefusesHostileManifests(t *testing.T) {
	route := func(body string) string { return `{"routes":{"r":` + body + `}}` }
	cases := map[string]string{
		"not json":            `{`,
		"here.now shape":      `{"proxies":{"/api/chat":{"upstream":"https://a.example.com"}}}`,
		"no routes":           `{"routes":{}}`,
		"unknown top field":   `{"routes":{"r":{"upstream":"https://a.example.com","methods":["GET"]}},"extra":1}`,
		"unknown field":       route(`{"upstream":"https://a.example.com","methods":["GET"],"body":"x"}`),
		"bad route name":      `{"routes":{"../x":{"upstream":"https://a.example.com","methods":["GET"]}}}`,
		"upper route name":    `{"routes":{"Chat":{"upstream":"https://a.example.com","methods":["GET"]}}}`,
		"http upstream":       route(`{"upstream":"http://a.example.com","methods":["GET"]}`),
		"no upstream":         route(`{"methods":["GET"]}`),
		"userinfo":            route(`{"upstream":"https://u:p@a.example.com","methods":["GET"]}`),
		"other port":          route(`{"upstream":"https://a.example.com:8443","methods":["GET"]}`),
		"ip literal":          route(`{"upstream":"https://169.254.169.254/latest","methods":["GET"]}`),
		"ipv6 literal":        route(`{"upstream":"https://[::1]/","methods":["GET"]}`),
		"localhost":           route(`{"upstream":"https://localhost/","methods":["GET"]}`),
		"single label":        route(`{"upstream":"https://metadata/","methods":["GET"]}`),
		"back at byagent":     route(`{"upstream":"https://byagent.dev/a/x/","methods":["GET"]}`),
		"byagent subdomain":   route(`{"upstream":"https://app.byagent.dev/api/me","methods":["GET"]}`),
		"fragment":            route(`{"upstream":"https://a.example.com/#x","methods":["GET"]}`),
		"dot segment":         route(`{"upstream":"https://a.example.com/a/../b","methods":["GET"]}`),
		"secret in url":       route(`{"upstream":"https://a.example.com/?key={{K}}","methods":["GET"]}`),
		"no methods":          route(`{"upstream":"https://a.example.com"}`),
		"both method forms":   route(`{"upstream":"https://a.example.com","method":"GET","methods":["POST"]}`),
		"connect":             route(`{"upstream":"https://a.example.com","methods":["CONNECT"]}`),
		"head":                route(`{"upstream":"https://a.example.com","methods":["TRACE"]}`),
		"host header":         route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"Host":"internal"}}`),
		"cookie header":       route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"cookie":"a=b"}}`),
		"te header":           route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"Transfer-Encoding":"chunked"}}`),
		"forwarded header":    route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"X-Forwarded-For":"1.2.3.4"}}`),
		"loop header":         route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"X-Byagent-Proxy":"0"}}`),
		"bad header name":     route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"Bad Name":"x"}}`),
		"crlf in header":      route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"X-A":"a\r\nX-Evil: 1"}}`),
		"dollar syntax":       route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"Authorization":"Bearer ${KEY}"}}`),
		"bad var name":        route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"Authorization":"Bearer {{key}}"}}`),
		"unclosed template":   route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"Authorization":"Bearer {{KEY"}}`),
		"path traversal":      route(`{"upstream":"https://a.example.com","methods":["GET"],"paths":["/../admin"]}`),
		"path no slash":       route(`{"upstream":"https://a.example.com","methods":["GET"],"paths":["models"]}`),
		"path encoded":        route(`{"upstream":"https://a.example.com","methods":["GET"],"paths":["/a%2fb"]}`),
		"path double slash":   route(`{"upstream":"https://a.example.com","methods":["GET"],"paths":["/a//b"]}`),
		"path query":          route(`{"upstream":"https://a.example.com","methods":["GET"],"paths":["/a?x=1"]}`),
		"empty query name":    route(`{"upstream":"https://a.example.com","methods":["GET"],"query":{"":"x"}}`),
		"five variables":      route(`{"upstream":"https://a.example.com","methods":["GET"],"headers":{"A":"{{K1}}{{K2}}","B":"{{K3}}"},"query":{"c":"{{K4}}{{K5}}"}}`),
		"empty body rules":    route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{}}`),
		"unknown body rule":   route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{"strip":["x"]}}`),
		"negative max":        route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{"max":{"max_tokens":-1}}}`),
		"pin and max":         route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{"pin":{"n":1},"max":{"n":2}}}`),
		"empty allowed name":  route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{"allow_only":[""]}}`),
		"allow_only not list": route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{"allow_only":"messages"}}`),
		"variable in body":    route(`{"upstream":"https://a.example.com","methods":["POST"],"body":{"pin":{"key":"{{K}}"}}}`),
		"zero page limit":     `{"limit_per_hour":0,"routes":{"r":{"upstream":"https://a.example.com","methods":["GET"]}}}`,
		"huge page limit":     `{"limit_per_hour":100000000,"routes":{"r":{"upstream":"https://a.example.com","methods":["GET"]}}}`,
		"too large manifest":  `{"routes":{"r":{"upstream":"https://a.example.com","methods":["GET"],"headers":{"X":"` + strings.Repeat("a", MaxManifestBytes) + `"}}}}`,
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw), "byagent.dev", "app.byagent.dev"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseLimitAndBodyRules(t *testing.T) {
	m, err := Parse([]byte(`{"limit_per_hour":50,"routes":{"r":{"upstream":"https://a.example.com","methods":["POST"],
		"body":{"pin":{"model":"x/y"},"max":{"max_tokens":1024}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.LimitPerHour != 50 || m.Routes["r"].Body == nil || string(m.Routes["r"].Body.Pin["model"]) != `"x/y"` {
		t.Fatalf("%+v", m)
	}
	if _, err := Parse([]byte(`{"routes":{"r":{"upstream":"https://a.example.com","methods":["GET"],"headers":{"A":"{{K1}}{{K2}}"},"query":{"b":"{{K3}}{{K4}}{{K1}}"}}}}`)); err != nil {
		t.Fatalf("four variables, one used twice: %v", err)
	}
}

func TestCleanSub(t *testing.T) {
	for _, ok := range []string{"", "/models", "/v1/chat-x_y.z~", "/items/42"} {
		if _, good := CleanSub(ok); !good {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"/..", "/a/../b", "/./a", "//a", "/a/", "/a%2fb", "/a%2Fb", "/%2e%2e/x", "/a%5cb", "/a\\b", "/a%00", "models", "/a%20b", "/a%25", "/a;x=1", "/caf\u00e9", "/a b", "/a:b", "/a@b"} {
		if _, good := CleanSub(bad); good {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBodyRulesStrictMode(t *testing.T) {
	m, err := Parse([]byte(`{"routes":{"r":{"upstream":"https://a.example.com","methods":["POST"],
		"body":{"pin":{"model":"x/y"},"max":{"max_tokens":1024},"allow_only":["messages","stream"]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	strict := m.Routes["r"].Body
	if _, err := strict.ApplyBody([]byte(`{"model":"other","max_tokens":5,"messages":[],"stream":true}`)); err != nil {
		t.Fatalf("pinned, capped and listed fields: %v", err)
	}
	for _, extra := range []string{`{"messages":[],"n":50}`, `{"max_completion_tokens":100000}`} {
		if _, err := strict.ApplyBody([]byte(extra)); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%s: %v", extra, err)
		}
	}
	// Without allow_only, fields the rules do not name pass through.
	loose := &BodyRules{Max: map[string]float64{"max_tokens": 10}}
	out, err := loose.ApplyBody([]byte(`{"n":50}`))
	if err != nil || !strings.Contains(string(out), `"n":50`) {
		t.Fatalf("loose rules: %s %v", out, err)
	}
	// An empty allow_only allows only what is pinned or capped, and is a
	// valid rule set on its own.
	m, err = Parse([]byte(`{"routes":{"r":{"upstream":"https://a.example.com","methods":["POST"],"body":{"allow_only":[]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Routes["r"].Body.ApplyBody([]byte(`{"a":1}`)); err == nil {
		t.Fatal("empty allow_only let a field through")
	}
	if _, err := m.Routes["r"].Body.ApplyBody([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}
