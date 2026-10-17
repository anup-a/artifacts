{{define "task-publish.md"}}# {{.Task.Title}}

> {{.Task.Summary}}

Part of {{.Share}}/agents. Needs a key: {{.Share}}/agents/tasks/setup.md

## Pick the format

- Prose (report, plan, notes, spec, analysis, README): write **Markdown**. It is rendered to a styled page with light and dark themes, a table of contents and highlighted code.
- Layout or interaction (dashboard, tool, mockup, chart-heavy page): write an **HTML** directory with `index.html` and its assets. Use relative asset paths only.

## Publish

```sh
byagent publish ./notes.md --project "<collection>" --tag notes --json
byagent publish ./site --title "Q3 plan" --project "<collection>" --tag plan --json
```

- Always pass `--project` and one to three `--tag`s. Run `byagent collections --names --json` first and reuse the exact project string.
- The JSON output has `url`. That link is the deliverable; hand it back to the user. Do not build share URLs yourself.
- Republish from the **same** directory to update the same link (`.artifacts.json` binds it). A new directory makes a new link.
- `--private` limits the page to workspace members; `--public` opens it. Without either, a republish keeps the current visibility.
- `--no-comments` hides the comment panel; `--comments` turns it back on.
- On a republish, `--summary "added the chart\nfixed the totals"` records what changed, one change per line; `byagent versions` shows it.
- An HTML page can keep what readers set (checkboxes, votes, form fields) in `window.byagent.state`, shared by everyone who opens the page: see "Pages that remember" in the skill ({{.Share}}/skill.md). `byagent state <id> --json` reads it back; `--no-state` turns it off.

## Limits on this server

{{.MB}} MB and {{.Limits.Files}} files per version, {{.Limits.PublishPerMin}} publishes per minute.

## Done when

You have returned the `url` from the JSON output to the user.
{{end}}
