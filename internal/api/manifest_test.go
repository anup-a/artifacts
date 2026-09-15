package api

import (
	"encoding/json"
	"testing"

	"github.com/anup-a/artifacts/internal/config"
)

var lim = config.Limits{VersionBytes: 100, Files: 3}

func TestManifestOK(t *testing.T) {
	m, e := parseManifest([]byte(`{"title":"T","files":[{"path":"index.html","sha256":"`+sha64("a")+`","size":10},{"path":"css/a.css","sha256":"`+sha64("b")+`","size":5}]}`), lim)
	if e != nil {
		t.Fatal(e)
	}
	if m.Title != "T" || len(m.Files) != 2 {
		t.Fatalf("%+v", m)
	}
}

func TestManifestRejects(t *testing.T) {
	cases := map[string]string{
		"no index": `{"files":[{"path":"a.html","sha256":"` + sha64("a") + `","size":1}]}`,
		"dotdot":   `{"files":[{"path":"../index.html","sha256":"` + sha64("a") + `","size":1}]}`,
		"abs":      `{"files":[{"path":"/index.html","sha256":"` + sha64("a") + `","size":1}]}`,
		"bad sha":  `{"files":[{"path":"index.html","sha256":"XYZ","size":1}]}`,
		"too big":  `{"files":[{"path":"index.html","sha256":"` + sha64("a") + `","size":101}]}`,
		"too many": `{"files":[{"path":"index.html","sha256":"` + sha64("a") + `","size":1},{"path":"b","sha256":"` + sha64("b") + `","size":1},{"path":"c","sha256":"` + sha64("c") + `","size":1},{"path":"d","sha256":"` + sha64("d") + `","size":1}]}`,
		"dup path": `{"files":[{"path":"index.html","sha256":"` + sha64("a") + `","size":1},{"path":"index.html","sha256":"` + sha64("b") + `","size":1}]}`,
		"empty":    `{"files":[]}`,
		"not json": `{`,
	}
	for name, raw := range cases {
		if _, e := parseManifest([]byte(raw), lim); e == nil {
			t.Errorf("%s: expected error", name)
		} else if e.Status != 400 && e.Status != 413 {
			t.Errorf("%s: status %d", name, e.Status)
		}
	}
}

// TestManifestRejectsControlCharsInPath covers paths that look clean to
// path.Clean but carry a control character, e.g. a NUL used to confuse a
// downstream consumer about the real extension.
func TestManifestRejectsControlCharsInPath(t *testing.T) {
	for name, p := range map[string]string{
		"nul":     "index.html\x00.png",
		"newline": "index.html\n",
		"del":     "index\x7f.html",
	} {
		quoted, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		raw := `{"files":[{"path":"index.html","sha256":"` + sha64("a") + `","size":1},` +
			`{"path":` + string(quoted) + `,"sha256":"` + sha64("b") + `","size":1}]}`
		_, e := parseManifest([]byte(raw), lim)
		if e == nil {
			t.Errorf("%s (%q): expected an error", name, p)
		} else if e.Status != 400 {
			t.Errorf("%s: status %d, want 400", name, e.Status)
		}
	}
}

func TestContentType(t *testing.T) {
	for p, want := range map[string]string{"index.html": "text/html; charset=utf-8", "a.css": "text/css; charset=utf-8", "x.js": "text/javascript; charset=utf-8", "i.png": "image/png", "d.json": "application/json", "z.unknownext": "application/octet-stream"} {
		if got := contentTypeFor(p); got != want {
			t.Errorf("%s: %q", p, got)
		}
	}
}

// sha64 returns a 64-hex string derived from s (not a real hash; only format matters here).
func sha64(s string) string {
	out := []byte("0000000000000000000000000000000000000000000000000000000000000000")
	for i := range s {
		out[i] = s[i]
	}
	return string(out)
}
