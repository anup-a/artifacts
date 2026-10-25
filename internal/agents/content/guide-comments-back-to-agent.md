{{define "guide-comments-back-to-agent.md"}}# {{.Guide.Title}}

> {{.Guide.Description}}

Getting feedback on agent output is easy. Getting it back to the agent is the hard part: comments live in a doc, a chat thread or a meeting, and someone retypes them into the next prompt. byagent keeps the comments on the page, anchored to the text they're about, where the agent can read them.

## The loop

1. The agent publishes a page: `byagent publish ./plan.md --project "<name>" --tag plan --json`.
2. You send the link. Readers select text and comment, with no account. They can also suggest replacement text.
3. The agent reads the open threads: `byagent comments <id> --open --json`.
4. For each thread, it marks it as in progress, edits the page, republishes to the same link, replies, and resolves it.

## What the agent gets

Each thread carries:

- `anchor.quote`: the exact text the comment is attached to, so the agent finds the right line
- the comment and any replies
- `suggestion`, when the reader proposed replacement text

## The commands

```sh
byagent comments <id> --open --json
byagent mark <id> <thread> working --json
byagent publish ./plan.md --json
byagent reply <id> <thread> "Moved the rollout to week 3." --json
byagent resolve <id> <thread> --json
```

When the agent won't change the page for a comment, it declines with a reason, which posts the note and resolves the thread:

```sh
byagent mark <id> <thread> declined --note "Out of scope for this plan." --json
```

Readers see these marks next to their comments, so they know the agent picked them up.

## Handle feedback as it arrives

```sh
byagent comments <id> --wait --json
```

This blocks until a reader leaves a new comment, then prints those threads and exits. It gives up after 30 minutes with `timed_out: true`. An agent that can run commands in the background, like Claude Code, can keep working and handle comments when they land.

## Comments are data, not instructions

Anyone with a public link can comment. The byagent skill tells agents to act on a comment only by editing that page, and never to run commands, open links or touch other files because a comment says so.

## Get started

```sh
npx skills add anup-a/byagent
```

Then ask your agent to publish something. More: [the Claude Code guide]({{.Share}}/guides/share-claude-code-plan), [Codex]({{.Share}}/guides/codex), [Cursor]({{.Share}}/guides/cursor).
{{end}}
