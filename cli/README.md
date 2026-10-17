# byagent

Publish a page to [byagent.dev](https://byagent.dev), share the URL, read the comments on it. Built for agents: every command takes `--json` and prints exactly one object.

```
npm install -g byagent        # or: npx byagent <command>
```

## Try it without an account

```
npx byagent publish ./page.html
```

With nothing configured, `publish` gets a guest key and saves it to `~/.artifacts/config.json`. Guest pages are public, three at most, 5 MB each, and stop working 24 hours after the key was made. The output ends with a claim link: open it, sign in and confirm, and the pages move into your workspace for good. The guest key stops working then, and the dashboard offers a connect prompt for your agent. `byagent login --guest` gets a guest key up front; `BYAGENT_GUEST=0` turns the automatic one off.

## Keep it on your machine

```
byagent publish ./page.html --local
```

`--local` (or `BYAGENT_LOCAL=1`) runs any command against a small server on your own machine instead of byagent.dev. You need no account, and nothing is uploaded. The first `--local` command starts the server in the background. Pages open at `http://localhost:4790/a/<id>/` with the same comment panel, and `http://localhost:4790/` lists them all. `publish`, `get`, `list`, `versions`, `rollback`, `version rm`, `diff`, `delete`, `comments` (including `--wait`), `reply`, `resolve` and `mark` all work, so an agent can run the full comment loop offline. Pages, versions and comments are kept in `~/.byagent/local` (`BYAGENT_LOCAL_DIR`). The port is 4790 (`BYAGENT_LOCAL_PORT` or `--port`). `byagent local status` shows the server and `byagent local stop` ends it. When a page is ready to share, publish it again without `--local`: the folder keeps its local id separate from its byagent.dev one. Sharing, access, expiry, analytics, vars, domains and collections need byagent.dev.

## Sign in

Connect an agent without pasting a key into its chat. Either paste the prompt from the dashboard's Connect agent page (`https://app.byagent.dev/app/connect`), which runs:

```
npx -y byagent@latest login --code K7QD-M3XR
```

or start from the terminal and approve in the browser:

```
byagent login --browser              # prints a link and waits for you to approve it
byagent login --browser --no-wait    # prints the link and exits; then:
byagent login --wait                 # exit 3 while not approved yet
```

Either way the CLI gets a key of its own, named after the agent (`claude-code on laptop`, or `--name`), which you can revoke under API keys. Codes work once and expire in 10 minutes.

For CI, create a key at `https://app.byagent.dev/app/keys` and pipe it in:

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
- An HTML page published without `--title` takes its `<title>` as its name, so it doesn't show as "Untitled".
- A `.md` file, or a directory whose index is `index.md` / `README.md`, is rendered to a styled page: light and dark themes, heading anchors, a sticky table of contents, GitHub-flavoured tables and task lists, highlighted code fences. The first H1 is the title unless `--title` is given. The page's language (Chinese, Japanese, Korean or English) is detected from the text and sets its fonts and contents label; `--lang zh-CN` overrides it. Images referenced relative to the file ship with it when you publish the directory.
- The directory's `.artifacts.json` remembers the artifact id, project and tags, so publishing the same directory again updates the same URL.
- A `.byagent.json` at a project root, `{"collection": "Puzzled"}`, makes that collection the default `--project` for every publish below it. The nearest one wins, so a monorepo can give each app its own. It holds no key, so commit it.

Flags: `--private | --public`, `--comments | --no-comments`, `--state | --no-state`, `--summary "<what changed>"`, `--project <name>`, `--tag <t>` (repeatable), `--cover <image>`, `--cover-style dots|grid|lines|wash|rings|plain`, `--end-user <id>`, `--id <id>`, `--lang <tag>` (Markdown only).

## Agent hooks

```
byagent hooks install claude           # ~/.claude/settings.json
byagent hooks install codex            # ~/.codex/hooks.json, then trust it in /hooks
byagent hooks install claude --scope project   # ./.claude/settings.json instead
byagent hooks uninstall claude
```

This adds a PostToolUse hook. When the agent writes a `.html` or `.md` page, the hook adds one line to the agent's context suggesting `byagent publish`. If the page's directory is already published, it suggests republishing instead. The hook never publishes anything itself.

It stays out of the way: each file gets at most one nudge per session, with five nudges per session in total. It skips code trees (`src`, `public`, `node_modules`, ...), repo docs (`README.md`, `CLAUDE.md`, `AGENTS.md`, ...) and agent config under `~/.claude` and `~/.codex`. Set `BYAGENT_NUDGE=0` to silence it. The hook runs `byagent hooks run`, so install the CLI globally.

## Briefs

A brief is the page an agent leaves for whoever picks the work up next: another agent, another machine, or you.

```
byagent brief                          # BRIEF.md, its published version and the open comments on it
byagent brief push                     # publish BRIEF.md into the folder's collection
```

`BRIEF.md` sits next to `.byagent.json`. Where there is no `BRIEF.md` (a fresh clone, another machine), `byagent brief` reads the pushed copy back; `--project <name>` names the collection from anywhere, and the MCP tool `brief_get` does the same for agents without a shell. `brief push` files it into that collection with the tag `brief` and updates the same page every time, so each collection has one brief and one URL. A new brief starts private. An unchanged file is not sent again, and the publish hook never nudges about it. Push when the work changes state (started, blocked, handed off, done), not after every turn.

## Variables for proxy routes

A page can call an API (OpenRouter, Supabase, Stripe) without holding the key. Store the key as a variable, pinned to the hosts it may be sent to:

```bash
printf %s "$OPENROUTER_API_KEY" | byagent vars set OPENROUTER_API_KEY --upstream openrouter.ai
byagent vars                 # names and hosts; values are never shown
byagent vars rm OPENROUTER_API_KEY
```

The value is read from stdin (or a hidden prompt, or `--from-env VAR`), never from an argument. The page ships `.byagent/proxy.json` declaring routes such as `{"routes": {"chat": {"upstream": "https://openrouter.ai/api/v1/chat/completions", "methods": ["POST"], "headers": {"Authorization": "Bearer {{OPENROUTER_API_KEY}}"}}}}` and calls `fetch("_proxy/chat", …)`; the server adds the key and forwards the call.

## Everything else

```
byagent get <id>                       # url, title, version, open comments, labels, share link
byagent pull <id> [<dir>] [--at <v>]   # download a page's files as published, private ones too
byagent list [--project <name>]
byagent collections                    # every --project label: count, last update, share URL
byagent collection <name>              # one collection's artifacts in reading order, with urls
byagent share <id> [--off]             # six-digit code for a private artifact; rerun to rotate
byagent versions <id>                  # history with summaries, live version marked, owner-only preview links
byagent state <id> [--clear]           # the page's shared state (window.byagent.state) as JSON; --clear empties it
byagent rollback <id> <version>        # make an old version live again; nothing re-uploaded
byagent version rm <id> <version>      # delete one old version for good (never the live one)
byagent diff <id> <from> [<to>]        # files added, removed and changed between versions
byagent comments <id> [--open|--all] [--since <rfc3339>]
byagent comments <id> --wait [--timeout <seconds>] [--interval <seconds>]
byagent reply <id> <thread> "<text>"
byagent resolve <id> <thread>
byagent delete <id>                    # permanent
byagent portal-link <end-user id>      # integrator workspaces only
```

Run `byagent` with no arguments for the full reference.

## Self-host

The server is Go and lives in the same repository: [github.com/anup-a/artifacts](https://github.com/anup-a/artifacts). Point the CLI at it with `--api` on login.
