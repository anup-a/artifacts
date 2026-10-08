package cover

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden covers in testdata")

var goldenCard = Card{
	ID:          "golden-seed",
	Title:       "Quarterly revenue report for the EMEA sales organisation, 2026",
	Project:     "finance",
	Host:        "byagent.dev",
	PublishedAt: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC),
}

func TestStylesAndPick(t *testing.T) {
	want := []string{"dots", "grid", "lines", "wash", "rings", "plain"}
	if got := Styles(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("styles %v", got)
	}
	s := Styles()
	s[0] = "x"
	if Styles()[0] != "dots" {
		t.Fatal("Styles must return a copy")
	}
	seen := map[string]bool{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n"} {
		st := PickStyle(id)
		if !Valid(st) {
			t.Fatalf("PickStyle(%q) = %q", id, st)
		}
		if PickStyle(id) != st {
			t.Fatal("PickStyle must be stable")
		}
		seen[st] = true
	}
	if len(seen) < 2 {
		t.Fatalf("PickStyle barely varies: %v", seen)
	}
}

func TestProjectColor(t *testing.T) {
	if ProjectColor("") != neutral {
		t.Fatal("no project must be neutral grey")
	}
	seen := map[string]bool{}
	for _, p := range []string{"finance", "ops", "design", "travel", "alpha", "beta", "gamma", "delta"} {
		c := ProjectColor(p)
		if c == neutral || c != ProjectColor(p) {
			t.Fatalf("%s: %s", p, c)
		}
		seen[c] = true
	}
	if len(seen) < 3 {
		t.Fatalf("palette barely varies: %v", seen)
	}
}

// TestGolden keeps every style byte-identical for a fixed card. Run with
// -update after an intentional change to the generators.
func TestGolden(t *testing.T) {
	for _, style := range Styles() {
		c := goldenCard
		c.Style = style
		got := Render(c)
		path := filepath.Join("testdata", style+".svg")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update)", style, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: output differs from golden; run with -update if intended", style)
		}
	}
}

func TestRenderIsDeterministicAndQuiet(t *testing.T) {
	for _, style := range Styles() {
		c := Card{ID: "some-id", Title: "Title", Project: "p", Host: "h.test", PublishedAt: goldenCard.PublishedAt, Style: style}
		a, b := Render(c), Render(c)
		if !bytes.Equal(a, b) {
			t.Fatalf("%s: two renders differ", style)
		}
		s := string(a)
		if !strings.HasPrefix(s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630" width="1200" height="630"`) || !strings.HasSuffix(s, "</svg>") {
			t.Fatalf("%s: not a 1200x630 svg: %.120s", style, s)
		}
		if strings.Contains(s, "<filter") || strings.Contains(s, "@import") || strings.Contains(s, "<image") {
			t.Fatalf("%s: must not use filters, imports or images", style)
		}
		for _, want := range []string{`fill="#fafafa"`, ">Title</text>", ">p</text>", ">h.test · 16 Sep 2026</text>", `fill="` + ProjectColor("p") + `"`} {
			if !strings.Contains(s, want) {
				t.Fatalf("%s: missing %s in %s", style, want, s)
			}
		}
	}
	base := Card{ID: "x", Title: "T"}
	pick := base
	pick.Style = PickStyle("x")
	for _, st := range []string{"nope", ""} {
		c := base
		c.Style = st
		if !bytes.Equal(Render(c), Render(pick)) {
			t.Fatalf("style %q must fall back to PickStyle", st)
		}
	}
	plain := Card{ID: "x", Title: "T", Style: "plain"}
	if s := string(Render(plain)); strings.Contains(s, "<pattern") || strings.Contains(s, "<circle") || strings.Contains(s, "Gradient") {
		t.Fatalf("plain must have no pattern: %s", s)
	}
}

// A cover hangs in a dashboard that has a dark theme, so a card drawn only in
// near-white is a lit slab on a dark page. The colours it can change carry a
// dark rule; the light values stay on the elements as attributes, so a
// renderer that ignores the stylesheet still gets the original card.
func TestCoverCarriesADarkScheme(t *testing.T) {
	for _, style := range Styles() {
		s := string(Render(Card{ID: "x", Title: "Title", Project: "p", Host: "h.test", Style: style}))
		if !strings.Contains(s, "prefers-color-scheme:dark") {
			t.Errorf("%s: no dark rule", style)
		}
		for _, want := range []string{
			`fill="#fafafa"`, // the light ground survives as the fallback
			darkGround,       // and the dark one is in the stylesheet
			darkForeground,
			darkMuted,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s: missing %q", style, want)
			}
		}
		// Every element the scheme recolours has to be reachable from it.
		if strings.Count(s, `class="bg"`) != 1 {
			t.Errorf("%s: want exactly one grounded rect", style)
		}
	}
	// The title and the footer follow the scheme; the project colour does not,
	// because it identifies the project rather than the page around it.
	s := string(Render(Card{ID: "x", Title: "Title", Project: "p", Style: "dots"}))
	if !strings.Contains(s, `class="tt"`) || !strings.Contains(s, `class="mu"`) {
		t.Errorf("title and muted text must carry their classes: %s", s)
	}
	if !strings.Contains(s, `fill="`+ProjectColor("p")+`"`) {
		t.Error("the project colour must stay literal")
	}
}

