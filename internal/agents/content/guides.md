{{define "guides.md"}}# Guides

How to share what a coding agent writes as a link people can open and comment on, and how to get that feedback back to the agent. Each guide is also available as Markdown: add `.md` to its URL.

{{range .Guides}}- [{{.Title}}]({{$.Share}}/guides/{{.Slug}}): {{.Description}}
{{end}}
Already have the CLI? The task pages are shorter: {{.Share}}/agents
{{end}}
