package domains

import "testing"

func TestNormalizeHost(t *testing.T) {
	ok := map[string]string{
		"docs.example.com":            "docs.example.com",
		"  Docs.Example.COM. ":        "docs.example.com",
		"https://docs.example.com":    "docs.example.com",
		"https://docs.example.com/":   "docs.example.com",
		"example.co.uk":               "example.co.uk",
		"bücher.example":              "",
		"bücher.de":                   "xn--bcher-kva.de",
		"a-b.c-d.example.org":         "a-b.c-d.example.org",
		"deep.sub.domain.example.net": "deep.sub.domain.example.net",
	}
	for in, want := range ok {
		got, err := NormalizeHost(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: want an error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "localhost", "com", "co.uk", "github.io", "192.168.1.1", "[::1]",
		"docs.example.com:8443", "https://docs.example.com/path", "http://user@docs.example.com",
		"*.example.com", "-bad.example.com", "bad-.example.com", "a..example.com", "exa mple.com",
		"docs.example.com/x", "evil.localhost", "printer.local", "db.internal", "x.test",
		"a_b.example.com", "docs.example.com?x=1",
	} {
		if got, err := NormalizeHost(bad); err == nil {
			t.Errorf("%q: want an error, got %q", bad, got)
		}
	}
	long := ""
	for i := 0; i < 50; i++ {
		long += "abcde."
	}
	if _, err := NormalizeHost(long + "com"); err == nil {
		t.Error("a 300 character hostname was accepted")
	}
}

func TestReserved(t *testing.T) {
	own := []string{"byagent.dev", "app.byagent.dev", "domains.byagent.dev"}
	for _, h := range []string{"byagent.dev", "app.byagent.dev", "evil.byagent.dev", "a.b.byagent.dev"} {
		if !Reserved(h, own) {
			t.Errorf("%s should be reserved", h)
		}
	}
	for _, h := range []string{"notbyagent.dev", "byagent.dev.example.com", "docs.example.com"} {
		if Reserved(h, own) {
			t.Errorf("%s should not be reserved", h)
		}
	}
}

func TestApexAndRelative(t *testing.T) {
	if !IsApex("example.co.uk") || IsApex("docs.example.co.uk") {
		t.Fatal("apex detection")
	}
	if relativeName("_byagent.docs.example.com", "example.com") != "_byagent.docs" || relativeName("example.com", "example.com") != "@" {
		t.Fatal("relative names")
	}
}

func TestNormalizePath(t *testing.T) {
	ok := map[string]string{"": "", "/": "", " /docs/ ": "docs", "docs/api": "docs/api", "/v1.2/guide~x": "v1.2/guide~x"}
	for in, want := range ok {
		if got, err := NormalizePath(in); err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/_rt", "/docs/_unlock", "/../x", "/a//b", "/a/./b", "/a b", "/%2e%2e", "/a/b/c/d/e/f", "/ü"} {
		if got, err := NormalizePath(bad); err == nil {
			t.Errorf("%q: want an error, got %q", bad, got)
		}
	}
	if Base("") != "/" || Base("docs") != "/docs/" || DisplayPath("") != "/" {
		t.Fatal("base")
	}
}
