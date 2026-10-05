package agents

import (
	"encoding/json"
	"net/http"
)

// facts is /agents/facts.json: the entrypoint as one object, for an agent
// that would rather parse than read. Limits come from the running server's
// config, so they are true for this deployment rather than copied prose.
func (h *Handler) facts(w http.ResponseWriter, r *http.Request) {
	d := h.d
	type link struct {
		URL    string `json:"url"`
		Format string `json:"format"`
		What   string `json:"what"`
	}
	type task struct {
		Title   string `json:"title"`
		What    string `json:"what"`
		Command string `json:"command"`
		Doc     string `json:"doc"`
	}
	var entry []link
	for _, e := range d.Entrypoints {
		entry = append(entry, link{d.URL(e.Path), e.Format, e.What})
	}
	var ts []task
	for _, t := range d.Tasks {
		ts = append(ts, task{t.Title, t.Summary, t.Command, d.Share + "/agents/tasks/" + t.Slug + ".md"})
	}
	body, err := json.MarshalIndent(map[string]any{
		"name":        "byagent",
		"url":         d.Share + "/",
		"description": "A CLI that publishes what AI agents write (Markdown or HTML) as shareable links that readers can comment on, and reads those comments back.",
		"interface": map[string]any{
			"kind":    "cli",
			"package": "byagent",
			"install": "npm install -g byagent",
			"run":     "npx byagent <command>",
			"output":  "Every command takes --json and prints exactly one JSON object.",
		},
		"auth": map[string]any{
			"docs":         "none: every page under /agents, /skill.md and /llms.txt is public and read-only",
			"read_page":    "none for a public page; a private page needs a signed-in workspace member",
			"comment":      "none for readers of a public page with comments on",
			"publish":      "API key. An agent gets its own with byagent login --code (a one-time code from " + d.API + "/app/connect) or byagent login --browser (a signed-in person approves it); keys for CI are created at " + d.API + "/app/keys",
			"guest":        "byagent publish with nothing configured gets a 24-hour guest key: public pages only, three at most",
			"key_over_api": "only by redeeming a one-time connect code a signed-in person made or approved; never from a password or by asking",
		},
		"capabilities": []string{
			"publish a Markdown file or an HTML directory to a link",
			"republish to the same link, with versions and rollback",
			"public or private pages, and share codes for private ones",
			"line-anchored comments from readers, read back over the CLI",
			"reply to and resolve comment threads",
			"collections: pages grouped by project label",
			"project briefs: BRIEF.md pushed and read back with its comments",
		},
		"not": []string{
			"not a hosting platform for apps with a backend: pages are static",
			"not a search engine, ranking or directory of other sites",
			"not a marketplace",
			"not an AI search optimization or agent discovery service",
			"keys are never created over the API",
		},
		"limits": map[string]any{
			"bytes_per_version":       d.Limits.VersionBytes,
			"files_per_version":       d.Limits.Files,
			"versions_kept":           d.Limits.VersionsKept,
			"publishes_per_minute":    d.Limits.PublishPerMin,
			"artifacts_per_workspace": d.Limits.ArtifactsPerTenant,
			"comment_bytes":           d.Limits.CommentBytes,
		},
		"tasks":       ts,
		"entrypoints": entry,
		"contact":     "hello@byagent.dev",
	}, "", "  ")
	if err != nil {
		http.Error(w, "encode error", 500)
		return
	}
	write(w, "application/json", append(body, '\n'))
}
