package i18n

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The extractors find every message the source asks to translate, so a test
// can hold each catalog to them and the server can hand a page's scripts
// just the messages they use. A plural pair comes back as "one|other".

var (
	tmplAction = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)
	tmplLit    = "(\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`)"
	tmplT      = regexp.MustCompile(`(?:^|[\s(|])th?\s+` + tmplLit)
	tmplN      = regexp.MustCompile(`(?:^|[\s(|])tn\s+(?:\([^)]*\)|[^\s()]+)\s+` + tmplLit + `\s+` + tmplLit)
)

// FromTemplate returns the messages an html/template file passes to t, th
// and tn.
func FromTemplate(src string) []string {
	var out []string
	for _, a := range tmplAction.FindAllStringSubmatch(src, -1) {
		body := a[1]
		if strings.HasPrefix(strings.TrimLeft(body, "- "), "/*") {
			continue
		}
		for _, m := range tmplT.FindAllStringSubmatch(body, -1) {
			if s, err := strconv.Unquote(m[1]); err == nil {
				out = append(out, s)
			}
		}
		for _, m := range tmplN.FindAllStringSubmatch(body, -1) {
			one, err1 := strconv.Unquote(m[1])
			other, err2 := strconv.Unquote(m[2])
			if err1 == nil && err2 == nil {
				out = append(out, one+"|"+other)
			}
		}
	}
	return out
}

// goFuncs are the Go calls that take a message, by name: how many string
// literal arguments make up the message (two for a plural pair).
var goFuncs = map[string]int{"T": 1, "M": 1, "N": 2, "tr": 1, "trn": 2}

// FromGo returns the messages a Go file passes to T, M, N (from this
// package or any other) and to the app's tr and trn helpers.
func FromGo(filename, src string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		}
		want, ok := goFuncs[name]
		if !ok {
			return true
		}
		var lits []string
		for _, a := range call.Args {
			if b, ok := a.(*ast.BasicLit); ok && b.Kind == token.STRING {
				if s, err := strconv.Unquote(b.Value); err == nil {
					lits = append(lits, s)
				}
			}
		}
		if len(lits) >= want {
			out = append(out, strings.Join(lits[:want], "|"))
		}
		return true
	})
	return out, nil
}

var (
	jsLit = `("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`
	jsT   = regexp.MustCompile(`\bt\(\s*` + jsLit)
	jsN   = regexp.MustCompile(`\btn\(\s*[^,()]+,\s*` + jsLit + `\s*,\s*` + jsLit)
)

// FromJS returns the messages a script passes to t("…") and tn(n, "…", "…").
func FromJS(src string) []string {
	var out []string
	for _, m := range jsT.FindAllStringSubmatch(src, -1) {
		out = append(out, jsUnquote(m[1]))
	}
	for _, m := range jsN.FindAllStringSubmatch(src, -1) {
		out = append(out, jsUnquote(m[1])+"|"+jsUnquote(m[2]))
	}
	return out
}

func jsUnquote(lit string) string {
	if lit[0] == '\'' {
		lit = `"` + strings.ReplaceAll(strings.ReplaceAll(lit[1:len(lit)-1], `\'`, `'`), `"`, `\"`) + `"`
	}
	var s string
	if err := json.Unmarshal([]byte(lit), &s); err != nil {
		return lit[1 : len(lit)-1]
	}
	return s
}

var verb = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*\d*(?:\.\d+)?([a-zA-Z%])`)

// Verbs lists a format's verbs in order, without explicit indexes, so a
// translation can be checked against its English: "%[2]s %[1]d" gives s, d.
func Verbs(format string) []string {
	var out []string
	for _, m := range verb.FindAllStringSubmatch(format, -1) {
		if m[1] != "%" {
			out = append(out, m[1])
		}
	}
	return out
}

// SourceDirs are the directories, relative to the repository root, whose
// templates, Go files and scripts are searched for messages.
var SourceDirs = []string{"internal/app", "internal/app/templates", "internal/app/templates/partials", "internal/app/static", "internal/auth", "internal/i18n", "internal/serve"}

// Collect returns every message under root's SourceDirs, each with the
// first file that uses it. Tests are skipped.
func Collect(root string) (map[string]string, error) {
	out := map[string]string{}
	add := func(file string, msgs []string) {
		for _, m := range msgs {
			if _, ok := out[m]; !ok {
				out[m] = file
			}
		}
	}
	for _, dir := range SourceDirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := filepath.Join(dir, name)
			b, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				return nil, err
			}
			switch filepath.Ext(name) {
			case ".html":
				add(rel, FromTemplate(string(b)))
			case ".js":
				add(rel, FromJS(string(b)))
			case ".go":
				msgs, err := FromGo(rel, string(b))
				if err != nil {
					return nil, err
				}
				add(rel, msgs)
			}
		}
	}
	return out, nil
}

// Missing lists the messages lang has no translation for, sorted.
func Missing(lang string, msgs map[string]string) []string {
	var out []string
	for m := range msgs {
		if e, ok := catalogs[lang][m]; !ok || e.other == "" {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
