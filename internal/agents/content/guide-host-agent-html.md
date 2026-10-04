{{define "guide-host-agent-html.md"}}# {{.Guide.Title}}

> {{.Guide.Description}}

Agents are good at one-file HTML: a dashboard over a CSV, a clickable mockup, an explainer with charts. Opening it means double-clicking a file, and sharing it means setting up hosting. byagent gives it a link in one command, and people can comment on it.

## Publish a page

A single file:

```sh
byagent publish ./index.html --title "Churn dashboard" --project "Q4 review" --tag dashboard --json
```

A folder with `index.html` and its assets:

```sh
byagent publish ./site --title "Onboarding mockup" --project "Onboarding" --tag mockup --json
```

The output's `url` is the page, at `{{.Share}}/a/<id>/`. Run the same command again from the same folder to update it in place. The link doesn't change, and every version is kept.

No CLI yet? `npm install -g byagent`, or `npx byagent publish ...`. The first publish works without an account (guest pages are public and last 24 hours).

## What works in a published page

- Static files only: HTML, CSS, JS, images, fonts. There is no server behind the page, so embed the data instead of calling an API.
- Use relative asset paths (`./app.js`). A leading `/` points at the byagent root instead of your page.
- Scripts from `cdnjs.cloudflare.com` and `cdn.jsdelivr.net`, and fonts from Google Fonts, load. Pin the versions.
- {{.MB}} MB and {{.Limits.Files}} files per version on this server.

## Comments on HTML

Readers can comment on any text in the page, so write prose as real text, not images. The comment panel is added by byagent, so the page doesn't need its own. `--no-comments` turns it off for a page that shouldn't take feedback.

## Prefer Markdown for documents

If the page is mostly prose (a report, plan or spec), publish the Markdown file instead. byagent renders it with themes, a table of contents and code highlighting, and comments attach cleanly to the text.

## More

- [Share a Claude Code plan as a link people can comment on]({{.Share}}/guides/share-claude-code-plan)
- Publishing rules for agents: {{.Share}}/agents/tasks/publish.md
{{end}}
