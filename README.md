# artifacts

A self-hostable service for publishing and sharing small static web apps
("artifacts") as versioned, commentable pages. It stores each version's
files content-addressed by sha256, injects a lightweight comments runtime
into HTML at serve time, and exposes a simple key-authenticated API for
publishing, listing, rolling back, and moderating threaded feedback anchored
to the rendered page. A tenant dashboard manages artifacts, API keys, usage,
and integrator workspaces. Integrators can give their customers a read-only
portal without requiring those customers to sign in.

See the full design in
[`docs/superpowers/specs/2026-09-15-artifacts-service-design.md`](docs/superpowers/specs/2026-09-15-artifacts-service-design.md),
the [tenant dashboard extension](docs/superpowers/specs/2026-09-16-tenants-dashboard-portal-design.md),
and operational procedures in [`deploy/RUNBOOK.md`](deploy/RUNBOOK.md).

## Self-host quickstart

```
echo "ADMIN_TOKEN=change-me" > .env
docker compose -f deploy/docker-compose.yml --env-file .env up -d
```

Publish a page and open it:

```
H='Authorization: Bearer change-me'
BODY='<html><body><h1>hello world</h1></body></html>'
SHA=$(printf '%s' "$BODY" | shasum -a 256 | cut -d' ' -f1)
printf '%s' "$BODY" > index.html
MAN=$(printf '{"title":"Hello","files":[{"path":"index.html","sha256":"%s","size":%d}]}' "$SHA" "${#BODY}")
curl -sf -H "$H" -F "manifest=$MAN" -F "$SHA=@index.html" localhost:8080/api/artifacts
# => {"id":"...","url":"http://localhost:8080/a/.../","version":1,"end_user":null}
```

Open the returned `url` in a browser to view the published page.

This API-only setup keeps `ADMIN_TOKEN` as the key for the `admin` tenant.
For the dashboard, configure `KEY_SIGNING_SECRET` and a sign-in provider:
`EMAIL_PROVIDER=log` for local development, `EMAIL_PROVIDER=resend` with
`RESEND_API_KEY` and `EMAIL_FROM` for delivered email, or the GitHub client
ID and secret. Without a provider, `/app/*` returns 503 and the API still works.

## Landing page

The bare root of the share origin (`PUBLIC_SHARE_ORIGIN/`) serves a landing page:
the publish command, a screenshot of the dashboard with a comment thread inset
over it, the publish-and-comment loop as a diagram with three lines under it, and
the two ways to start, with this deployment's own origins filled in. The
screenshots are embedded static assets (`internal/app/static/dashboard-*.webp`
and `thread-*.webp`, a light and a dark one of each, swapped with the reader's
theme), served content-hashed and cached like the stylesheet.

On the API origin the root still redirects to `/app/`, so a sign-in email lands
on the dashboard. With no web app mounted (`Web` unset), the root stays a JSON 404.

## Dashboard and customer portals

Open `PUBLIC_API_ORIGIN/app/login`, sign in, and create a named API key at
`PUBLIC_API_ORIGIN/app/keys`. The full key is shown once. The dashboard lists
artifacts, and each artifact has a detail page at `/app/artifacts/{id}` with
its share link, a preview, the kept versions (with one-click rollback), and
the comment threads, where you can reply as the owner and resolve them. It
also lets you delete artifacts, rotate keys, inspect usage, and rename the
workspace. Any active key in a workspace can manage all of its artifacts.
Revoking one key does not affect artifacts or other keys in that workspace.

Settings can create an integrator workspace. Publish with `--end-user <id>`
to attribute an artifact to your customer, then use `portal-link <id>` to
create a link to just that customer's artifacts. End-user IDs are opaque,
1 to 128 printable ASCII characters. Republishing cannot reassign an artifact
to a different customer. Portal links last seven days, require no account or
cookie, and stop working when `KEY_SIGNING_SECRET` is rotated.

The web app and portals live on the API origin. Use separate API and share
origins for web-enabled deployments: published HTML must never share an
origin with an authenticated dashboard. Sessions use an HttpOnly cookie
scoped to `/app`, with Secure enabled on HTTPS.

Usage shows publishes, retained version storage, page views, and open comments
for the current and previous calendar months in UTC. Views count current-page
200 and 304 responses and flush every 30 seconds and at shutdown. Storage and
open comments use the month cutoff; previous totals reflect retained data, so
pruned versions cannot be reconstructed.

## CLI

