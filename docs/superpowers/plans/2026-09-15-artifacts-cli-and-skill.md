# Artifacts CLI and agent skill Implementation Plan

> **For agentic workers:** execute task-by-task, lean (no per-task review gates).

**Goal:** Ship the `artifacts` Node CLI (spec §8) and the agent skill (spec §9), so an agent can publish a page, hand over a URL, poll comments, reply, republish and resolve without a web console.

**Architecture:** A dependency-free ES-module Node package under `cli/`, run via `npx artifacts`. One thin `fetch` client that maps the server's `{error:{code,message}}` envelope to a `CliError` carrying an exit code. Commands are small modules; every command takes `--json` and prints exactly one JSON object on stdout. The skill lives at `skill/SKILL.md`, is `go:embed`ed and served at `GET /skill.md` on the share origin.

**Tech Stack:** Node >= 18 (global `fetch`, `FormData`, `Blob`, `crypto.subtle` not needed — use `node:crypto`), `node:test` runner, no runtime dependencies. Go 1.22+ for the `/skill.md` route.

## Global Constraints

- No runtime npm dependencies. Dev dependencies are not allowed either; tests use `node:test`.
- Package name `artifacts`, `"type": "module"`, `bin` entry `artifacts` → `bin/artifacts.js`.
- Config resolution order: `ARTIFACTS_TOKEN` / `ARTIFACTS_API` env, then `~/.artifacts/config.json` `{token, api}`. Default api `https://api.artifacts.dev` is NOT assumed: if neither env nor config supplies one, error out.
- `--json` prints one JSON object to stdout and nothing else; errors print one JSON object to stderr and exit non-zero.
- Exit codes: 0 ok, 1 API/runtime error, 2 usage error.
- Publish excludes `.git`, `node_modules`, `.artifacts.json`, and dotfiles at any level except none (keep it simple: exclude `.git`, `node_modules`, `.artifacts.json`).
- Never write files from Bash; use Write/Edit only.
- No em-dashes in any prose.

---

## File Structure

- `cli/package.json` — package metadata, `bin`, `scripts.test`.
- `cli/bin/artifacts.js` — shebang entry, delegates to `src/main.js`.
- `cli/src/args.js` — minimal flag parser.
- `cli/src/config.js` — config resolution and `~/.artifacts/config.json` write.
- `cli/src/errors.js` — `CliError`, `usage`.
- `cli/src/client.js` — `apiFetch`, `apiJson`, error-envelope mapping.
- `cli/src/files.js` — directory walk, sha256, single-html handling, `.artifacts.json` read/write.
- `cli/src/commands/publish.js`
- `cli/src/commands/artifacts.js` — `get`, `list`, `versions`, `rollback`, `delete`
- `cli/src/commands/comments.js` — `comments`, `reply`, `resolve`
- `cli/src/commands/login.js`
- `cli/src/main.js` — dispatch table, `--json` contract, exit codes.
- `cli/test/unit.test.mjs` — args, config, files.
- `cli/test/e2e.test.mjs` — real spawned Go server, every command.
- `skill/SKILL.md` — the agent skill.
- `internal/skill/embed.go` — `//go:embed SKILL.md` of a copy, or embed from repo root.
- `internal/serve/serve.go` — register `GET /skill.md`.
- `.github/workflows/ci.yml` — `cli` job.
- `README.md` — CLI + skill section.

---

### Task 1: CLI scaffold, args, config, errors

**Files:** `cli/package.json`, `cli/bin/artifacts.js`, `cli/src/args.js`, `cli/src/config.js`, `cli/src/errors.js`, `cli/test/unit.test.mjs`

- [ ] `parseArgs(argv)` → `{command, positional[], flags{}}`; `--k v`, `--k=v`, boolean flags for a known set (`--json`, `--open`, `--all`).
- [ ] `resolveConfig(env, home)` → `{token, api}`; env wins, then `~/.artifacts/config.json`; throws `CliError("not configured", 2)` when token or api missing.
- [ ] `saveConfig(home, {token, api})` writes `0600` `~/.artifacts/config.json`.
- [ ] Unit tests for all three.
- [ ] Commit.

### Task 2: API client

**Files:** `cli/src/client.js`

