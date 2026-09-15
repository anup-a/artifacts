// Package runtime embeds the built comments runtime.
package runtime

import _ "embed"

//go:embed dist/comments.js
var CommentsJS []byte
