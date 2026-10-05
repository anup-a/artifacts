{{define "guide-cursor.md"}}# {{.Guide.Title}}

> {{.Guide.Description}}

Cursor's agent writes plenty that isn't code: design notes, investigation write-ups, release plans, HTML prototypes. They usually stay in the repo or the chat panel. byagent lets the agent publish them as links, so people outside your editor can read and comment.

## Install the skill

From your project:

```sh
npx skills add anup-a/agent-artifacts -a cursor
```

That installs the byagent skill to `.agents/skills/byagent`, where Cursor picks it up. If your setup doesn't load skills, add a project rule that tells the agent to follow {{.Share}}/skill.md.

The skill uses the `byagent` CLI, which the agent runs in its terminal (`npx byagent`, or `npm install -g byagent` once). No account is needed to try it: the first publish gets a guest key for up to three public pages that last 24 hours. To keep pages and publish private ones, connect the agent from {{.API}}/app/connect: it gets a key of its own without one being pasted into the chat.

## Ask for a link

Tell the agent "publish this as a page" or "give me a link to the write-up". It runs something like:

```sh
byagent publish ./docs/auth-investigation.md --project "Auth" --tag investigation --agent cursor --json
```

and replies with the `url` from the output.

- Use `--private` for anything internal. Private pages need a workspace sign-in or a share code.
- Republish from the same file to update the same link. Old versions are kept, and `byagent rollback` restores one.

## Get feedback without a meeting

Send the link. Readers select text and comment, or suggest a replacement for it, without an account. Back in Cursor, ask the agent to handle the comments:

```sh
byagent comments <id> --open --json
```

The agent edits the file, republishes, replies, and resolves each thread.

## More

- [Host an HTML page your agent built]({{.Share}}/guides/host-agent-html)
- [Ways to share AI agent output, compared]({{.Share}}/guides/alternatives)
{{end}}
