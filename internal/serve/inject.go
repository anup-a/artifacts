// Package serve streams artifact versions to browsers with the comments runtime injected.
package serve

import (
	"bytes"
	"fmt"
	"html"
)

// tag builds the runtime script tag. rev, when set, is appended as a query
// string so that a redeployed runtime gets a fresh URL: /_rt/comments.js is
// served immutable for a year, and without a revision returning viewers would
// keep the old bundle until their cache expired.
func tag(artifactID string, version int, rev string) []byte {
	src := "/_rt/comments.js"
	if rev != "" {
		src += "?v=" + rev
	}
	return []byte(fmt.Sprintf(`<script src="%s" data-artifact="%s" data-version="%d" defer></script>`, src, artifactID, version))
}

// Inject places the runtime tag before the last </body> (any case) or appends it.
func Inject(html []byte, artifactID string, version int, rev string) []byte {
	return insertBeforeBody(html, tag(artifactID, version, rev))
}

// insertBeforeBody puts chunk before the last </body> (any case), or appends
// it when the document has no body close tag.
func insertBeforeBody(html, chunk []byte) []byte {
	lower := bytes.ToLower(html)
	i := bytes.LastIndex(lower, []byte("</body>"))
	if i < 0 {
		return append(append([]byte{}, html...), chunk...)
	}
	out := make([]byte, 0, len(html)+len(chunk))
	out = append(out, html[:i]...)
	out = append(out, chunk...)
	return append(out, html[i:]...)
}

// metaTags builds the Open Graph and Twitter card tags for a served page.
// coverURL is absolute so link unfurlers on other hosts can fetch it.
func metaTags(title, coverURL string) []byte {
	return []byte(`<meta property="og:title" content="` + html.EscapeString(title) + `">` +
		`<meta property="og:image" content="` + html.EscapeString(coverURL) + `">` +
		`<meta name="twitter:card" content="summary_large_image">`)
}

// InjectMeta adds og:title, og:image and twitter:card to a page: right after
// the opening <head> tag when there is one, otherwise where the runtime tag
// goes (before </body>, or appended).
func InjectMeta(doc []byte, title, coverURL string) []byte {
	chunk := metaTags(title, coverURL)
	lower := bytes.ToLower(doc)
	i := bytes.Index(lower, []byte("<head"))
	if i < 0 {
		return insertBeforeBody(doc, chunk)
	}
	end := bytes.IndexByte(doc[i:], '>')
	if end < 0 {
		return insertBeforeBody(doc, chunk)
	}
	at := i + end + 1
	out := make([]byte, 0, len(doc)+len(chunk))
	out = append(out, doc[:at]...)
	out = append(out, chunk...)
	return append(out, doc[at:]...)
}
