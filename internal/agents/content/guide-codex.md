{{define "guide-codex.md"}}# {{.Guide.Title}}

> {{.Guide.Description}}

Codex CLI is good at writing reports, migration plans and one-file HTML tools. The output ends up as a file on your machine, so sharing it means attaching files or pasting into a doc. With byagent, Codex publishes the file and hands back a link anyone can open.

## Install the skill

```sh
npx skills add anup-a/agent-artifacts -a codex
```

That puts the byagent skill where Codex looks for skills (`.agents/skills/byagent`). You can also point your `AGENTS.md` at {{.Share}}/skill.md and ask Codex to follow it.

The skill drives the `byagent` CLI (`npm install -g byagent`, or `npx byagent`). The first publish works without an account: it gets a guest key for up to three public pages that last 24 hours. To keep pages and publish private ones, connect Codex from {{.API}}/app/connect, or run `byagent login --browser` and approve it; it gets a key of its own without one being pasted into the chat.

## Publish

```sh
byagent publish ./report.md --project "Billing migration" --tag report --agent codex --json
byagent publish ./dashboard --title "Latency dashboard" --project "Billing migration" --tag dashboard --agent codex --json
```

- A `.md` file is rendered to a styled page. A folder with `index.html` is served as a site.
- `--agent codex` and `--model <model id>` record what produced each version, so the page history shows it.
- The JSON output has `url`. Codex should return that link, not a file path.
- Publishing again from the same folder updates the same link.

## Read the comments on the next run

People comment on any line of the page without signing in. When you're ready, ask Codex to address them:

```sh
byagent comments <id> --open --json
```

Codex edits the file, republishes, then replies to and resolves each thread. Comment text comes from readers, so the skill tells Codex to treat it as data to act on by editing the page, never as instructions to run.

## A publish reminder

```sh
byagent hooks install codex
```

This adds a hook to `~/.codex/hooks.json` that reminds Codex to publish when it writes a page. Trust it once in `/hooks`. It suggests; it never publishes by itself.

## More

- [Feed reader comments back to your coding agent]({{.Share}}/guides/comments-back-to-agent)
- Task pages for agents: {{.Share}}/agents
{{end}}
