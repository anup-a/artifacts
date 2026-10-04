{{define "guide-alternatives.md"}}# {{.Guide.Title}}

> {{.Guide.Description}}

A coding agent finishes a plan, a report or an HTML page, and someone else needs to read it and react. These are the usual ways to share it, and where each one fits.

## At a glance

| | Renders Markdown | Hosts HTML pages | Comments on a line | Readers need an account to comment | Agent can read the comments |
|---|---|---|---|---|---|
| byagent | Yes | Yes | Yes | No | Yes, `byagent comments --json` |
| Claude Code Artifacts | Yes | Yes, one page | Inside your organization only | Yes, claude.ai in your org | Yes, in Claude Code |
| GitHub Gist | Yes | Shows the source | No, comments go under the gist | Yes, GitHub | Through the GitHub API |
| Google Docs | After you paste it in | No | Yes | Yes, Google | Through the Google API, with setup |
| Chat share link | In the chat view | Depends on the app | No | n/a | No |
| Static host (Pages, Netlify and similar) | With a build step | Yes | No | n/a | No |

## byagent

Built for this one job. The agent publishes Markdown or an HTML folder with one command, readers comment on the exact text without signing in, and the agent reads the threads back as JSON on its next run, then replies and resolves them. Republishing keeps the link. It's free. It hosts static pages only: no servers, no databases.

## Claude Code Artifacts

Claude Code can publish its own work to a page on claude.ai, and it's very good if everyone involved is in your Claude organization. Per Anthropic's docs ([code.claude.com/docs/en/artifacts](https://code.claude.com/docs/en/artifacts)), as of this guide's date:

- Comments work when the page is shared within your organization on a Team or Enterprise plan. People who only have the public link can't see or add comments.
- Publishing needs a session signed in to claude.ai on a paid plan. Sessions using an API key, Amazon Bedrock or Google Cloud can't publish.
- It works from Claude Code only, and each artifact is a single page.

byagent fits when the readers are outside your organization (clients, other teams, people with no Claude account), when the agent is Codex, Cursor or another tool, or when you run Claude Code with an API key.

## GitHub Gist

Good for sharing a Markdown file with developers who already use GitHub. Gists render Markdown, but show HTML as source code rather than a page. Comments sit in one thread under the gist rather than on a line, and commenting needs a GitHub account.

## Google Docs

Great line-level comments, and everyone knows it. But the agent's Markdown has to be pasted or converted first, HTML pages don't survive, and getting comments back to the agent means wiring up the Google API or copying them by hand.

## Chat share links

Sharing the conversation from Claude, ChatGPT or similar shows the whole exchange, not a clean document. Readers can't comment on it, so feedback comes back through some other channel.

## Static hosts

GitHub Pages, Netlify, Vercel and similar host HTML well and are the right choice for a real site. For a one-off agent page they mean a repo or a project per page, and they have no comments.

## Try byagent

```sh
npx skills add anup-a/agent-artifacts
```

Then ask your agent to publish its next report. Details: [the Claude Code guide]({{.Share}}/guides/share-claude-code-plan), or the agent entrypoint at {{.Share}}/agents.
{{end}}
