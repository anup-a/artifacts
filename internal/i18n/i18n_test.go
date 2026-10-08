package i18n

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMatch(t *testing.T) {
	for tag, want := range map[string]string{
		"zh": "zh-CN", "zh-Hans": "zh-CN", "zh-SG": "zh-CN", "zh_CN": "zh-CN", "zh-Hans-CN": "zh-CN",
		"zh-Hant": "zh-TW", "zh-HK": "zh-TW", "zh-Hant-TW": "zh-TW", "zh-TW": "zh-TW",
		"pt": "pt-BR", "pt-PT": "pt-BR", "es-MX": "es", "EN-gb": "en", "ja-JP": "ja", "de-AT": "de",
	} {
		if got, ok := Match(tag); !ok || got != want {
			t.Errorf("Match(%q) = %q, %v; want %q", tag, got, ok, want)
		}
	}
	for _, tag := range []string{"", "xx", "*", "ru"} {
		if got, ok := Match(tag); ok {
			t.Errorf("Match(%q) = %q, want no match", tag, got)
		}
	}
}

func TestNegotiate(t *testing.T) {
	for header, want := range map[string]string{
		"":                                 "en",
		"zh-CN,zh;q=0.9,en;q=0.8":          "zh-CN",
		"ru-RU,ru;q=0.9,ja;q=0.7,en;q=0.5": "ja",
		"en;q=0.5, zh-TW;q=0.9":            "zh-TW",
		"ru":                               "en",
		"fr-CH, fr;q=0.9, *;q=0.5":         "fr",
	} {
		if got := Negotiate(header); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/?lang=zh-Hant", nil)
	r.Header.Set("Accept-Language", "ja")
	if lang, fromURL := FromRequest(r); lang != "zh-TW" || !fromURL {
		t.Errorf("?lang= gave %q, %v", lang, fromURL)
	}
	w := httptest.NewRecorder()
	Remember(w, r)
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Value != "zh-TW" {
		t.Fatalf("cookie = %v", c)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "ja")
	r.AddCookie(c[0])
	if lang, fromURL := FromRequest(r); lang != "zh-TW" || fromURL {
		t.Errorf("cookie gave %q, %v", lang, fromURL)
	}
	r = httptest.NewRequest("GET", "/?lang=nope", nil)
	r.Header.Set("Accept-Language", "ko-KR")
	if lang := Lang(r); lang != "ko" {
		t.Errorf("Accept-Language gave %q", lang)
	}
	w = httptest.NewRecorder()
	Remember(w, r)
	if len(w.Result().Cookies()) != 0 {
		t.Error("an unknown ?lang= set a cookie")
	}
}

func TestPlural(t *testing.T) {
	catalogs["xx-test"] = map[string]entry{"%s page|%s pages": {one: "%s Seite", other: "%s Seiten"}}
	defer delete(catalogs, "xx-test")
	if got := N("en", 1, "%s page", "%s pages"); got != "1 page" {
		t.Errorf("en 1 = %q", got)
	}
	if got := N("en", 0, "%s page", "%s pages"); got != "0 pages" {
		t.Errorf("en 0 = %q", got)
	}
	if got := N("fr", 0, "%s page", "%s pages"); got != "0 page" {
		t.Errorf("fr 0 = %q", got)
	}
	if got := N("ja", 1, "%s page", "%s pages"); got != "1 pages" {
		t.Errorf("ja 1 without a catalog entry = %q, want the English other form", got)
	}
	if got := N("xx-test", 1, "%s page", "%s pages"); got != "1 Seite" {
		t.Errorf("one = %q", got)
	}
	if got := N("xx-test", 4000, "%s page", "%s pages"); got != "4,000 Seiten" {
		t.Errorf("other = %q", got)
	}
}

func TestFormats(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if got := Ago("en", now.Add(-5*time.Minute), now); got != "5m ago" {
		t.Errorf("Ago = %q", got)
	}
	if got := Ago("en", now.Add(3*time.Hour), now); got != "in 3h" {
		t.Errorf("Ago future = %q", got)
	}
	for lang, want := range map[string]string{"en": "Sep 16, 2026", "zh-CN": "2026年9月16日", "ko": "2026년 9월 16일", "de": "16. Sept. 2026", "es": "16 sept 2026"} {
		if got := Date(lang, time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)); got != want {
			t.Errorf("Date(%s) = %q, want %q", lang, got, want)
		}
	}
	for lang, want := range map[string]string{"en": "1,234,567", "de": "1.234.567", "fr": "1 234 567", "ja": "-1,234,567"} {
		n := int64(1234567)
		if lang == "ja" {
			n = -n
		}
		if got := Number(lang, n); got != want {
			t.Errorf("Number(%s) = %q, want %q", lang, got, want)
		}
	}
}

func TestExtract(t *testing.T) {
	tmpl := `<a title="{{t "Sign out"}}">{{th ` + "`Read <a href=\"%s\">the guide</a>`" + ` .URL}}</a>
{{/* {{t "not a message"}} */}}
{{if .X}}{{tn .Count "%d page" "%d pages"}}{{end}} {{tn (len .Items) "%d item" "%d items"}}
{{template "x" (t "In a pipeline")}} {{.Data.t}} {{ .Item | t }}`
	got := FromTemplate(tmpl)
	want := []string{"Sign out", `Read <a href="%s">the guide</a>`, "%d page|%d pages", "%d item|%d items", "In a pipeline"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("FromTemplate = %q\nwant %q", got, want)
	}

	goSrc := `package x
var a = i18n.M("Marked")
func f() { tr(r, "Hello %s", name); i18n.N(lang, n, "%d view", "%d views"); trn(r, n, "%d a", "%d b"); T(lang, msg) }`
	got, err := FromGo("x.go", goSrc)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"Marked", "Hello %s", "%d view|%d views", "%d a|%d b"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("FromGo = %q\nwant %q", got, want)
	}

	js := `label.textContent = t("Copied"); x = t('It\'s "here"'); y = tn(hidden, "+%d more", "+%d more"); split("a")`
	got = FromJS(js)
	want = []string{"Copied", `It's "here"`, "+%d more|+%d more"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("FromJS = %q\nwant %q", got, want)
	}
}

// TestCatalogsComplete holds every catalog to the messages the source uses:
// each has a translation that keeps the English format verbs, and none
// carries a message the source no longer uses.
//
// To see what a language lacks: go run ./internal/i18n/cmd/i18n zh-CN
func TestCatalogsComplete(t *testing.T) {
	msgs, err := Collect("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 50 {
		t.Fatalf("found only %d messages; is the extractor broken?", len(msgs))
	}
	for _, l := range Locales[1:] {
		if missing := Missing(l.Code, msgs); len(missing) > 0 {
			show := missing
			if len(show) > 10 {
				show = show[:10]
			}
			t.Errorf("%s lacks %d messages, first %q", l.Code, len(missing), show)
		}
		for m, e := range catalogs[l.Code] {
			if _, ok := msgs[m]; !ok {
				t.Errorf("%s translates %q, which nothing uses", l.Code, m)
				continue
			}
			english := strings.ReplaceAll(m, "|", " ")
			for _, form := range []string{e.one, e.other} {
				if form == "" {
					continue
				}
				a, b := Verbs(english), Verbs(form)
				if strings.Contains(m, "|") {
					// A plural pair has the count once per form.
					a = Verbs(m[strings.Index(m, "|")+1:])
				}
				slices.Sort(a)
				slices.Sort(b)
				if !slices.Equal(a, b) {
					t.Errorf("%s %q: %q has verbs %v, want %v", l.Code, m, form, b, a)
				}
			}
		}
	}
}