func TestNoProjectOmitsLineAndUsesGrey(t *testing.T) {
	s := string(Render(Card{ID: "x", Title: "T", Style: "dots"}))
	if strings.Count(s, "<text") != 1 || !strings.Contains(s, `fill="`+neutral+`"`) {
		t.Fatalf("%s", s)
	}
	// Host without date, date without host, neither.
	if s := string(Render(Card{ID: "x", Title: "T", Host: "h"})); !strings.Contains(s, ">h</text>") {
		t.Fatal(s)
	}
	if s := string(Render(Card{ID: "x", Title: "T", PublishedAt: goldenCard.PublishedAt})); !strings.Contains(s, ">16 Sep 2026</text>") {
		t.Fatal(s)
	}
}

func TestTitleAndProjectAreEscapedText(t *testing.T) {
	out := string(Render(Card{ID: "id", Title: `<script>alert("x")</script> & "co"`, Project: "<b>p</b>", Style: "grid"}))
	if strings.Contains(out, "<script") || strings.Contains(out, "<b>") || strings.Contains(out, `"x"`) {
		t.Fatalf("markup leaked: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; &#34;co&#34;") || !strings.Contains(out, "&lt;b&gt;p&lt;/b&gt;") {
		t.Fatalf("not escaped as text: %s", out)
	}
}

func TestEmptyTitleShowsID(t *testing.T) {
	out := string(Render(Card{ID: "abc123abc123", Title: "   ", Style: "rings"}))
	if !strings.Contains(out, ">abc123abc123</text>") {
		t.Fatalf("id not shown: %s", out)
	}
}

func TestClampTitle(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Short", []string{"Short"}},
		{"exactly twenty-six chars!", []string{"exactly twenty-six chars!"}},
		{"a title that wraps onto a second line neatly", []string{"a title that wraps onto a", "second line neatly"}},
		{"a very long title that keeps going and going and going and going past three lines", []string{"a very long title that", "keeps going and going and", "going and going past thre…"}},
		{strings.Repeat("x", 90), []string{strings.Repeat("x", 26), strings.Repeat("x", 26), strings.Repeat("x", 25) + "…"}},
		{"ünïcödé wörds are counted by rune not byte", []string{"ünïcödé wörds are counted", "by rune not byte"}},
		// Chinese wraps between characters, 16 to a line at 1.6 Latin widths each.
		{"第三季度各地区销售收入报告与明年预算规划", []string{"第三季度各地区销售收入报告与明年", "预算规划"}},
		{"Q3 销售报告", []string{"Q3 销售报告"}},
		// The comma after the 16th character goes with it, not to the next line.
		{"一二三四五六七八九十一二三四五六，七八", []string{"一二三四五六七八九十一二三四五", "六，七八"}},
		{"「byagent」是什么", []string{"「byagent」是什么"}},
		{strings.Repeat("长", 60), []string{strings.Repeat("长", 16), strings.Repeat("长", 16), strings.Repeat("长", 15) + "…"}},
	}
	for _, c := range cases {
		got := clampTitle(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
		if len(got) > maxLines {
			t.Errorf("%q: %d lines", c.in, len(got))
		}
		for _, line := range got {
			if fit(line, maxLineChars*narrowWidth) != line {
				t.Errorf("%q: line %q is wider than a line", c.in, line)
			}
		}
	}
}

// TestDump writes one cover per style to COVER_DUMP_DIR for eyeballing.
func TestDump(t *testing.T) {
	dir := os.Getenv("COVER_DUMP_DIR")
	if dir == "" {
		t.Skip("set COVER_DUMP_DIR to write sample covers")
	}
	for _, style := range Styles() {
		c := Card{ID: "demo-" + style, Title: "Quarterly revenue report for EMEA sales, " + style, Project: "finance", Host: "byagent.dev", PublishedAt: goldenCard.PublishedAt, Style: style}
		if err := os.WriteFile(filepath.Join(dir, "gen-"+style+".svg"), Render(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := Card{ID: "demo-noproject", Title: "Untitled scratch page", Host: "byagent.dev", PublishedAt: goldenCard.PublishedAt, Style: "dots"}
	if err := os.WriteFile(filepath.Join(dir, "gen-noproject.svg"), Render(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkRender(b *testing.B) {
	styles := Styles()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := goldenCard
		c.Style = styles[i%len(styles)]
		Render(c)
	}
}
