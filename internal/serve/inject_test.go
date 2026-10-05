package serve

import (
	"strings"
	"testing"
)

func TestInjectBeforeBody(t *testing.T) {
	out := string(Inject([]byte("<html><body><p>x</p></BODY></html>"), "abc", 3, ""))
	want := `<script src="/_rt/comments.js" data-artifact="abc" data-version="3" defer></script></BODY>`
	if !strings.Contains(out, want) || strings.Count(out, "<script") != 1 {
		t.Fatal(out)
	}
}

func TestInjectAppendsWithoutBody(t *testing.T) {
	out := string(Inject([]byte("<p>no body tag</p>"), "abc", 1, ""))
	if !strings.HasSuffix(out, `data-version="1" defer></script>`) {
		t.Fatal(out)
	}
}

func TestInjectMetaIntoHead(t *testing.T) {
	out := string(InjectMeta([]byte(`<!doctype html><html><HEAD lang="en"><title>x</title></HEAD><body></body></html>`), "T & <U>", "http://s/a/id/_cover.svg"))
	want := `<HEAD lang="en"><meta property="og:title" content="T &amp; &lt;U&gt;"><meta property="og:image" content="http://s/a/id/_cover.svg"><meta name="twitter:card" content="summary_large_image"><title>x</title>`
	if !strings.Contains(out, want) {
		t.Fatal(out)
	}
}

func TestInjectMetaURLAndBase(t *testing.T) {
	out := string(InjectMetaURL([]byte(`<head></head><body></body>`), "T", "https://x.page.test/_cover.svg", `https://x.page.test/"`))
	if !strings.Contains(out, `<meta property="og:image" content="https://x.page.test/_cover.svg"><meta property="og:url" content="https://x.page.test/&#34;">`) {
		t.Fatal(out)
	}
	out = string(InjectBase([]byte("<body></body>"), "abc", 2, "r1", `/a"b/`))
	if !strings.Contains(out, `data-version="2" data-base="/a&#34;b/" defer></script></body>`) {
		t.Fatal(out)
	}
	out = string(InjectIconHref([]byte("<head></head>"), "https://byagent.dev/favicon.svg"))
	if !strings.Contains(out, `<link rel="icon" href="https://byagent.dev/favicon.svg">`) {
		t.Fatal(out)
	}
}

func TestInjectIconIntoHead(t *testing.T) {
	out := string(InjectIcon([]byte(`<!doctype html><html><HEAD><title>x</title></HEAD><body></body></html>`)))
	if !strings.Contains(out, `<HEAD><link rel="icon" href="/favicon.svg"><title>x</title>`) {
		t.Fatal(out)
	}
	// No head: it lands where the runtime tag goes.
	out = string(InjectIcon([]byte(`<p>bare</p>`)))
	if !strings.HasSuffix(out, `<link rel="icon" href="/favicon.svg">`) {
		t.Fatal(out)
	}
}

func TestInjectIconLeavesAnAuthorsOwnIcon(t *testing.T) {
	for _, doc := range []string{
		`<head><link rel="icon" href="/mine.png"></head>`,
		`<head><link rel='shortcut icon' href='/mine.ico'></head>`,
		`<head><link rel=icon href=/mine.svg></head>`,
		`<head><link rel="apple-touch-icon" href="/mine.png"></head>`,
	} {
		if out := string(InjectIcon([]byte(doc))); out != doc {
			t.Fatalf("overwrote the author's icon: %s", out)
		}
	}
	// A link that is not an icon does not count as one.
	styled := `<head><link rel="stylesheet" href="/a.css"></head>`
	if out := string(InjectIcon([]byte(styled))); !strings.Contains(out, `href="/favicon.svg"`) {
		t.Fatal(out)
	}
	// An icon declared after </head> is not the head's, so it does not count.
	late := `<head></head><body><link rel="icon" href="/late.png"></body>`
	if out := string(InjectIcon([]byte(late))); !strings.Contains(out, `href="/favicon.svg"`) {
		t.Fatal(out)
	}
}

func TestInjectMetaWithoutHeadGoesBeforeBodyClose(t *testing.T) {
	out := string(InjectMeta([]byte(`<p>hi</p></body>`), "T", "http://s/c.svg"))
	if !strings.HasSuffix(out, `content="summary_large_image"></body>`) || !strings.HasPrefix(out, "<p>hi</p><meta") {
		t.Fatal(out)
	}
	out = string(InjectMeta([]byte(`<p>bare</p>`), "T", "http://s/c.svg"))
	if !strings.HasPrefix(out, "<p>bare</p><meta property=\"og:title\"") {
		t.Fatal(out)
	}
	// Meta first, then the runtime tag, so the tag lands after the meta.
	out = string(Inject(InjectMeta([]byte(`<body></body>`), "T", "u"), "abc", 1, ""))
	if strings.Index(out, "og:title") > strings.Index(out, "<script") {
		t.Fatal(out)
	}
}

func TestInjectVersionsTheRuntimeURL(t *testing.T) {
	out := string(Inject([]byte("<body></body>"), "abc", 2, "0123abcd"))
	if !strings.Contains(out, `src="/_rt/comments.js?v=0123abcd"`) {
		t.Fatal(out)
	}
}
