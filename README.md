# artifacts

A self-hostable service for publishing and sharing small static web apps
("artifacts") as versioned, commentable pages. It stores each version's
files content-addressed by sha256, injects a lightweight comments runtime
into HTML at serve time, and exposes a simple key-authenticated API for
publishing, listing, rolling back, and moderating threaded feedback anchored
to the rendered page.

See the full design in
[`docs/superpowers/specs/2026-09-15-artifacts-service-design.md`](docs/superpowers/specs/2026-09-15-artifacts-service-design.md)
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
# => {"id":"...","url":"http://localhost:8080/a/.../","version":1}
```

Open the returned `url` in a browser to view the published page.

## API

Base: `PUBLIC_API_ORIGIN`. Auth: `Authorization: Bearer <key>` on all
`/api/*` routes except `POST /api/keys`. Viewer routes under `/c/*` on the
share origin take no auth; the artifact id is the capability.

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
| `POST` | `/c/:id/comments/:thread/seen` | Viewer beacon. `{version, anchor_status}` → 204. Records where the runtime re-anchored the thread; ignored unless `version` is the current one. |
| `GET` | `/c/:id/comments?status=open\|all&version=n` | What the runtime renders: open threads with raw anchors. The server never resolves anchors; the runtime does. |
| `GET` | `/c/:id/meta` | Viewer. `{current_version, versions:[n]}` so the runtime can render a version switcher. |
| `GET` | `/a/:id/*` | Current version. `/a/:id/v/:n/*` pins a version. Directory paths resolve to `index.html`. |
| `GET` | `/healthz` | |

Errors: `{"error":{"code","message","request_id"}}`. Codes:
`INVALID_ARGUMENT` 400, `UNAUTHORIZED` 401, `FORBIDDEN` 403, `NOT_FOUND` 404,
`GONE` 410, `PAYLOAD_TOO_LARGE` 413, `RATE_LIMITED` 429, `QUOTA_EXCEEDED` 429,
`INTERNAL` 500. Every response carries `X-Request-Id`.

Limits (managed free tier): 25 MB per version, 500 files, 10 publishes/min
per key, 200 artifacts per unverified key, 20 versions kept, 2 KB comment body,
20 comments/min per IP on `/c/*`.

## Comments runtime

Every served HTML page gets `<script src="/_rt/comments.js" data-artifact="…" data-version="…" defer>`
injected before `</body>`. The runtime is vanilla JS with no dependencies, lives
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
| `ADMIN_TOKEN` | (empty) | Self-host: the one bearer key; disables `POST /api/keys`. One of `ADMIN_TOKEN` or `KEY_SIGNING_SECRET` is required. |
| `KEY_SIGNING_SECRET` | (empty) | Managed mode: presence enables issuing keys via `POST /api/keys`. |
| `PUBLIC_SHARE_ORIGIN` | `http://localhost:8080` | Origin used for generated artifact/comment links and CORS. |
| `PUBLIC_API_ORIGIN` | `http://localhost:8080` | Origin used for API links and CORS. |
| `TRUST_PROXY_HEADERS` | `false` | Bucket per-IP rate limits by `CF-Connecting-IP` / `X-Forwarded-For` instead of the peer address. Set `true` **only** when the server is reachable exclusively through a proxy that overwrites those headers (the managed box behind Cloudflare does). Leaving it on for a directly-reachable server lets anyone bypass IP rate limits by varying the header. |
| `LIMIT_VERSION_BYTES` | `26214400` (25 MB) | Max bytes per published version. |
| `LIMIT_FILES` | `500` | Max files per version. |
| `LIMIT_VERSIONS_KEPT` | `20` | Older versions are GC'd beyond this. |
| `LIMIT_PUBLISH_PER_MIN` | `10` | Publishes per key per minute. |
| `LIMIT_ARTIFACTS_PER_KEY` | `200` | Artifacts per unverified key. |
| `LIMIT_COMMENT_BYTES` | `2048` | Max comment body size. |
| `LIMIT_COMMENTS_PER_MIN` | `20` | Comments per IP per minute on `/c/*`. |
| `LIMIT_ANCHOR_BYTES` | `4096` | Max serialized anchor size. |

## Deploying

- Self-host: `deploy/docker-compose.yml` (above).
- Managed (Hetzner + Cloudflare + Neon + R2) and the Fly.io upgrade path:
  see [`deploy/RUNBOOK.md`](deploy/RUNBOOK.md).

## Development

```
go run ./cmd/artifacts-server
CGO_ENABLED=0 go test ./...
./scripts/smoke.sh   # end-to-end smoke test against a locally started server
```
