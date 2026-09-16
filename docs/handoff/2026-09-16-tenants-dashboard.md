# Handoff: tenants, dashboard, portal and comments panel C

Date: 2026-09-16. Written for whoever picks the project up next (human or agent).
Read this top to bottom before touching anything.

## Where things stand

| Branch | Commits ahead of main | State |
| --- | --- | --- |
| `feat/tenants-dashboard` | 10 | pushed, build/vet/test green, smoke OK, e2e 12/12, coverage 80.2% |
| `feat/comments-panel-c` | 3 | pushed, e2e green, direction C comments panel |

Both branches merge cleanly onto `origin/main` and onto each other (checked with
`git merge-tree`, zero conflict markers). Neither is deployed. `share.creatica.app`
still runs the pre-tenant build.

Commit list on `feat/tenants-dashboard` (`git log --oneline main..HEAD`):

```
872b6a2 fix: keep timestamp cells on one line in dashboard and portal tables
3ba1c89 test: raise coverage across store, api, apierr and blob
de38a70 test: dashboard end-to-end flow and skill notes for keys, whoami and portal links
6f297ff feat: mount the web app, usage counter and graceful shutdown in the server
2d68536 feat: web app with sign-in, dashboard pages and end-user portal
97d0cc2 feat: auth package with sessions, CSRF, GitHub OAuth and magic links
4beed1f feat: buffered page view counter and serve hook
8c424e8 fix: adapt api to tenant-scoped store and add me/portal-link endpoints
ef0de29 wip: tenants store layer, portal tokens and CLI account command (Codex, cut off by usage limit)
012b9d6 docs: tenants, integrator dashboard and end-user portal design
```

Commit list on `feat/comments-panel-c`:

```
a290cb8 feat: refined comments side panel (direction C)
2f84ded feat: relative time and avatar helpers for the comments panel
8dbf78b test: e2e coverage for the refined comments side panel
```

## What was built

- Spec: `docs/superpowers/specs/2026-09-16-tenants-dashboard-portal-design.md`.
- `internal/store`: tenants, users, memberships, tenant-scoped keys, end users,
  portal tokens, blobs, usage. One SQL layer for sqlite and postgres. Both SQL
  dialects live in `internal/store/migrations`.
- `internal/auth`: sessions, CSRF, GitHub OAuth, email magic links.
- `internal/app`: the web dashboard. Go `html/template` plus `go:embed`, one
  stylesheet `static/app.css`, one small vanilla script `static/app.js`. No inline
  scripts or styles (CSP). Pages: login, artifacts, end users, keys, usage,
  settings, and the end-user portal at `/p/{token}`.
- `internal/api`: tenant-scoped API, `GET /api/me`, `POST /api/end-users/{id}/portal-link`.
  `POST /api/keys` was removed. Keys are created in the dashboard at `/app/keys`.
- `internal/usage`: buffered page view counter, flushed on graceful shutdown.
- CLI: `publish --end-user <id>`, `whoami`, `portal-link`. Docs updated in
  `README.md`, `deploy/RUNBOOK.md`, `skill/SKILL.md` and `--help`.
- Runtime: direction C comments side panel on `feat/comments-panel-c`.

## How it was verified

- `go build ./... && go vet ./... && go test ./...` green.
- Coverage 80.2% total (api 80.0, apierr 100, app 81.8, auth 85.4, blob 80.7,
  config 84.9, ids 94.4, portal 95.7, ratelimit 95.5, serve 88.3, store 80.0,
  usage 91.2, cmd 0).
- `./scripts/smoke.sh` prints `SMOKE OK`.
- `cd runtime && npm run e2e`: 12 passed (11 comments, 1 dashboard flow).
- `cd cli && npm test`: 32 pass.
- Real browser: login, magic link sign-in, artifacts, keys, settings, usage and
  the portal were driven in Chrome against a local build. Integrator-workspace
  pages were not screenshotted in Chrome because switching workspace is a form
  POST; the Playwright dashboard flow asserts them instead.

