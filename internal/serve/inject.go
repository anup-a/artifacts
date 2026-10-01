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
	return insertInHead(doc, metaTags(title, coverURL))
}

// iconTag points a published page at the shelf mark. The path is absolute so
// it resolves the same from /a/<id>/ and from a nested page inside it.
var iconTag = []byte(`<link rel="icon" href="/favicon.svg">`)

// InjectIcon gives a published page a favicon, but only when it declares no
// icon of its own: the page belongs to whoever published it, so an author who
// shipped their own mark keeps it.
func InjectIcon(doc []byte) []byte {
	if hasIcon(doc) {
		return doc
	}
	return insertInHead(doc, iconTag)
}

// hasIcon reports whether the document's head already has a <link> tag
// mentioning an icon: rel="icon", "shortcut icon", "apple-touch-icon" and the
// unquoted spellings all contain the word.
func hasIcon(doc []byte) bool {
	head := bytes.ToLower(doc)
	if i := bytes.Index(head, []byte("</head")); i >= 0 {
		head = head[:i]
	}
	for {
		i := bytes.Index(head, []byte("<link"))
		if i < 0 {
			return false
		}
		head = head[i+len("<link"):]
		tag := head
		if end := bytes.IndexByte(tag, '>'); end >= 0 {
			tag = tag[:end]
		}
		if bytes.Contains(tag, []byte("icon")) {
			return true
		}
	}
}

// insertInHead puts chunk right after the opening <head> tag when there is
// one, otherwise where the runtime tag goes (before </body>, or appended).
func insertInHead(doc, chunk []byte) []byte {
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

// guestBadge marks a page published without an account. Anyone can make one,
// so a reader is told plainly that it is temporary and whose it is not: the
// badge is the difference between a guest page and a byagent page.
const guestBadge = `<a href="%s" target="_blank" rel="noopener" style="position:fixed;left:12px;bottom:12px;z-index:2147483646;` +
	`font:500 12px/1.2 system-ui,sans-serif;color:#fff;background:rgba(20,20,20,.86);padding:6px 10px;border-radius:999px;text-decoration:none">` +
	`Temporary guest page &middot; byagent</a>`

// InjectGuestBadge adds the guest badge before </body>.
func InjectGuestBadge(doc []byte, siteURL string) []byte {
	return insertBeforeBody(doc, []byte(fmt.Sprintf(guestBadge, html.EscapeString(siteURL+"/"))))
}
