# artifacts: artifact hosting for agent tools, with comments

Design spec, 2026-09-15. Working product name: **artifacts**.

## 1. Purpose

Agents (Claude Code, Codex, Orca, custom) produce HTML pages: reports, plans,
dashboards, mockups. They need a stable shareable URL, the ability to republish
in place, and a way for the humans who open the link to point at things and
comment so the agent can read the feedback and iterate.

Today every tool builds its own (Orca `share.onorca.dev`, Claude artifacts,
aifee sites, gf-preview). This is that primitive as an independent product:
a managed service with a cheap API, and the same binary self-hostable.

First customer: developers running agents. The agent is the console.

## 2. Decisions (locked)

| Topic | Decision |
|---|---|
| First customer | Agent tools: API + CLI + `SKILL.md`. No web console in v1. |
| Comments | Anchored to a text selection or element. Viewer gives a display name, no account. Injected runtime, page stays static. |
| URL model | Path-based `https://share.<domain>/a/<id>/`. Object storage + CDN. |
| Publisher identity | API keys minted with an email. Self-host: single `ADMIN_TOKEN`. |
| Versioning | Content-addressed versions, rollback via API, keep last 20 on managed. |
| Stack | Go server (single binary), Postgres or SQLite, S3-compatible or local disk. Node CLI. Vanilla-JS comments runtime. |
| Managed deploy | Hetzner CX22 running the self-host container + Neon Postgres (free) + Cloudflare R2 (free), Cloudflare in front. ~$4/month. Fly.io is the documented upgrade path for HA. |
| v1 scope | Publish loop and comments in the same release (approach 1). |

## 3. Architecture

```
agent / CLI ──HTTPS──▶ artifacts-server (Go) ──▶ Postgres | SQLite   (keys, artifacts, versions, files, comments)
                              │              ──▶ S3/R2 | local disk (blobs by sha256)
viewer browser ─▶ CDN ─▶ GET /a/<id>/[v/<n>/]<path>   (server streams from storage, CDN caches by ETag)
                          comments runtime /_rt/comments.js ─▶ /c/<id>/comments
```

- Two public origins on managed: `share.<domain>` (pages, `/_rt/*`, `/c/*`)
  and `api.<domain>` (`/api/*`). Self-host may serve both from one origin;
  `PUBLIC_SHARE_ORIGIN` and `PUBLIC_API_ORIGIN` env vars drive link generation
  and CORS.
- The server serves artifact bytes itself so self-host works with local disk.
  The CDN is a cache, not a requirement.
- Publish-time injection: for every `.html` file in a version the server
  inserts `<script src="/_rt/comments.js" data-artifact="<id>" data-version="<n>" defer></script>`
  before `</body>` (or appends if absent). The original bytes are stored
  untouched; injection happens on serve and is cached.
- CSP on `/a/*` responses:
  `default-src 'self' data: blob:; script-src 'self' 'unsafe-inline' https://cdnjs.cloudflare.com https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; img-src 'self' data: blob: https:; connect-src 'self' <PUBLIC_SHARE_ORIGIN>; frame-ancestors 'none'`.
- Shared-origin mitigation: the runtime namespaces its own `localStorage`
  keys by artifact id; the API origin is separate so no publisher credential
  ever lives on the share origin; `/c/*` is the only thing a page can call.

### 3b. Managed deployment

```
Cloudflare free plan (DNS, TLS, WAF + rate limit on /c/*, cache /a/* and /_rt/*)
   ├─ share.<domain> ─┐
   └─ api.<domain>   ─┴─▶ Hetzner CX22 (2 vCPU, 4 GB, 20 TB egress), docker compose,
                          the same image as self-host with DB=postgres STORAGE=s3
                              ├─▶ Neon Postgres (free tier, scale-to-zero)
                              └─▶ Cloudflare R2 bucket (S3 API, free 10 GB, zero egress)
```

- The VPS holds no state. Rebuilding it is: install Docker, copy `.env`,
  `docker compose up -d`. Documented in `deploy/RUNBOOK.md`.