The `cli/` directory is the CLI package, published to npm as
[`byagent`](https://www.npmjs.com/package/byagent). Markdown rendering uses `marked`.

```
npm install -g byagent        # or: npx byagent <command>
export ARTIFACTS_API=http://localhost:8080 ARTIFACTS_TOKEN=change-me
byagent publish ./site --title "Hello" --json
# => {"id":"...","url":"http://localhost:8080/a/.../","version":1,"end_user":null,"files":2,"bytes":1841,"uploaded":2}
```

| Command | What it does |
| --- | --- |
| `login --api <url> [--token <key>]` | Verifies a key created at `<url>/app/keys`, then writes `~/.artifacts/config.json` (the token may be piped on stdin) |
| `publish <dir\|file.html> [--id <id>] [--title <t>] [--end-user <id>] [--private\|--public] [--comments\|--no-comments] [--project <name>] [--tag <t>]... [--cover <file>] [--cover-style <name>]` | Uploads missing blobs and publishes a version, optionally attributed to a customer. `--no-comments` hides the comment panel from viewers and closes the viewer API for that artifact; `--comments` turns it back on; neither keeps the current setting. `--project` and repeatable `--tag` label the artifact for the gallery and are remembered in `.artifacts.json`. `--cover` is an svg/png/jpg/webp shown as the card cover and `og:image` (a file inside the directory is used in place, any other is copied in as `_cover.<ext>`); without one the server generates a quiet OG-style card in `--cover-style` (`dots`, `grid`, `lines`, `wash`, `rings`, `plain`; default derived from the id) |
| `get <id>` | URL, title, current version, open comment count, project and tags; the share code and its link when one is set |
| `share <id> [--off]` | Issues a six-digit share code for a private artifact and prints it with a one-click link. Running it again rotates the code, stopping every link that carried the old one; `--off` withdraws it |
| `list [--search <text>] [--end-user <id>] [--project <name>]` | Workspace artifacts, optionally narrowed by a case-insensitive title match, one customer or one project; prints project and tags |
| `whoami` | Current tenant ID, kind and name, plus the key ID and name |
| `portal-link <end-user id>` | Seven-day read-only customer portal URL, integrator workspaces only |
| `versions <id>` | Version history with per-version comment counts |
| `rollback <id> <version>` | Republishes an old version as the newest one |
| `comments <id> [--open\|--all] [--since <rfc3339>]` | Threads with their anchor status and comments |
| `reply <id> <thread> "<text>"` | Posts an owner reply on a thread |
| `resolve <id> <thread>` | Marks a thread resolved |
| `delete <id>` | Soft-deletes the artifact; the URL stops working |
| `hooks install\|uninstall claude\|codex [--scope user\|project]` | Adds (or removes) a PostToolUse hook in Claude Code or Codex that nudges the agent to publish, or republish, `.html` and `.md` pages it writes. It never publishes by itself; once per file, five per session at most; `BYAGENT_NUDGE=0` silences it |

Config comes from `ARTIFACTS_TOKEN` / `ARTIFACTS_API`, else
`~/.artifacts/config.json`. Every command takes `--json` and then prints exactly
one JSON object on stdout; errors print one JSON object on stderr and exit
non-zero (2 for usage and configuration problems, 1 for a failed call).
Publish and list JSON include `end_user`, the external ID or `null`, plus
`visibility` (`public` or `private`), `project`, `tags`, `cover_url` and `cover_style`.
`portal-link --json` returns `{url, expires_at}`; its default output is just
the URL. `whoami --json` returns `{tenant:{id,kind,name},key:{id,name}}`.

Use `byagent publish ./site --private` for workspace-only viewing, and
`byagent publish ./site --public` to make it public. These flags are mutually
exclusive. New artifacts default to public; republishing without either flag
preserves the server's current visibility, including changes made in the dashboard.
The API manifest accepts `"visibility":"private"` or `"visibility":"public"`;
omission preserves it on update, and other string values are rejected.

Private publish/get/list responses return an app-origin URL,
`<PUBLIC_API_ORIGIN>/app/view/<id>/`, and an authenticated `cover_url` under that
path. Open the URL while signed in to the owning workspace. Other workspaces,
anonymous visitors, end-user portals, and public `/a/` and `/c/` routes cannot
retrieve private content, including pinned versions, assets, covers, or viewer
comments. Private viewing requires separate app and share origins. The browser
viewer is sandboxed and has no viewer comment panel; owners can still manage
comments through the dashboard and authenticated API. API keys publish and manage
private artifacts but do not authenticate the browser viewer.

A private artifact can also carry a **share code**: six digits that let anyone
holding them read it, and comment on it, without signing in. Create, rotate or
withdraw the code with `byagent share <id>` (again to rotate, `--off` to stop)
or on the artifact's dashboard page; `GET /api/artifacts/:id`
returns `share_code` and `share_url` while one is set, and `byagent get <id>`
prints both. List responses never include it.

There are two ways in, both opening the same artifact and nothing else. The
link `https://<share origin>/a/<id>/?k=123456` verifies the code, moves it into
a cookie scoped to that artifact, and redirects to the clean URL, so the code
leaves the address bar and the browser history. Or send the code by itself: the
artifact's address shows a page asking for it. A viewer who has neither sees
that same page, which does reveal that something is there — unlike a private
artifact with no code, which gives nothing away.

Six digits is a million possibilities, so the server, not the code, is what
stops a guesser: wrong codes are counted against the artifact, not the client,
and ten of them within fifteen minutes stop every further attempt from anyone
until the window passes. Rotating the code revokes every link and every
unlocked browser that carried the old one, for that artifact alone. Making an
artifact public clears its code. A code cannot reach anything else in the
workspace, and it never grants the dashboard.

Private responses use `Cache-Control: private, no-store`. Public artifact content
requires cache revalidation so changing visibility blocks subsequent requests.
Making an artifact private cannot revoke copies already downloaded or cached
before this upgrade. Making it public exposes its retained versions as well.

Publishing a directory writes `<dir>/.artifacts.json` with the artifact id and
the API it belongs to, so the next `publish` of that directory updates the same
URL. `--id` overrides it, and the link file is ignored when it names a different
API. `.git`, `node_modules` and `.artifacts.json` are never uploaded. A single
`.html` file is published as `index.html`.

Tests: `cd cli && npm test` (unit plus an e2e suite that builds and drives the
real server).

## Agent skill

[`skill/SKILL.md`](skill/SKILL.md) teaches an agent the whole loop: publish,
hand over the URL, poll `comments --open --json`, reply, republish, resolve. It
also carries the page design constraints (self-contained HTML, relative asset
paths, the CDNs the page CSP allows). It is embedded in the binary and served at
`GET /skill.md` on the share origin, so an agent can fetch it from a running
deployment.

## API

Base: `PUBLIC_API_ORIGIN`. Auth: `Authorization: Bearer <key>` on all
owner routes under `/api/*`. `POST /api/keys` has been removed and always
returns 404, including with `ADMIN_TOKEN`. Create keys in the app at
`PUBLIC_API_ORIGIN/app/keys`. Viewer routes under `/c/*` on the share origin
take no auth; the artifact id is the capability.

| Method | Path | Notes |
|---|---|---|
| `GET` | `/api/me` | `{tenant:{id,kind,name},key:{id,name}}`. |
| `POST` | `/api/end-users/{external_id}/portal-link` | `{url,expires_at}`. Integrator tenant required; URL uses the API origin and lasts seven days. |
| `POST` | `/api/artifacts/preflight` | `{files:[{path, sha256, size}]}` → `{missing:[sha256]}`. Lets the CLI upload only new blobs. |
| `POST` | `/api/artifacts` | multipart: part `manifest` (JSON `{title?, visibility?, comments_enabled?, project?, tags?, cover?, cover_style?, files:[{path, sha256, size}]}`) then one part per missing blob named by sha256. Optional `X-Artifact-End-User` header. Returns `{id,url,version:1,end_user,visibility,comments_enabled,project,tags,cover,cover_url,cover_style}`. Requires root `index.html`. `project` is 1 to 64 characters (trimmed; `null` or empty clears), `tags` up to 10 strings of 1 to 32 characters (lowercased, deduplicated), `cover` the path of an svg/png/jpg/webp in the same manifest, `cover_style` one of `dots`, `grid`, `lines`, `wash`, `rings`, `plain`. Bad values return 400 `INVALID_ARGUMENT` naming the field. |
| `PUT` | `/api/artifacts/:id` | Same body and optional header. New version; returns the same shape with `version:n`. A different end user returns 403 `FORBIDDEN`. `comments_enabled` (boolean, optional) switches viewer comments; omitted keeps the current setting. Omitted `project`, `tags`, `cover` and `cover_style` keep their values; `null` clears `project` or `cover`, `[]` clears `tags`. |
| `GET` | `/api/artifacts/:id` | `{id, title, url, current_version, versions_count, open_comments, visibility, comments_enabled, end_user, project, tags, cover, cover_url, cover_style, created_at, updated_at, published_at}`. `published_at` equals `created_at`; `cover_style` is the effective generator name even when the publisher never chose one. |
| `GET` | `/api/artifacts?limit&cursor&end_user&project` | Tenant's artifacts, newest first. Items have the same shape as `GET /api/artifacts/:id`; `end_user` is an external ID or `null`; `project` filters by exact label. |
| `GET` | `/api/projects` | `{projects:[{name,count}]}`: the workspace's project labels with live artifact counts, most used first. |
| `DELETE` | `/api/artifacts/:id` | Soft delete; page returns 410; blobs GC'd later. |
| `GET` | `/api/artifacts/:id/versions` | `[{version, created_at, bytes, files, comment_count}]` |
| `POST` | `/api/artifacts/:id/share-code` | `{action}`: `issue` or `rotate` for a fresh six-digit code, `revoke` to withdraw it. Returns `{id, visibility, share_code, share_url}`, both null once revoked. Private artifacts only; anything else is 400. |
| `POST` | `/api/artifacts/:id/rollback/:version` | Creates version n+1 with version v's file set. |
| `GET` | `/api/artifacts/:id/comments?status=open\|resolved\|all&since=<rfc3339>` | Threads with anchors, `anchor_status`, version. |
| `POST` | `/api/artifacts/:id/comments/:thread/reply` | `{body}` → owner reply, displayed as "Author". |
| `POST` | `/api/artifacts/:id/comments/:thread/resolve` | Marks resolved. |
| `POST` | `/c/:id/comments` | Viewer. `{author_name, body, anchor, version}` → thread. `Idempotency-Key` header honoured. Every `/c/*` route answers 404 while the artifact is private or its `comments_enabled` is false; owner routes keep working so old threads stay readable. |
| `POST` | `/c/:id/comments/:thread/reply` | Viewer. `{author_name, body}`. |
| `POST` | `/c/:id/comments/:thread/seen` | Viewer beacon. `{version, anchor_status}` → 204. Records where the runtime re-anchored the thread; ignored unless `version` is the current one. |
| `GET` | `/c/:id/comments?status=open\|all&version=n` | What the runtime renders: open threads with raw anchors. The server never resolves anchors; the runtime does. |
| `GET` | `/c/:id/meta` | Viewer. `{current_version, versions:[n]}` so the runtime can render a version switcher. |
| `GET` | `/a/:id/*` | Current version. `/a/:id/v/:n/*` pins a version. Directory paths resolve to `index.html`, which is served with `og:title`, `og:image` (the cover URL) and `twitter:card` meta tags injected. |
| `GET` | `/a/:id/_cover.svg` | The cover: the publisher's `cover` file from the current version when present, else a generated 1200 x 630 SVG card (title, project, share host and publish date over a subtle `cover_style` pattern). `Cache-Control: public, max-age=3600` with an ETag; 404 / 410 like pages. |
| `GET` | `/healthz` | |

Errors: `{"error":{"code","message","request_id"}}`. Codes:
`INVALID_ARGUMENT` 400, `UNAUTHORIZED` 401, `FORBIDDEN` 403, `NOT_FOUND` 404,
`GONE` 410, `PAYLOAD_TOO_LARGE` 413, `RATE_LIMITED` 429, `QUOTA_EXCEEDED` 429,
`INTERNAL` 500. Every response carries `X-Request-Id`.

Limits (managed free tier): 25 MB per version, 500 files, 10 publishes/min
per key, 200 artifacts per tenant, 20 versions kept, 2 KB comment body,
20 comments/min per IP on `/c/*`.
Email sign-in is limited to 5 requests/hour per address and 20/hour per IP;
key creation to 20/hour per tenant; portal links to 60/minute per tenant.

## Comments runtime

Every served HTML page gets `<script src="/_rt/comments.js" data-artifact="…" data-version="…" defer>`
injected before `</body>` on public artifacts, unless comments are switched off for that artifact
(`byagent publish --no-comments`, `comments_enabled:false` in the manifest,
or the Comments switch on the dashboard's artifact page), in which case the
page is served exactly as published. The runtime is vanilla JS with no dependencies, lives
entirely in a shadow root, and fails closed: if the comments API is unreachable
the page renders exactly as published.

Viewers can select text or point at an element to leave an anchored comment under
a display name; no account is involved. Anchors are re-resolved in the browser on
every load and reported back as `attached`, `drifted` or `orphaned`, which is what
`GET /api/artifacts/:id/comments` shows the publisher.

Working on it:

```bash
cd runtime
npm install
npm test        # node:test units for anchor matching
npm run build   # esbuild -> dist/comments.js (committed, 25 KB budget)
npm run e2e     # Playwright against a real server
```

`runtime/dist/comments.js` is committed so `go build` never needs Node. CI rebuilds
it and fails if the checked-in copy is stale.

## Environment reference

| Variable | Default | Notes |
|---|---|---|
| `ADDR` | `:8080` | Listen address. |
| `DB` | `sqlite` | `sqlite` or `postgres`. |
| `DATABASE_URL` | (empty) | Required when `DB=postgres`. |
| `STORAGE` | `local` | `local` or `s3`. |
| `DATA_DIR` | `./data` | SQLite file + local blob storage root when `STORAGE=local`. |
| `S3_ENDPOINT` | (empty) | Required when `STORAGE=s3`. |
| `S3_BUCKET` | (empty) | Required when `STORAGE=s3`. |
| `S3_ACCESS_KEY` | (empty) | Required when `STORAGE=s3`. |
| `S3_SECRET_KEY` | (empty) | Required when `STORAGE=s3`. |
| `S3_USE_SSL` | `true` | Set `false` for local/dev S3-compatible endpoints (e.g. MinIO). |
| `ADMIN_TOKEN` | (empty) | API key for the `admin` personal tenant. No web login. One of `ADMIN_TOKEN` or `KEY_SIGNING_SECRET` is required for API-only use. |
| `KEY_SIGNING_SECRET` | (empty) | Required for the web app and portal links, even with `ADMIN_TOKEN`. Rotation invalidates all existing portal links. |
| `GITHUB_CLIENT_ID` | (empty) | Enables GitHub sign-in together with `GITHUB_CLIENT_SECRET`. |
| `GITHUB_CLIENT_SECRET` | (empty) | GitHub OAuth app secret; configure callback `PUBLIC_API_ORIGIN/app/login/github/callback`. |
| `EMAIL_PROVIDER` | unset, disabled | Explicit `log` enables development email sign-in; `resend` delivers email. With no provider configured, `/app/*` returns 503. |
| `RESEND_API_KEY` | (empty) | Required when `EMAIL_PROVIDER=resend`. |
| `EMAIL_FROM` | (empty) | Sender for delivered email, for example `artifacts <login@example.com>`. |
| `PUBLIC_SHARE_ORIGIN` | `http://localhost:8080` | Origin used for generated artifact/comment links and CORS. |
| `PUBLIC_API_ORIGIN` | `http://localhost:8080` | Origin for API links, `/app/*`, `/p/*`, and the GitHub callback. Keep separate from the share origin when enabling web sign-in. |
| `TRUST_PROXY_HEADERS` | `false` | Bucket per-IP rate limits by `CF-Connecting-IP` / `X-Forwarded-For` instead of the peer address. Set `true` **only** when the server is reachable exclusively through a proxy that overwrites those headers (the managed box behind Cloudflare does). Leaving it on for a directly-reachable server lets anyone bypass IP rate limits by varying the header. |
| `LIMIT_VERSION_BYTES` | `26214400` (25 MB) | Max bytes per published version. |
| `LIMIT_FILES` | `500` | Max files per version. |
| `LIMIT_VERSIONS_KEPT` | `20` | Older versions are GC'd beyond this. |
| `LIMIT_PUBLISH_PER_MIN` | `10` | Publishes per key per minute. |
| `LIMIT_ARTIFACTS_PER_TENANT` | `200` | Live artifacts per tenant, shared across its keys. |
| `LIMIT_ARTIFACTS_PER_KEY` | (empty) | Deprecated fallback when `LIMIT_ARTIFACTS_PER_TENANT` is unset; logs a deprecation notice. |
| `LIMIT_COMMENT_BYTES` | `2048` | Max comment body size. |
| `LIMIT_COMMENTS_PER_MIN` | `20` | Comments per IP per minute on `/c/*`. |
| `LIMIT_ANCHOR_BYTES` | `4096` | Max serialized anchor size. |

## Deploying

- Self-host: `deploy/docker-compose.yml` (above).
- Managed (Hetzner + Cloudflare + Neon + R2) and the Fly.io upgrade path:
  see [`deploy/RUNBOOK.md`](deploy/RUNBOOK.md).

## Development

```
ADMIN_TOKEN=local-dev go run ./cmd/artifacts-server
CGO_ENABLED=0 go test ./...
./scripts/smoke.sh   # end-to-end smoke test against a locally started server
```
