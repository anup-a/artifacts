package serve

import (
	"strings"
	"testing"
)

func TestInjectBeforeBody(t *testing.T) {
	out := string(Inject([]byte("<html><body><p>x</p></BODY></html>"), "abc", 3))
	want := `<script src="/_rt/comments.js" data-artifact="abc" data-version="3" defer></script></BODY>`
	if !strings.Contains(out, want) || strings.Count(out, "<script") != 1 {
		t.Fatal(out)
	}
}

func TestInjectAppendsWithoutBody(t *testing.T) {
	out := string(Inject([]byte("<p>no body tag</p>"), "abc", 1))
	if !strings.HasSuffix(out, `data-version="1" defer></script>`) {
		t.Fatal(out)
	}
}
