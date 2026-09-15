// Package serve streams artifact versions to browsers with the comments runtime injected.
package serve

import (
	"bytes"
	"fmt"
)

func tag(artifactID string, version int) []byte {
	return []byte(fmt.Sprintf(`<script src="/_rt/comments.js" data-artifact="%s" data-version="%d" defer></script>`, artifactID, version))
}

// Inject places the runtime tag before the last </body> (any case) or appends it.
func Inject(html []byte, artifactID string, version int) []byte {
	lower := bytes.ToLower(html)
	i := bytes.LastIndex(lower, []byte("</body>"))
	t := tag(artifactID, version)
	if i < 0 {
		return append(append([]byte{}, html...), t...)
	}
	out := make([]byte, 0, len(html)+len(t))
	out = append(out, html[:i]...)
	out = append(out, t...)
	return append(out, html[i:]...)
}
