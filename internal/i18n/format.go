package i18n

import (
	"strconv"
	"strings"
	"time"
)

// Ago renders t against now as "just now", "5m ago", "2h ago", "3d ago" or,
// beyond a month, the date. Future times read "in …".
func Ago(lang string, t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	future := d < 0
	if future {
		d = -d
	}
	switch {
	case d < time.Minute:
		return T(lang, "just now")
	case d < time.Hour && future:
		return T(lang, "in %dm", int(d/time.Minute))
	case d < time.Hour:
		return T(lang, "%dm ago", int(d/time.Minute))
	case d < 24*time.Hour && future:
		return T(lang, "in %dh", int(d/time.Hour))
	case d < 24*time.Hour:
		return T(lang, "%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour && future:
		return T(lang, "in %dd", int(d/(24*time.Hour)))
	case d < 30*24*time.Hour:
		return T(lang, "%dd ago", int(d/(24*time.Hour)))
	}
	return Date(lang, t)
}

// months are the short month names, January first, for the languages that
// write the month as a word.
var months = map[string][12]string{
	"en":    {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
	"es":    {"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sept", "oct", "nov", "dic"},
	"pt-BR": {"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"},
	"fr":    {"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."},
	"de":    {"Jan.", "Feb.", "März", "Apr.", "Mai", "Juni", "Juli", "Aug.", "Sept.", "Okt.", "Nov.", "Dez."},
}

// Date is t's calendar day in UTC the way lang writes it: "Jan 2, 2006",
// "2006年1月2日", "2 ene 2006".
func Date(lang string, t time.Time) string {
	t = t.UTC()
	y, m, d := t.Date()
	ys, ds, ms := strconv.Itoa(y), strconv.Itoa(d), strconv.Itoa(int(m))
	switch lang {
	case "zh-CN", "zh-TW", "ja":
		return ys + "年" + ms + "月" + ds + "日"
	case "ko":
		return ys + "년 " + ms + "월 " + ds + "일"
	case "es", "pt-BR", "fr":
		return ds + " " + months[lang][m-1] + " " + ys
	case "de":
		return ds + ". " + months[lang][m-1] + " " + ys
	}
	return months["en"][m-1] + " " + ds + ", " + ys
}

// Day is t's month and day in UTC, without the year: "Sep 16", "9月16日".
func Day(lang string, t time.Time) string {
	t = t.UTC()
	_, m, d := t.Date()
	ds, ms := strconv.Itoa(d), strconv.Itoa(int(m))
	switch lang {
	case "zh-CN", "zh-TW", "ja":
		return ms + "月" + ds + "日"
	case "ko":
		return ms + "월 " + ds + "일"
	case "es", "pt-BR", "fr":
		return ds + " " + months[lang][m-1]
	case "de":
		return ds + ". " + months[lang][m-1]
	}
	return months["en"][m-1] + " " + ds
}

// Number groups the thousands of n the way lang does: 1,234,567 in
// English and the CJK languages, 1.234.567 in German, Spanish and
// Portuguese, and 1 234 567 (narrow no-break spaces) in French.
func Number(lang string, n int64) string {
	sep := ","
	switch lang {
	case "de", "es", "pt-BR":
		sep = "."
	case "fr":
		sep = " "
	}
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteRune(c)
	}
	return neg + b.String()
}
