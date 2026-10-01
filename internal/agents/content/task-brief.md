{{define "task-brief.md"}}# {{.Task.Title}}

> {{.Task.Summary}}

Part of {{.Share}}/agents. Needs a key: {{.Share}}/agents/tasks/setup.md

A folder with a `.byagent.json` such as `{"collection": "<name>"}` is a project. Every publish below it joins that collection. Its `BRIEF.md` is the note for whoever picks the work up next.

## Starting work

```sh
byagent brief --json
```

It returns BRIEF.md and the open comments on the published brief. Without the folder (a fresh clone, another machine), `byagent brief --project "<name>" --json` reads the pushed copy from anywhere.

## When the work changes state

Started, blocked, handed off or done: rewrite BRIEF.md, then

```sh
byagent brief push --json
```

Keep it short, for a reader with no context: a `# Title`, then Goal, Where it stands, Next move, Tried and ruled out, and Needs a person. Do not push after every turn, and do not publish BRIEF.md with `byagent publish`.
{{end}}
