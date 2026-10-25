{{define "guide-share-claude-code-plan.md"}}# {{.Guide.Title}}

> {{.Guide.Description}}

Claude Code writes plans, reports and specs as files in your repo or as long chat messages. To get feedback on one, you usually paste it into a doc, wait for comments, then copy them back into the chat by hand. byagent turns that into one link: Claude publishes the page, people comment on the line they mean, and Claude reads those comments on its next run.

## Set it up once

Install the byagent skill so Claude knows when and how to publish:

```sh
npx skills add anup-a/byagent
```

The skill uses the `byagent` CLI, which Claude can run with `npx byagent` or you can install globally:

```sh
npm install -g byagent
```

You don't need an account to try it. With no key, the first publish gets a guest key: up to three public pages, 5 MB each, that stop working after 24 hours. The output includes a `claim_url`; open it and sign in to keep the pages. For anything internal, connect Claude Code first: paste the prompt from {{.API}}/app/connect, or run `byagent login --browser` and approve it.

## Publish the plan

Ask Claude to "publish the plan", or let the skill do it when Claude finishes a document. Under the hood it runs:

```sh
byagent publish ./plan.md --project "Checkout rewrite" --tag plan --json
```

The Markdown becomes a styled page with light and dark themes, a table of contents and highlighted code. The JSON output has the `url`. That link is what you send around.

- Add `--private` for anything internal. Private pages open only for members of your workspace, or for someone you give a share code (`byagent share <id>`).
- Republishing from the same file or folder keeps the same link and adds a version. Readers always see the latest one.
- Add `--expires 7d` for a review draft that shouldn't outlive the review.

## Collect comments

Readers select any text on the page and comment on it. They don't need an account. A reader can also suggest replacement text for the selection, the way suggested edits work in a doc.

## Bring the comments back to Claude

On the next run, tell Claude "address the comments on the plan". It runs:

```sh
byagent comments <id> --open --json
```

Each thread comes back with the quoted text it is anchored to, so Claude knows exactly which line a comment is about. Claude edits the plan, republishes to the same link, replies, and resolves the thread. Readers see a "working on it" mark next to their comment while that happens. See [Feed reader comments back to your coding agent]({{.Share}}/guides/comments-back-to-agent) for the full loop.

If you're sending the plan out for review right now, ask Claude to watch it. `byagent comments <id> --wait` blocks until someone comments, so Claude can handle feedback as it arrives.

## Optional: a reminder to publish

Agents forget to publish. This adds a hook that suggests `byagent publish` when Claude writes a `.md` or `.html` page. It never publishes on its own:

```sh
byagent hooks install claude
```

## How this differs from Claude Code Artifacts

Claude Code's built-in Artifacts take comments only from people in your Claude organization, and need a claude.ai sign-in to publish. byagent pages take comments from anyone with the link, and work with an API key or from other agents. More in [Ways to share AI agent output, compared]({{.Share}}/guides/alternatives).

## What it costs

byagent is free, with no paid plan. The limits are on {{.Share}}/pricing.
{{end}}
