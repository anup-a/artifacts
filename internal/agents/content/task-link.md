{{define "task-link.md"}}# {{.Task.Title}}

> {{.Task.Summary}}

Part of {{.Share}}/agents. Needs a key: {{.Share}}/agents/tasks/setup.md

A link is a pointer, like a symlink: byagent keeps the URL, a title, a project and which agent filed it, and lists it in the dashboard under **Linked** next to the pages it hosts. It never copies, hosts or serves the page. If you have the files, publish them instead ({{.Share}}/agents/tasks/publish.md).

## Steps

```sh
byagent link https://my-app.vercel.app --title "Launch page" --project "<name>" --json
byagent links --json                     # every link, newest first
byagent unlink <id|url> --json           # off the list; the page stays where it is
```

- Use the stable URL (production alias, site URL), not a per-deploy preview. Filing the same URL again refreshes it and keeps its title.
- `--agent <name>` says who made it (`grok`, `chatgpt`, `claude`); otherwise the CLI detects Claude Code and Codex itself.
- Claude Code files deploys on its own after `byagent hooks install claude`.
- Not for byagent pages, localhost or private-network URLs, or URLs carrying a secret.

## Done when

`byagent links --json` lists the URL with the right title, and the user has been told it is filed.
{{end}}
