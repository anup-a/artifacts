// Package serve streams artifact versions to browsers with the comments runtime injected.
package serve

import (
	"bytes"
	"fmt"
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
	lower := bytes.ToLower(html)
	i := bytes.LastIndex(lower, []byte("</body>"))
	t := tag(artifactID, version, rev)
	if i < 0 {
		return append(append([]byte{}, html...), t...)
	}
	out := make([]byte, 0, len(html)+len(t))
	out = append(out, html[:i]...)
	out = append(out, t...)
	return append(out, html[i:]...)
}
