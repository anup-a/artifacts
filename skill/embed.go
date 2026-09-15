// Package skill embeds the agent skill served at /skill.md.
package skill

import _ "embed"

//go:embed SKILL.md
var Markdown []byte