- [ ] `apiJson(cfg, method, path, {body, query})` → parsed JSON; on non-2xx throw `CliError(message, 1, {code, status, requestId})` reading `error.code`, `error.message`, `X-Request-Id`.
- [ ] 204 returns `null`. Network failure throws `CliError` too, never a bare fetch rejection.
- [ ] Commit.

### Task 3: File collection and publish

**Files:** `cli/src/files.js`, `cli/src/commands/publish.js`

- [ ] `collectFiles(target)`: a `.html` file publishes as a single `index.html`; a directory walks recursively, skipping `.git`, `node_modules`, `.artifacts.json`, producing `[{path (posix, relative), abs, sha256, size}]`.
- [ ] `readLink(dir)` / `writeLink(dir, {id, api})` for `.artifacts.json`.
- [ ] `publish`: resolve id from `--id` else `.artifacts.json` (only when api matches), POST `/api/artifacts/preflight` with the manifest, then multipart POST/PUT with the manifest part FIRST and one file part per missing sha.
- [ ] Write `.artifacts.json` back for directory targets. Output `{id, url, version, files, bytes}`.
- [ ] Unit tests for `collectFiles` (single html, exclusions, nesting).
- [ ] Commit.

### Task 4: Read and lifecycle commands

**Files:** `cli/src/commands/artifacts.js`

- [ ] `get <id>`, `list`, `versions <id>`, `rollback <id> <version>`, `delete <id>` mapped onto the API routes; human output is compact lines, `--json` passes the API object through (delete emits `{id, deleted: true}` since the API returns 204).
- [ ] Commit.

### Task 5: Comment commands

**Files:** `cli/src/commands/comments.js`

- [ ] `comments <id> [--open|--all] [--since ts]` → `GET /api/artifacts/{id}/comments?status=open|all&since=`; default `--open`.
- [ ] `reply <id> <thread> "<text>"` → `POST .../comments/{thread}/reply` with `{body}`.
- [ ] `resolve <id> <thread>` → `POST .../comments/{thread}/resolve`.
- [ ] Human output prints thread id, anchor status, quote, then each comment.
- [ ] Commit.

### Task 6: login and main dispatch

**Files:** `cli/src/commands/login.js`, `cli/src/main.js`

- [ ] `login [--token t] [--api u]` writes the config file and verifies with `GET /api/artifacts?limit=1`; without `--token`, reads one line from stdin so it is pipe-friendly and never echoes.
- [ ] `main(argv)` dispatches, prints `--json` or human output, catches `CliError` (stderr `{error:{code,message}}` when `--json`, plain line otherwise) and exits with its code.
- [ ] `--help` / no command prints usage, exit 2.
- [ ] Commit.

### Task 7: e2e against the real server

**Files:** `cli/test/e2e.test.mjs`

- [ ] `before`: build `./cmd/artifacts-server` into a temp dir, spawn with `ADDR=127.0.0.1:<port>`, `ADMIN_TOKEN`, `DB=sqlite`, `STORAGE=local`, `DATA_DIR`, both public origins, and `LIMIT_PUBLISH_PER_MIN=1000` / `LIMIT_COMMENTS_PER_MIN=1000`.
- [ ] Run the CLI as a child process (`node bin/artifacts.js ... --json`) with `ARTIFACTS_TOKEN`/`ARTIFACTS_API` set, covering: publish dir, republish in place via `.artifacts.json`, publish single html, get, list, versions, rollback, comment created through `/c/{id}/comments` then `comments --open`, `reply`, `resolve`, `comments --open` empty, `delete` then `get` gone.
- [ ] Commit.

### Task 8: skill/SKILL.md and GET /skill.md

**Files:** `skill/SKILL.md`, `internal/serve/skill.go` (or extend `serve.go`), `internal/serve/serve_test.go`

- [ ] Write the skill: when to use, the publish loop, the comment loop, page design constraints (self-contained HTML, relative asset paths, allowed CDNs matching the server CSP), and the exact commands.
- [ ] Embed it and serve `GET /skill.md` as `text/markdown; charset=utf-8` from the serve handler.
- [ ] Go test asserting a 200 and a known substring.
- [ ] Commit.

### Task 9: CI job, smoke, README

**Files:** `.github/workflows/ci.yml`, `scripts/smoke.sh`, `README.md`

- [ ] `cli` job: node 22 + go, `npm test` in `cli/`.
- [ ] Smoke: assert `/skill.md` serves the skill.
- [ ] README: CLI command table and skill pointer.
- [ ] Commit.