- Deploy pipeline: GitHub Actions on tag `v*` → build multi-arch image →
  push GHCR (`ghcr.io/<org>/artifacts`, this is also the self-host image) →
  SSH to the box → `docker compose pull && docker compose up -d`. Deploys
  cause a few seconds of downtime; acceptable at launch.
- Migrations embedded in the binary, run idempotently at boot.
- Health: `GET /healthz` (DB ping + storage HEAD), polled by Cloudflare or
  a free uptime monitor. Logs: JSON to stdout, `docker logs`.
- Cost: ~$4/month (Hetzner) until Neon or R2 outgrow free. Alternative at $0:
  Oracle Cloud Always-Free ARM VM with `DB=sqlite STORAGE=local`.
- Upgrade path when HA matters: same image on Fly.io, 2 machines
  (`deploy/fly.toml` kept in the repo); nothing else changes.
- Secrets in `.env` on the box: `DATABASE_URL`, `S3_ENDPOINT`, `S3_BUCKET`,
  `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `KEY_SIGNING_SECRET`,
  `PUBLIC_SHARE_ORIGIN`, `PUBLIC_API_ORIGIN`.

### 3c. Self-host

```yaml
# docker-compose.yml
services:
  artifacts:
    image: ghcr.io/<org>/artifacts:latest
    ports: ["8080:8080"]
    environment:
      DB: sqlite                 # or postgres + DATABASE_URL
      STORAGE: local             # or s3 + S3_* vars
      DATA_DIR: /data
      ADMIN_TOKEN: change-me
      PUBLIC_SHARE_ORIGIN: http://localhost:8080
      PUBLIC_API_ORIGIN: http://localhost:8080
    volumes: ["./data:/data"]
