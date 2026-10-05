package pageurl

import (
	"strings"
	"testing"

	"github.com/anup-a/artifacts/internal/config"
)

const (
	id   = "28BP97mgT4ix"
	slug = "q7mz4kd2xw5ntb3e"
)

// Without a content domain every address is the share origin's, exactly as
// before page hosts existed: dev, tests and a rollback all look like this.
func TestWithoutContentDomain(t *testing.T) {
	b := New(config.Config{ShareOrigin: "https://byagent.dev/"})
	for _, c := range [][2]string{
		{b.Page(id, slug), "https://byagent.dev/a/28BP97mgT4ix/"},
		{b.Version(id, slug, 3), "https://byagent.dev/a/28BP97mgT4ix/v/3/"},
		{b.Cover(id, slug), "https://byagent.dev/a/28BP97mgT4ix/_cover.svg"},
		{b.Share(id, slug, "123 456"), "https://byagent.dev/a/28BP97mgT4ix/?k=123456"},
		{b.Form(), "https://byagent.dev/a/<id>/"},
		{b.Source(), ""},
		{b.Origin(slug), ""},
	} {
		if got, want := c[0], c[1]; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if b.PerPage() {
		t.Fatal("PerPage without a content domain")
	}
	if k, _ := b.Classify(slug + ".byagent.page"); k != Elsewhere {
		t.Fatalf("no host is a page host without a content domain: %v", k)
	}
}

func TestWithContentDomain(t *testing.T) {
	b := New(config.Config{ShareOrigin: "https://byagent.dev", ContentScheme: "https", ContentDomain: "byagent.page"})
	for _, c := range [][2]string{
		{b.Page(id, slug), "https://q7mz4kd2xw5ntb3e.byagent.page/"},
		{b.Version(id, slug, 3), "https://q7mz4kd2xw5ntb3e.byagent.page/v/3/"},
		{b.Cover(id, slug), "https://q7mz4kd2xw5ntb3e.byagent.page/_cover.svg"},
		{b.Share(id, slug, "123-456"), "https://q7mz4kd2xw5ntb3e.byagent.page/?k=123456"},
		{b.Origin(slug), "https://q7mz4kd2xw5ntb3e.byagent.page"},
		{b.Form(), "https://<name>.byagent.page/"},
		{b.Source(), "https://*.byagent.page"},
		// A row with no slug yet keeps its share-origin address, which
		// redirects once it has one.
		{b.Page(id, ""), "https://byagent.dev/a/28BP97mgT4ix/"},
	} {
		if got, want := c[0], c[1]; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	local := New(config.Config{ShareOrigin: "http://localhost:8080", ContentScheme: "http", ContentDomain: "page.localhost:8080"})
	if got := local.Page(id, slug); got != "http://q7mz4kd2xw5ntb3e.page.localhost:8080/" {
		t.Fatalf("local: %q", got)
	}
	if got := local.Source(); got != "http://*.page.localhost:8080" {
		t.Fatalf("local source: %q", got)
	}
}

func TestClassify(t *testing.T) {
	b := New(config.Config{ShareOrigin: "https://byagent.dev", ContentScheme: "https", ContentDomain: "byagent.page"})
	for _, tc := range []struct {
		host string
		kind Kind
		slug string
	}{
		{"q7mz4kd2xw5ntb3e.byagent.page", Page, slug},
		{"Q7MZ4KD2XW5NTB3E.BYAGENT.PAGE.", Page, slug},
		{"q7mz4kd2xw5ntb3e.byagent.page:443", Page, slug},
		{"byagent.page", Apex, ""},
		{"www.byagent.page", Apex, ""},
		{"a.b.byagent.page", Stray, ""},
		{"under_score.byagent.page", Stray, ""},
		{"xn--nxasmq6b.byagent.page", Stray, ""},
		{"byagent.dev", Elsewhere, ""},
		{"app.byagent.dev", Elsewhere, ""},
		{"evilbyagent.page", Elsewhere, ""},
		{"127.0.0.1:8080", Elsewhere, ""},
	} {
		k, s := b.Classify(tc.host)
		if k != tc.kind || s != tc.slug {
			t.Errorf("%s: got %v %q, want %v %q", tc.host, k, s, tc.kind, tc.slug)
		}
	}
	local := New(config.Config{ShareOrigin: "http://localhost:8080", ContentScheme: "http", ContentDomain: "page.localhost:8080"})
	if k, s := local.Classify(slug + ".page.localhost:8080"); k != Page || s != slug {
		t.Fatalf("local: %v %q", k, s)
	}
	if k, _ := local.Classify("localhost:8080"); k != Elsewhere {
		t.Fatalf("the share host is not a page host: %v", k)
	}
}

// With the share host as the content domain, pages are {slug}.byagent.dev
// and nothing else under byagent.dev changes.
func TestClassifyShared(t *testing.T) {
	b := New(config.Config{ShareOrigin: "https://byagent.dev", ContentScheme: "https", ContentDomain: "byagent.dev", ContentShared: true})
	if got := b.Page("abc", slug); got != "https://"+slug+".byagent.dev/" {
		t.Fatalf("page: %s", got)
	}
	if got := b.Source(); got != "https://*.byagent.dev" {
		t.Fatalf("source: %s", got)
	}
	for _, tc := range []struct {
		host string
		kind Kind
	}{
		{slug + ".byagent.dev", Page},
		{strings.ToUpper(slug) + ".BYAGENT.DEV.", Page},
		{"byagent.dev", Elsewhere},
		{"www.byagent.dev", Elsewhere},
		{"app.byagent.dev", Elsewhere},
		{"domains.byagent.dev", Elsewhere},
		{"q7mz4kd2xw5ntb3.byagent.dev", Elsewhere},   // 15 characters
		{"q7mz4kd2xw5ntb3e1.byagent.dev", Elsewhere}, // 17
		{"q7mz4kd2xw5ntb3a.byagent.dev", Page},
		{"q7mz4kd2xw5ntb31.byagent.dev", Elsewhere}, // 1 is not base32
		{"x." + slug + ".byagent.dev", Elsewhere},
		{slug + ".app.byagent.dev", Elsewhere},
		{"127.0.0.1:8080", Elsewhere},
	} {
		if k, _ := b.Classify(tc.host); k != tc.kind {
			t.Errorf("%s: got %v, want %v", tc.host, k, tc.kind)
		}
	}
	for origin, want := range map[string]bool{
		"https://" + slug + ".byagent.dev": true, "https://app.byagent.dev": false, "https://byagent.dev": false,
		"null": false, "": false, "https://" + slug + ".evil.example": false,
	} {
		if got := b.IsPageOrigin(origin); got != want {
			t.Errorf("IsPageOrigin(%q) = %v", origin, got)
		}
	}
}
