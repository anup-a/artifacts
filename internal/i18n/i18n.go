// Package i18n translates the dashboard and the public site.
//
// A message is its own English text: templates call {{t "Sign out"}}, Go
// calls i18n.T(lang, "Sign out"), and English needs no catalog at all. Each
// other language is one JSON file in locales/, from English text to its
// translation. A message a catalog lacks falls back to English, and
// TestCatalogsComplete fails until every language has it.
//
// Messages are fmt formats. A translation keeps the English verbs and may
// reorder them with explicit indexes ("%[2]s … %[1]d"). A message with a
// count is a pair, "%s page|%s pages", and a catalog answers it with a string
// when the language has one form, or {"one": …, "other": …} when it has two.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Default is the language of the source text.
const Default = "en"

// Locale is one language the site is offered in.
type Locale struct {
	Code string // BCP 47 tag, the value of ?lang= and <html lang>
	Name string // the language's name for itself, for the picker
}

// Locales are the languages offered, in the order the picker lists them.
var Locales = []Locale{
	{"en", "English"},
	{"zh-CN", "简体中文"},
	{"zh-TW", "繁體中文"},
	{"ja", "日本語"},
	{"ko", "한국어"},
	{"es", "Español"},
	{"pt-BR", "Português"},
	{"fr", "Français"},
	{"de", "Deutsch"},
}

// CookieName remembers a language picked with ?lang=.
const CookieName = "lang"

//go:embed locales/*.json
var localeFS embed.FS

// entry is one translation: other always, one only where the language
// says "1 page" differently from "2 pages".
type entry struct{ one, other string }

func (e *entry) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &e.other)
	}
	var forms struct{ One, Other string }
	if err := json.Unmarshal(b, &forms); err != nil {
		return err
	}
	e.one, e.other = forms.One, forms.Other
	return nil
}

// catalogs are the translations by locale code. English has none.
var catalogs = func() map[string]map[string]entry {
	out := map[string]map[string]entry{}
	for _, l := range Locales[1:] {
		b, err := localeFS.ReadFile("locales/" + l.Code + ".json")
		if err != nil {
			panic(err) // embedded, so only a build mistake
		}
		m := map[string]entry{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Errorf("locales/%s.json: %w", l.Code, err))
		}
		out[l.Code] = m
	}
	return out
}()

// T translates msg into lang and formats it with args.
func T(lang, msg string, args ...any) string {
	s := msg
	if e, ok := catalogs[lang][msg]; ok && e.other != "" {
		s = e.other
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// N translates the pair one|other for count n and formats it with n first,
// grouped the way lang writes numbers, then args: N(lang, 1234, "%s page",
// "%s pages") is "1,234 pages". The count is a string, so its verb is %s.
func N(lang string, n int, one, other string, args ...any) string {
	s := other
	if oneForm(lang, n) {
		s = one
	}
	if e, ok := catalogs[lang][one+"|"+other]; ok && e.other != "" {
		s = e.other
		if e.one != "" && oneForm(lang, n) {
			s = e.one
		}
	}
	return fmt.Sprintf(s, append([]any{Number(lang, int64(n))}, args...)...)
}

// oneForm reports whether n takes the singular in lang. Chinese, Japanese
// and Korean have one form; French and Portuguese count zero as singular.
func oneForm(lang string, n int) bool {
	switch lang {
	case "zh-CN", "zh-TW", "ja", "ko":
		return false
	case "fr", "pt-BR":
		return n == 0 || n == 1
	}
	return n == 1
}

// M marks a message for translation where it is declared, in a table that
// is translated later with T or a template's t. It returns msg unchanged.
func M(msg string) string { return msg }

// Match maps a language tag to the locale that serves it: "zh", "zh-Hans"
// and "zh-SG" to zh-CN, "zh-Hant" and "zh-HK" to zh-TW, "pt" to pt-BR, "es-MX"
// to es. ok is false when no locale speaks the language.
func Match(tag string) (code string, ok bool) {
	tag = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(tag, "_", "-")))
	if tag == "" {
		return "", false
	}
	base, rest, _ := strings.Cut(tag, "-")
	if base == "zh" {
		for _, sub := range strings.Split(rest, "-") {
			switch sub {
			case "hant", "tw", "hk", "mo":
				return "zh-TW", true
			}
		}
		return "zh-CN", true
	}
	for _, l := range Locales {
		if b, _, _ := strings.Cut(strings.ToLower(l.Code), "-"); b == base {
			return l.Code, true
		}
	}
	return "", false
}

// Negotiate picks the locale for an Accept-Language header: the first tag
// by quality that a locale serves, else English.
func Negotiate(header string) string {
	best, bestQ := Default, 0.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(part, ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if _, err := fmt.Sscanf(v, "%g", &q); err != nil {
				continue
			}
		}
		if code, ok := Match(tag); ok && q > bestQ {
			best, bestQ = code, q
		}
	}
	return best
}

// FromRequest is the language to answer r in: ?lang=, then the cookie an
// earlier ?lang= left, then Accept-Language. fromURL says the address
// itself chose it, so the response may be shared by a cache.
func FromRequest(r *http.Request) (lang string, fromURL bool) {
	if code, ok := Match(r.URL.Query().Get("lang")); ok {
		return code, true
	}
	if c, err := r.Cookie(CookieName); err == nil {
		if code, ok := Match(c.Value); ok {
			return code, false
		}
	}
	return Negotiate(r.Header.Get("Accept-Language")), false
}

// Lang is FromRequest without the source.
func Lang(r *http.Request) string {
	lang, _ := FromRequest(r)
	return lang
}

// Remember keeps a language chosen with ?lang= for later visits, so the
// links on the page need not carry it. It sets nothing otherwise.
func Remember(w http.ResponseWriter, r *http.Request) {
	code, ok := Match(r.URL.Query().Get("lang"))
	if !ok {
		return
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value == code {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: code, Path: "/", MaxAge: 365 * 24 * 3600,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
}

// URL is p in lang, for the language picker and hreflang links. English is
// the bare address, the one search engines already index.
func URL(p, lang string) string {
	if lang == Default {
		return p
	}
	return p + "?lang=" + lang
}

// Known reports whether code is one of Locales.
func Known(code string) bool {
	for _, l := range Locales {
		if l.Code == code {
			return true
		}
	}
	return false
}

// Messages returns lang's translations of msgs, for a script on the page:
// only the messages lang translates, with plural pairs as {"one", "other"}.
func Messages(lang string, msgs []string) map[string]any {
	out := map[string]any{}
	for _, m := range msgs {
		e, ok := catalogs[lang][m]
		if !ok {
			continue
		}
		if e.one != "" {
			out[m] = map[string]string{"one": e.one, "other": e.other}
		} else {
			out[m] = e.other
		}
	}
	return out
}