## Decisions taken where the spec was silent

- Revoking a key twice is idempotent. The second revoke just redirects.
- End-user ids are path-escaped in dashboard form actions.
- Portal links minted from the dashboard are valid for 7 days.
- `str` and `path` template helpers exist for nullable strings and path escaping.
- `KeyLimiter` was dropped from `api.Deps`. Key creation limits live in the app.
- The 503 "unconfigured" page keeps its specific message. Other 5xx pages show a
  generic line.
- The dashboard e2e uses port 8098 so it can run next to the comments suite on 8099.
- Graceful shutdown flushes the view counter before exit.
- Timestamp cells never wrap (`when` class in `app.css`).
- Each browser session starts in the personal workspace. `/app/end-users` on a
  personal workspace renders a 404 explaining end users exist only in integrator
  workspaces.

## Tasks ready to pick up

Each is independent. Work on a branch off `feat/tenants-dashboard`, test first,
keep files under 800 lines, no em-dashes, commit with `git commit --no-verify`,
never rebase or squash, and do not deploy or call the live server.

1. **Idempotency map never sweeps.** `internal/api/comments.go` around line 186.
   The per-process map remembers viewer thread creations for 10 minutes but only
   evicts on read. Add a bounded sweep (on `put`, or a ticker owned by `RunGC`),
   with a test that proves expired keys are dropped.
2. **S3 Put buffers the whole blob.** `internal/blob/s3.go` line 33 reads the
   body with `io.ReadAll` before uploading. Stream it: hash while copying to
   minio's `PutObject` with a known size, or spool to a temp file above a
   threshold. Keep the checksum mismatch behaviour (`ErrChecksum`). Test with the
   fake S3 in `internal/blob/s3_test.go`.
3. **Base62 modulo bias.** `internal/ids/ids.go` `base62` converts random bytes
   with big-int division, so widths that do not line up with the byte count are
   slightly biased. Either use rejection sampling per character or size the
   random input so `62^width` divides evenly enough to be negligible. Keep the
   existing widths (12 chars for artifact ids, 43 for tokens) and add a
   distribution test.

Already done, do not repeat: the `ListArtifacts` N+1 is gone. Open-comment
counts and end-user ids come from correlated subqueries in the single list query
(`internal/store/sql_artifacts.go`, `artifactCols`).

## Needs Anup's call, not an agent's

- Merge order and deploy. Suggested: merge `feat/comments-panel-c` first (small),
  then `feat/tenants-dashboard`, then one deploy. The deploy loop is in
  `deploy/RUNBOOK.md`. New env vars must be set on Fly before the deploy:
  `KEY_SIGNING_SECRET`, `EMAIL_PROVIDER`, `GITHUB_CLIENT_ID`,
  `GITHUB_CLIENT_SECRET`, `PUBLIC_API_ORIGIN`, `PUBLIC_SHARE_ORIGIN`. Values live
  in `deploy/.env.free` (gitignored) and the Fly secrets, never in git.
- The live `/skill.md` still says `npx artifacts`. The npm name `artifacts`
  belongs to someone else. Pick a package name before publishing the CLI.
- The seeded local database under the session scratchpad has four keys named
  "laptop" from half-run seed scripts. It is throwaway, not a bug.

## Local dev recipe

```
go build -o /tmp/artifacts-server ./cmd/artifacts-server
DB=sqlite STORAGE=local DATA_DIR=/tmp/artifacts-data ADDR=127.0.0.1:8097 \
PUBLIC_SHARE_ORIGIN=http://127.0.0.1:8097 PUBLIC_API_ORIGIN=http://127.0.0.1:8097 \
KEY_SIGNING_SECRET=dev-only EMAIL_PROVIDER=log ADMIN_TOKEN= \
GITHUB_CLIENT_ID= GITHUB_CLIENT_SECRET= /tmp/artifacts-server
```

With `EMAIL_PROVIDER=log` the magic link is printed as a JSON log line containing
`magic_link_url`. Open it in a browser to sign in.