```

One container, one volume. `POST /api/keys` is disabled when `ADMIN_TOKEN`
is set; the admin token is the only key. All limits below are env-overridable
(`LIMIT_VERSION_BYTES`, `LIMIT_FILES`, `LIMIT_VERSIONS_KEPT`, `LIMIT_PUBLISH_PER_MIN`).

## 4. API

Base: `PUBLIC_API_ORIGIN`. Auth: `Authorization: Bearer <key>` on all `/api/*`
routes except `POST /api/keys`. Viewer routes under `/c/*` on the share origin
take no auth; the artifact id is the capability.

| Method | Path | Notes |
|---|---|---|
| `POST` | `/api/keys` | `{email}` → `{key, quota}`. Managed only. Rate-limited per IP. Key is `ak_` + 32 random bytes base62; only its SHA-256 is stored. |
| `POST` | `/api/artifacts/preflight` | `{files:[{path, sha256, size}]}` → `{missing:[sha256]}`. Lets the CLI upload only new blobs. |
| `POST` | `/api/artifacts` | multipart: part `manifest` (JSON `{title?, files:[{path, sha256, size}]}`) then one part per missing blob named by sha256. → `{id, url, version:1}`. Requires `index.html` at root. |
| `PUT` | `/api/artifacts/:id` | Same body → new version, same URL. → `{id, url, version:n}`. |
| `GET` | `/api/artifacts/:id` | `{id, title, url, current_version, versions_count, open_comments, created_at, updated_at}` |
| `GET` | `/api/artifacts?limit&cursor` | Owner's artifacts, newest first. |
| `DELETE` | `/api/artifacts/:id` | Soft delete; page returns 410; blobs GC'd later. |
| `GET` | `/api/artifacts/:id/versions` | `[{version, created_at, bytes, files, comment_count}]` |
| `POST` | `/api/artifacts/:id/rollback/:version` | Creates version n+1 with version v's file set. |
| `GET` | `/api/artifacts/:id/comments?status=open\|resolved\|all&since=<rfc3339>` | Threads with anchors, `anchor_status`, version. |
| `POST` | `/api/artifacts/:id/comments/:thread/reply` | `{body}` → owner reply, displayed as "Author". |
| `POST` | `/api/artifacts/:id/comments/:thread/resolve` | Marks resolved. |
| `POST` | `/c/:id/comments` | Viewer. `{author_name, body, anchor, version}` → thread. `Idempotency-Key` header honoured. |
| `POST` | `/c/:id/comments/:thread/reply` | Viewer. `{author_name, body}`. |
| `GET` | `/c/:id/comments?version=n` | What the runtime renders: open threads with raw anchors. The server never resolves anchors; the runtime does (section 6). |
| `GET` | `/a/:id/*` | Current version. `/a/:id/v/:n/*` pins a version. Directory paths resolve to `index.html`. |
| `GET` | `/healthz` | |

Errors: `{"error":{"code","message","request_id"}}`. Codes:
`INVALID_ARGUMENT` 400, `UNAUTHORIZED` 401, `FORBIDDEN` 403, `NOT_FOUND` 404,
`GONE` 410, `PAYLOAD_TOO_LARGE` 413, `RATE_LIMITED` 429, `QUOTA_EXCEEDED` 429,
`INTERNAL` 500. Every response carries `X-Request-Id`.

Limits (managed free tier): 25 MB per version, 500 files, 10 publishes/min
per key, 200 artifacts per unverified key, 20 versions kept, 2 KB comment body,
20 comments/min per IP on `/c/*`.

Ids: artifact id = 12 chars base62 from 9 random bytes (`cHgm0mQz3kLp`).
Never sequential.

## 5. Data model

```
keys        (id, key_sha256 UNIQUE, email, created_at, quota_json, revoked_at)
artifacts   (id, key_id, title, current_version, created_at, updated_at, deleted_at)
versions    (artifact_id, version, created_at, bytes, file_count, PRIMARY KEY(artifact_id, version))
files       (artifact_id, version, path, sha256, size, content_type, PRIMARY KEY(artifact_id, version, path))
blobs       (sha256 PRIMARY KEY, size, refcount, created_at)
threads     (id, artifact_id, version, anchor_json, status open|resolved, created_at, resolved_at)
comments    (id, thread_id, author_name, author_kind viewer|owner, body, created_at)
```

Blob storage key: `blobs/<sha256[0:2]>/<sha256>`. Local backend: same layout
under `DATA_DIR/blobs`. `refcount` drops when versions are pruned or artifacts
deleted; a periodic GC removes zero-ref blobs older than 24 h.

Version pruning: after a publish, if `versions_count > LIMIT_VERSIONS_KEPT`,
delete the oldest versions except any that still have open threads.

## 6. Comment anchors

Anchor JSON:

```json
{"kind":"text","quote":"…exact selected text…","prefix":"…32 chars before…","suffix":"…32 chars after…"}
{"kind":"element","css":"main > section:nth-of-type(2) > h2","text":"…trimmed textContent, max 120…"}
```

Runtime resolution on each page load, against the version being viewed:

1. Text: exact `quote` found once → `attached`. Found several times → pick the
   one with best prefix/suffix match. Not found → try fuzzy (prefix+suffix
   window) → `drifted`. Still nothing → `orphaned`.
2. Element: `css` resolves and `text` matches → `attached`. `css` fails but a
   unique element with the same `text` exists → `drifted`. Else `orphaned`.

`anchor_status` is computed by the runtime and reported back with a
`POST /c/:id/comments/:thread/seen` beacon `{version, anchor_status}` so the
owner's `GET /api/.../comments` shows the latest known status per thread.
Orphaned threads are listed in the side panel under "On earlier version", never
dropped.

## 7. Comments runtime (`/_rt/comments.js`)

Vanilla JS, no dependencies, under 25 KB minified. Injected on serve.

- Selection → floating "Comment" button → popover with name (remembered in
  `localStorage` under `artifacts:<id>:name`) and body.
- Click-to-comment on any element when the side panel's "point at element"
  mode is on.
- Highlights for attached/drifted anchors; a right-hand side panel listing
  threads, replies, resolve state; viewer replies inline.
- Version switcher in the panel header when the artifact has more than one
  version (`/a/<id>/v/<n>/`).
- Renders correctly on light and dark pages: the panel uses its own scoped
  styles inside a shadow root so the host page's CSS cannot break it and vice
  versa.
- Fails closed: if `/c/*` is unreachable the page still renders, the panel
  shows "comments unavailable".

## 8. CLI (`npx artifacts`)

Node package `artifacts` (working name), runs via `npx` with no install step. Reads
`ARTIFACTS_TOKEN` and `ARTIFACTS_API` env, falling back to `~/.artifacts/config.json`.

```
artifacts login                       # POST /api/keys with an email, stores key
artifacts publish <dir|file.html> [--id <id>] [--title t] [--json]
artifacts get <id> [--json]
artifacts list [--json]
artifacts versions <id> [--json]
artifacts rollback <id> <version>
artifacts comments <id> [--open|--all] [--since ts] [--json]
artifacts reply <id> <thread> "<text>"
artifacts resolve <id> <thread>
artifacts delete <id>
```

- `publish` on a directory writes `<dir>/.artifacts.json` `{id, api}` so the
  next publish in that directory updates in place; `--id` overrides. A single
  `.html` file is published as `index.html`.
- Preflight + upload only missing blobs. Excludes `.git`, `node_modules`,
  `.artifacts.json`.
- `--json` emits one JSON object on stdout, exit code non-zero on any error,
  error JSON on stderr. This is the surface agents use.

## 9. Agent skill (`SKILL.md`)

Shipped in the repo at `skill/SKILL.md` and served at
`https://share.<domain>/skill.md`. Teaches: publish, share the URL, poll
`comments --open --json`, reply, republish, resolve. Includes the design
constraints for pages (self-contained HTML, relative asset paths, allowed CDNs).

## 10. Error handling

- Every handler returns typed errors mapped to the table in section 4;
  unexpected errors log with stack and request id, return `INTERNAL`.
- Publish is transactional: blobs are written first (idempotent by sha256),
  then the version row and files rows in one DB transaction, then
  `current_version` is advanced. A crash mid-way leaves orphan blobs for GC,
  never a half-visible version.
- Viewer endpoints validate body sizes, strip HTML from comment bodies
  (rendered as text), and reject anchors over 4 KB.
- Rate limits return `Retry-After`.
- CLI: network errors retry 3× with backoff on idempotent calls
  (preflight, GET); publish is retried only with the same `Idempotency-Key`.

## 11. Testing

- Go: unit tests per package (storage backends against a temp dir and MinIO
  in CI, anchor-independent comment logic, key hashing, limits). HTTP tests
  through `httptest` covering every route and every error code. Target 80%+.
- Runtime: Playwright tests against a served fixture artifact: select text →
  comment → reload → highlight present; republish with changed text → drifted
  and orphaned states; dark-page fixture.
- CLI: unit tests for file collection and manifest; integration test against
  the Go server binary started in-process (`go build` in CI).
- End-to-end smoke in CI: start server (sqlite+local), `artifacts publish`
  fixture, curl the page, post a viewer comment, `artifacts comments --json`
  shows it, `artifacts rollback`, page serves old content.

## 12. Out of scope for v1

Web console, accounts/OAuth, billing and metering, custom domains, email
verification of keys, subdomain-per-artifact isolation, password-protected
artifacts, real-time comment updates (polling only). Each is a later slice on
the same API.

## 13. Repository layout

```
artifacts/
  cmd/artifacts-server/      main.go
  internal/api/              handlers, middleware, errors
  internal/store/            sqlite + postgres via one interface, migrations/
  internal/blob/             local + s3 backends
  internal/serve/            /a/* streaming, injection, CSP, ETag
  internal/comments/         threads, anchors validation
  runtime/                   comments.js source + build (esbuild) → embedded
  cli/                       Node package `artifacts`
  skill/SKILL.md
  deploy/docker-compose.yml  deploy/RUNBOOK.md  deploy/fly.toml  Dockerfile
  docs/superpowers/specs/
```
