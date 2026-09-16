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
