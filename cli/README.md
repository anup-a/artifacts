# byagent

Publish a page to [byagent.dev](https://byagent.dev), share the URL, read the comments on it. Built for agents: every command takes `--json` and prints exactly one object.

```
npm install -g byagent        # or: npx byagent <command>
```

## Sign in

Create a key in the dashboard at `https://app.byagent.dev/app/keys`, then:

```
echo "$KEY" | byagent login --api https://app.byagent.dev
byagent whoami
```

Credentials live in `~/.artifacts/config.json`. `ARTIFACTS_TOKEN` and `ARTIFACTS_API` override it.

## Publish

```
byagent publish ./site --title "Q3 plan" --project "Personal" --tag plan --json
byagent publish ./page.html --json
byagent publish ./notes.md --project "Personal" --tag notes --json
```

- A directory is uploaded as is (`.git`, `node_modules` and `.artifacts.json` are skipped) and served from its `index.html`.
- A single `.html` file becomes `index.html`.
- A `.md` file, or a directory whose index is `index.md` / `README.md`, is rendered to a styled page: light and dark themes, heading anchors, a sticky table of contents, GitHub-flavoured tables and task lists, highlighted code fences. The first H1 is the title unless `--title` is given. Images referenced relative to the file ship with it when you publish the directory.
- The directory's `.artifacts.json` remembers the artifact id, project and tags, so publishing the same directory again updates the same URL.

Flags: `--private | --public`, `--comments | --no-comments`, `--project <name>`, `--tag <t>` (repeatable), `--cover <image>`, `--cover-style dots|grid|lines|wash|rings|plain`, `--end-user <id>`, `--id <id>`.

## Everything else

```
byagent get <id>                       # url, title, version, open comments, labels, share link
byagent list [--project <name>]
byagent collections                    # every --project label: count, last update, share URL
byagent collection <name>              # one collection's artifacts in reading order, with urls
byagent share <id> [--off]             # six-digit code for a private artifact; rerun to rotate
byagent versions <id>
byagent rollback <id> <version>
byagent comments <id> [--open|--all] [--since <rfc3339>]
byagent reply <id> <thread> "<text>"
byagent resolve <id> <thread>
byagent delete <id>                    # permanent
byagent portal-link <end-user id>      # integrator workspaces only
```

Run `byagent` with no arguments for the full reference.

## Self-host

The server is Go and lives in the same repository: [github.com/anup-a/artifacts](https://github.com/anup-a/artifacts). Point the CLI at it with `--api` on login.
