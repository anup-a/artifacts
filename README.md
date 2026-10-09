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

Next to it, in the same layout, are `/pricing`, `/privacy`, `/terms`,
`/security`, `/subprocessors` and `/acceptable-use` (`internal/app/site.go`,
templates in `internal/app/templates/`), plus `/pricing.md` for agents. The
policies carry an effective date and version; bump both in `site.go` when the
text changes. The limits on the pricing page come from the server's config.
The link preview card is `/og-card.png`, rendered from `scripts/og-card.html`.

## Dashboard and customer portals

Open `PUBLIC_API_ORIGIN/app/login` and sign in. To give an agent a key,
use `PUBLIC_API_ORIGIN/app/connect`: it makes a prompt with a one-time code
(ten minutes, one use) that `byagent login --code` trades for a key named
after the agent, and it is where `byagent login --browser` sends you to
approve a CLI. Neither puts a key in the chat. For CI, create a named key at
`PUBLIC_API_ORIGIN/app/keys`; the full key is shown once. The dashboard lists
artifacts, and each artifact has a detail page at `/app/artifacts/{id}` with
its share link, a preview, the kept versions (each with Preview, which opens
it for you without making it live, Restore and Delete), and
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
origin with an authenticated dashboard. Sessions use an HttpOnly,
SameSite=Lax cookie. Over HTTPS it is `__Host-artifacts_session` (Secure,
`Path=/`, no `Domain`), so no other host, page hosts under the same domain
included, can set or overwrite it; the old name (`artifacts_session` at
`/app`) is still read for sessions that existed before the rename, for one
release. Over plain http it is `artifacts_session` at `/app`.

### One origin per page (`CONTENT_DOMAIN`)

Without it, every public page is served from the share origin at
`/a/<id>/`, so one page's script runs in the same origin as every other page:
it can fetch a page the visitor unlocked, post comments as that visitor, and
set cookies for the share origin's domain. Set `CONTENT_DOMAIN` and every
page gets its own origin instead, like GitHub Pages. byagent.dev sets it to
its own share host, `CONTENT_DOMAIN=byagent.dev`, so pages live at
`https://<slug>.byagent.dev/`. The slug is 16 random lower-case base32
characters, unrelated to the id (ids are case-sensitive and DNS is not).

Two layouts work:

- **The share host itself** (`byagent.dev`). Only a name that is exactly one
  slug label (`<16 base32 chars>.byagent.dev`) is a page host. The apex,
  `www`, `app`, `domains` and every other name keep their normal routes.
  Page hosts are same-site with byagent.dev and app.byagent.dev, so the app's
  own cookies are `__Host-` (host-only, a page cannot plant or read them) and
  any state-changing request to byagent's own hosts whose `Origin` is a page
  host is refused with 403.
- **A domain of its own** (`byagent.page`). It must not overlap the share or
  API host. Its apex (and `www.`) redirects to the share origin and any other
  name under it answers 404. Listing it on the Public Suffix List later would
  make each page host its own site ([draft request](docs/psl-byagent-page.md)).

- A page host serves that page and nothing else: the page at `/`, pinned
  versions at `/v/<n>/`, the cover at `/_cover.svg`, the share-code form at
  `/_unlock`, the comment runtime at `/_rt/comments.js` and the page's own
  comment routes at `/c/<its id>/`. A route naming another page's id answers
  404, and the API, dashboard and agent pages are not there. `/_proxy/` is
  the page's proxy routes (same-origin only).
- `/a/<id>/...` on the share origin 308-redirects to the page host with the
  sub-path and query kept, so old links and share links (`?k=`) keep working;
  the code then unlocks on the page host. A private page nobody outside the
  workspace can open is not redirected (it answers 404 as before). The legacy
  `/c/<id>/` routes stay on the share origin for old runtimes.
- Cookies on a page host are host-only: `__Host-artifact_unlock` (Secure,
  `Path=/`, no `Domain`), HttpOnly, SameSite=Lax. Over plain http (local work)
  the name drops the prefix.
- Every page host is same-site with the others (and, under byagent.dev, with
  byagent's own hosts), so SameSite does not stop one page posting to
  another with its cookies. Every state-changing request on a page host must
  carry that page's own `Origin`, or, when `Origin` is absent or `null`,
  `Sec-Fetch-Site: same-origin`; anything else is refused with 403.
- Every link the server makes (API `url`, `share_url`, `cover_url`, version
  `url`, analytics, dashboard, showcase, collections, `og:url`/`og:image`)
  comes from one helper, `internal/pageurl`. The owner API also accepts a
  page's slug wherever it takes an id.

Leave it unset and nothing changes: pages stay under `/a/<id>/`. For local
work, `CONTENT_DOMAIN=http://localhost:8080` (pages at `<slug>.localhost`, the
byagent.dev layout) or `CONTENT_DOMAIN=http://page.localhost:8080` (a domain
of its own) next to `PUBLIC_SHARE_ORIGIN=http://localhost:8080` works without
DNS, since browsers resolve every `*.localhost` name to the machine. DNS, certificates and the
tunnel for production are in [deploy/RUNBOOK.md](deploy/RUNBOOK.md).

Usage shows publishes, retained version storage, page views, and open comments
for the current and previous calendar months in UTC. Views count current-page
200 and 304 responses and flush every 30 seconds and at shutdown. Storage and
open comments use the month cutoff; previous totals reflect retained data, so
pruned versions cannot be reconstructed.

Each artifact's detail page has a Views card: views and unique visitors over
24 hours, 7, 30 or 90 days or all time, a small bar chart (hourly for 24h),
and the top referrers, countries, paths and devices; the artifacts list shows
each page's views over the last 30 days. A view is a GET for an HTML page of
the current version that the server actually served (200 or 304, after any
share-code gate) to a browser: `Accept` must include `text/html` and the user
agent must look like a browser and not like a crawler, link previewer,
monitor, headless browser or HTTP library. HEAD, prefetches, assets, pinned
versions, the owner's `/app/view` and the dashboard preview frame (marked
`?byagent_preview=1`) are not counted. No cookies are set and no IP address
or user agent is stored. Unique visitors are counted in memory with an HMAC
of the client address and user agent under a random key that is replaced at
the start of every UTC day and never persisted, so a visitor counts once per
page per day, a range adds up the days, and a restart forgets the day's set.
Referrers are reduced to a host name; country comes from Cloudflare's
`CF-IPCountry` only when `TRUST_PROXY_HEADERS=true`. Each page keeps at most
50 values per day per breakdown and folds the rest into `(other)`. Daily rows
are kept for the life of the page, hourly rows for 48 hours; delete erases
all of them.

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
| `login --code <code> [--api <url>] [--name <n>]` | Trades a one-time code from `<url>/app/connect` for a key named after the agent (`POST /api/connect`) and writes `~/.artifacts/config.json` |
| `login --browser [--no-wait] [--timeout <s>]`, `login --wait` | Asks for an approval link (`POST /api/connect/device`), then polls `POST /api/connect/device/token` until a signed-in person approves it; `--no-wait` exits after printing the link, `--wait` resumes (exit 3 while pending) |
| `login --api <url> [--token <key>]` | Verifies a key created at `<url>/app/keys`, then writes `~/.artifacts/config.json` (the token may be piped on stdin) |
| `publish <dir\|file.html> [--id <id>] [--title <t>] [--end-user <id>] [--private\|--public] [--comments\|--no-comments] [--project <name>] [--tag <t>]... [--cover <file>] [--cover-style <name>]` | Uploads missing blobs and publishes a version, optionally attributed to a customer. `--no-comments` hides the comment panel from viewers and closes the viewer API for that artifact; `--comments` turns it back on; neither keeps the current setting. `--project` and repeatable `--tag` label the artifact for the gallery and are remembered in `.artifacts.json`. `--cover` is an svg/png/jpg/webp shown as the card cover and `og:image` (a file inside the directory is used in place, any other is copied in as `_cover.<ext>`); without one the server generates a quiet OG-style card in `--cover-style` (`dots`, `grid`, `lines`, `wash`, `rings`, `plain`; default derived from the id) |
| `get <id>` | URL, title, current version, open comment count, project and tags; the share code and its link when one is set |
| `share <id> [--off]` | Issues a six-digit share code for a private artifact and prints it with a one-click link. Running it again rotates the code, stopping every link that carried the old one; `--off` withdraws it |
| `access <id> [--public\|--password\|--restricted --allow <email\|@domain>...\|--add <entry>...\|--off] [--invite <email>...]` | Shows or sets who can open the page (see Page access below). `--password` reads the password from stdin or a hidden prompt, never argv. `publish --password` publishes privately and then sets one |
| `list [--search <text>] [--end-user <id>] [--project <name>]` | Workspace artifacts, optionally narrowed by a case-insensitive title match, one customer or one project; prints project and tags |
| `whoami` | Current tenant ID, kind and name, plus the key ID and name |
| `portal-link <end-user id>` | Seven-day read-only customer portal URL, integrator workspaces only |
| `versions <id>` | Version history with per-version comment counts, the live version marked, and an owner-only preview link per version |
| `rollback <id> <version>` | Makes an old version live again (a pointer flip: nothing is re-uploaded, and visibility, share code, expiry and comments stay as they are) |
| `version rm <id> <version>` | Deletes one old version for good; the live version cannot be deleted |
| `diff <id> <from> [<to>]` | Files added, removed and changed between two versions; `<to>` defaults to the live one |
| `comments <id> [--open\|--all] [--since <rfc3339>]` | Threads with their anchor status and comments; `--since` keeps threads started after that time |
| `comments <id> --wait [--timeout <s>] [--interval <s>] [--since <rfc3339>]` | Blocks until a reader leaves a new comment (new thread or reply), prints those threads and exits; owner replies never wake it. Polls every 20 s and gives up after 1800 s by default with `timed_out: true` |
| `reply <id> <thread> "<text>"` | Posts an owner reply on a thread |
| `resolve <id> <thread>` | Marks a thread resolved |
| `delete <id>` | Soft-deletes the artifact; the URL stops working |
| `analytics [<id>] [--range 24h\|7d\|30d\|90d\|all]` | Views and unique visitors of one page, or of every page in the workspace without an id, over the range (default `30d`): a per-day series (per hour for `24h`), top referrers, countries, paths and devices, and the top pages for the workspace |
| `vars` | Account variables: names and the hosts each may be sent to. Values are never returned |
| `vars set NAME --upstream <host> [--upstream <host>]... [--from-env VAR]` | Stores an API key for proxy routes. The value is read from stdin (piped, or typed at a hidden prompt) or from the named environment variable, never from an argument. At least one host is required |
| `vars rm NAME` | Deletes a variable; routes that use it stop working |
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
viewer is sandboxed and has no viewer comment panel. Because a sandboxed page's
own requests carry no session cookie, opening a private HTML page redirects to
`/app/view/<id>/~cap/<capability>/`: a signed, 24-hour capability for that
artifact and version, under which the page's relative images, styles and
scripts load. Navigating to a capability address still needs the session;
owners can still manage
comments through the dashboard and authenticated API. API keys publish and manage
private artifacts but do not authenticate the browser viewer.

A private artifact can also carry a **share code**: six digits that let anyone
holding them read it, and comment on it, without signing in. Create, rotate or
withdraw the code with `byagent share <id>` (again to rotate, `--off` to stop)
or on the artifact's dashboard page; `GET /api/artifacts/:id`
returns `share_code` and `share_url` while one is set, and `byagent get <id>`
prints both. List responses never include it.

There are two ways in, both opening the same artifact and nothing else. The
link `https://<share origin>/a/<id>/?k=123456` (with `CONTENT_DOMAIN`,
`https://<slug>.<content domain>/?k=123456`) verifies the code, moves it into
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

### Page access

Besides public, private and the share code, a page can take a **password** or
be **restricted** to a list of people. Readers never need an account, and
readers who get in can comment and suggest edits like code holders.

- `byagent access <id> --password` (password on stdin) makes the page private
  and asks visitors for the password once per browser. Only an argon2id hash is
  stored. Guesses are metered per page and client address first (10 per 15
  minutes), then per page overall as a backstop no single address can reach
  (30 per 15 minutes); a client past its own budget never spends the page's.
  When every password check slot is busy for two seconds the gate answers 503
  and asks the visitor to try again. Changing access is metered too (30 a
  minute per key or dashboard user). The unlock is an HttpOnly cookie
  scoped to that page's `/a/<id>/` and `/c/<id>/` paths, signed over the page,
  the password hash and an access version, so changing the password or the
  mode signs every visitor out.
- `byagent access <id> --restricted --allow sam@acme.com --allow @acme.com`
  admits only those addresses, or any address at exactly that domain. A
  visitor enters their email; if it is on the list the server mails a six-digit
  code and a one-time link (15 minutes, five guesses per code). Mail is
  metered per client and address on every request, and per page (50 an hour),
  per workspace and for the whole server (`LIMIT_ACCESS_MAIL_PER_WORKSPACE_DAY`,
  `LIMIT_ACCESS_MAIL_PER_DAY`) only when a code is actually sent. The gate
  gives the same answer, in the same time, whether or not the address is
  listed or a budget is spent: the list is checked and the code stored and
  mailed after the answer has gone. Taking someone off the list takes them off
  the page at once. Comments made through the gate are signed with the
  address the reader proved and carry a "verified" mark set by the server; a
  name typed anywhere else never gets it. Restricted pages need
  `EMAIL_PROVIDER` and `KEY_SIGNING_SECRET`.
- `--invite <email>` mails people on the list by name a link to the page
  (50 per request, 100 a day per workspace). Domain entries are never mailed.
- `--off` is workspace-only (restricted with an empty list); `--public` opens
  the page to anyone with the link and drops any password or list.

One door at a time: a password or a list withdraws a share code, and a share
code cannot be issued while either is set. Guest pages get none of this.

**Limits of the gate.** Every published page is served from the same origin.
A script on another published page, opened in the same browser as an
unlocked visitor, can act as that visitor: read a password, restricted or
share-code page they have opened, and comment on it as them. The gate keeps
out people who have not got in; it does not isolate one page from another in
the same browser. A separate origin per page is planned.

Private responses use `Cache-Control: private, no-store`. Public artifact content
requires cache revalidation so changing visibility blocks subsequent requests.
Making an artifact private cannot revoke copies already downloaded or cached
before this upgrade. Making it public exposes its retained versions as well:
pinned `/a/:id/v/:n/` addresses are public on a public page, on purpose, so the
comment panel can show readers the version a comment was made on. To take one
version's content down for good, delete that version.

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
| `GET` | `/api/artifacts/:id/versions` | `{id, current_version, versions:[{version, created_at, bytes, files, comment_count, agent, model, key, current, restored_at, url, preview_url}]}`, oldest first. `url` is the pinned address (`/a/:id/v/:n/` for a public page, the signed-in viewer for a private one). `preview_url` is `<PUBLIC_API_ORIGIN>/app/view/:id/v/:n/`: it opens that version only for a signed-in member of the owning workspace, sandboxed and never counted as a view. It is `null` for guest keys and on servers without sign-in. |
| `GET` | `/api/artifacts/:id/analytics?range=24h\|7d\|30d\|90d\|all` | `{id, url, range, bucket, from, to, first_view_day, totals:{all_time_views, range_views, range_visitors}, series:[{bucket, views, visitors}], top_referrers:[{referrer, views}], top_countries:[{country, views}], top_paths:[{path, views}], top_devices:[{device, views}]}`. `range` defaults to `30d`; anything else is 400. `bucket` is `hour` for `24h` (RFC 3339 hour starts) and `day` otherwise (UTC dates), zero-filled. `referrer` is a host, `(direct)` or `(other)`. Visitors are unique per page per day. Guest keys get 403. |
| `GET` | `/api/analytics?range=...` | The same report across every live page of the workspace, with `top_pages:[{id, title, url, views, visitors}]` and `top_paths` items carrying `id`. Visitors add up per page. |
| `GET` | `/api/variables` | `{variables:[{name, allowed_upstreams, created_at, updated_at}], limit}`. Values are write-only and never returned. |
| `PUT` | `/api/variables/:name` | `{value, allowed_upstreams:[host]}` → `{name, allowed_upstreams, updated_at, created}`. Creates or replaces. Names are an uppercase letter then uppercase letters, digits and `_` (64 max); values up to 4 KB with no control characters; 1 to 10 hosts, each a host name (a URL is reduced to its host; IPs, ports and single-label names are refused). 50 per workspace (429 past it). Guest keys get 403; 503 while variables are off. |
| `DELETE` | `/api/variables/:name` | `{name, deleted:true}`; 404 when absent. |
| `GET`/`POST`/`PUT`/`PATCH`/`DELETE` | `/a/:id/_proxy/:route[/:sub]` | Runs a proxy route declared in the live version's `.byagent/proxy.json` (see [Proxy routes](#proxy-routes)). |
| `POST` | `/api/artifacts/:id/share-code` | `{action}`: `issue` or `rotate` for a fresh six-digit code, `revoke` to withdraw it. Returns `{id, visibility, share_code, share_url}`, both null once revoked. Private artifacts only; anything else is 400. |
| `POST` | `/api/artifacts/:id/versions/:version/restore` | Makes that version live again by pointing the page at it: no files are copied and no version is added. Visibility, share code, expiry, the comments switch, title and labels stay as they are. Returns `{id, url, version, current_version, previous_version, restored_at, visibility, comments_enabled}`. 409 `CONFLICT` if it is already live, 404 if it was deleted or pruned. The next publish takes the next unused number. `POST /api/artifacts/:id/rollback/:version` is the same call. |
| `DELETE` | `/api/artifacts/:id/versions/:version` | Deletes one version that is not live. Its pinned URL stops answering at once; its files are erased by the hourly GC unless another version or page holds the same bytes. Threads made on it are kept and marked `orphaned`. Returns `{id, deleted_version, current_version, versions_count, orphaned_comments}`. 409 `CONFLICT` for the live version. Version numbers are never reused. |
| `GET` | `/api/artifacts/:id/diff?from=a&to=b` | What changed between two versions, by content hash: `{id, from, to, added:[{path,size}], removed:[{path,size}], changed:[{path,from_size,to_size}], unchanged}`. `to` defaults to the live version. Not a text diff. |
| `GET` | `/api/artifacts/:id/access` | `{id, mode, visibility, password_set, allowed_emails, allowed_domains, access_version, url, share_url}`. `mode` is `anyone_with_link` (public, or private with a share code, which then also returns `share_code`), `password`, or `restricted` (an empty list means workspace-only). The password is never returned. |
| `PATCH` | `/api/artifacts/:id/access` | `{mode, password?, allowed_emails?, allowed_domains?}`. `anyone_with_link` makes the page public. `password` needs `password` (8 to 128 characters) unless one is set already. `restricted` replaces both lists whole (read, merge, write to add one); domains are given as `acme.com`. Guest keys get 403; restricted with entries needs mail on the server (503 otherwise). Returns the GET shape. |
| `POST` | `/api/artifacts/:id/access/invites` | `{emails:[...]}` (1 to 50). Mails each address that is on the allowlist by name a link to the page. Returns `{notifications:{sent:[email], failed:[{email,error}], skipped:[{email,reason}]}}`; `reason` is `not_allowed` or `invalid`. |
| `GET` | `/api/artifacts/:id/comments?status=open\|resolved\|all&since=<rfc3339>` | Threads with anchors, `anchor_status`, version. |
| `POST` | `/api/artifacts/:id/comments/:thread/reply` | `{body}` → owner reply, displayed as "Author". |
| `POST` | `/api/artifacts/:id/comments/:thread/resolve` | Marks resolved. |
| `POST` | `/c/:id/comments` | Viewer. `{author_name, body, anchor, version}` → thread. `Idempotency-Key` header honoured. Every `/c/*` route answers 404 while the artifact is private or its `comments_enabled` is false; owner routes keep working so old threads stay readable. |
| `POST` | `/c/:id/comments/:thread/reply` | Viewer. `{author_name, body}`. |
| `POST` | `/c/:id/comments/:thread/seen` | Viewer beacon. `{version, anchor_status}` → 204. Records where the runtime re-anchored the thread; ignored unless `version` is the current one. |
| `GET` | `/c/:id/comments?status=open\|all&version=n` | What the runtime renders: open threads with raw anchors. The server never resolves anchors; the runtime does. |
| `GET` | `/c/:id/meta` | Viewer. `{current_version, versions:[n]}` so the runtime can render a version switcher. |
| `GET` | `/a/:id/*` | Current version. `/a/:id/v/:n/*` pins a version. Directory paths resolve to `index.html`, which is served with `og:title`, `og:image` (the cover URL), `og:url` and `twitter:card` meta tags injected. With `CONTENT_DOMAIN` set these 308 to the page's own host, where the same paths sit at the root (`/`, `/v/:n/`, `/_cover.svg`, `/_unlock`, `/_access/...`). |
| `POST` | `/a/:id/_unlock` | Gate form: `code` for a share code, or `password` on a password page. 303 to the page with the unlock cookie, or the gate again. |
| `POST` | `/a/:id/_access/email`, `/a/:id/_access/code` | Restricted gate: ask for a code by `email`, then send `email` and `code`. |
| `GET`/`POST` | `/a/:id/_access/link?t=` | The mailed link. GET shows a button (so mail scanners do not spend it); the POST spends it and sets the cookie. |
| `GET` | `/a/:id/_cover.svg` | The cover: the publisher's `cover` file from the current version when present, else a generated 1200 x 630 SVG card (title, project, share host and publish date over a subtle `cover_style` pattern). `Cache-Control: public, max-age=3600` with an ETag; 404 / 410 like pages. |
| `GET` | `/healthz` | |
| `GET` | `/api/domains` | `{domains:[domain]}`: the workspace's custom domains with their mounts. A domain is `{domain, status, verified, ssl_status, is_apex, url, dns_instructions:[{type, host, name, value, purpose}], verification?:{state, message}, mounts:[mount], created_at, verified_at, checked_at}`; `status` is `pending`, `active` or `error`. Every domain route answers 404 when the server has no `DOMAINS_PROVIDER`, and 403 for a guest key. |
| `POST` | `/api/domains` | `{domain}` → 201 with the domain, or 200 with the existing one when this workspace already connected it. 400 for anything that is not a hostname someone can own (IPs, ports, paths, wildcards, byagent's own hosts), 409 `CONFLICT` when another workspace has it, 429 `QUOTA_EXCEEDED` past `LIMIT_DOMAINS_PER_TENANT`. |
| `GET` | `/api/domains/:domain` | The domain; a pending one is checked again on the way (at most every 15 seconds). Poll this until `status` is `active`. |
| `DELETE` | `/api/domains/:domain` | 204. Unmounts every page on it and removes it from the edge. |
| `GET` | `/api/domains/:domain/mounts` | `{domain, mounts:[{domain, path, artifact_id, url, primary, created_at}]}`. |
| `POST` | `/api/domains/:domain/mounts` | `{path, artifact_id}` → the mount. `path` is `/` (or empty) for the root, else up to five URL-safe segments, none starting with `_`. Mounting at a path that is taken points it at the new page. 50 pages per domain. |
| `DELETE` | `/api/domains/:domain/mounts?path=/x` | 204. No `path` is the root. |
| `GET` | `/api/artifacts/:id/primary-domain` | `{id, primary: mount\|null, primary_url, eligible:[mount]}`. |
| `POST` | `/api/artifacts/:id/primary-domain` | `{domain, path?}` makes that mount primary: `/a/:id/*` then answers 308 to the same path under it (query kept). `{domain: null}` stops. The domain must be `active` (409 otherwise); `path` is needed when the page is mounted twice on it. A private page without a share code is never redirected. |

Errors: `{"error":{"code","message","request_id"}}`. Codes:
`INVALID_ARGUMENT` 400, `UNAUTHORIZED` 401, `FORBIDDEN` 403, `NOT_FOUND` 404,
`CONFLICT` 409, `GONE` 410, `PAYLOAD_TOO_LARGE` 413, `RATE_LIMITED` 429, `QUOTA_EXCEEDED` 429,
`INTERNAL` 500, and on proxy routes `METHOD_NOT_ALLOWED` 405, `REQUEST_TIMEOUT` 408,
`BAD_GATEWAY` 502, `UNAVAILABLE` 503 and `GATEWAY_TIMEOUT` 504. Every response carries `X-Request-Id`.

Limits (managed free tier): 25 MB per version, 500 files, 10 publishes/min
per key, 200 artifacts per tenant, 20 versions kept, 2 KB comment body,
20 comments/min per IP on `/c/*`.
Email sign-in is limited to 5 requests/hour per address and 20/hour per IP;
key creation to 20/hour per tenant; portal links to 60/minute per tenant.

## Proxy routes

A page can call an API whose key it never sees. The workspace stores the key
as a **variable** (`byagent vars set`, `PUT /api/variables/:name`, or
Settings > Variables in the dashboard), pinned to the hosts it may be sent to.
The page ships `.byagent/proxy.json`:

```json
{
  "routes": {
    "chat": {
      "upstream": "https://openrouter.ai/api/v1/chat/completions",
      "methods": ["POST"],
      "headers": { "Authorization": "Bearer {{OPENROUTER_API_KEY}}" },
      "query": { "key": "{{OTHER_KEY}}" },
      "paths": ["/models", "/v1/*"]
    }
  }
}
```

and calls `fetch("_proxy/chat", …)` from its own origin. The server reads the
manifest from the page's live version, adds the variables and forwards the call,
streaming the answer back.

Manifest schema: a top-level `routes` object keyed by route name (1 to 64 of
`a-z 0-9 - _`, at most 20). Each route has `upstream` (required; `https`, default
port, a host name, no user info, fragment, dot segments or `{{…}}`), `methods`
(required; GET, POST, PUT, PATCH, DELETE; `"method": "POST"` also works),
`headers` and `query` (optional maps whose values may hold `{{NAME}}`; at most
4 distinct variables per route), `paths` (optional sub-paths callers may append:
exact, or a prefix ending in `/*`, with letters, digits and `-._~` only;
without it the route is the upstream URL alone), and `body` (optional rules for
a JSON object body: `"pin": {"model": "x"}` sets fields whatever the caller
sent, `"max": {"max_tokens": 1024}` caps numbers and fills them in when
missing, and `"allow_only": ["messages", "stream"]` turns on strict mode,
refusing (400) a body with any top-level field that is not pinned, capped or
listed; such bodies are limited to 1 MB and apply to every method, so a
route with rules needs a JSON object body except on a DELETE sent without
one). Without `allow_only`, `pin` and `max` constrain only the fields they
name: a visitor can add others the upstream bills for, such as `n`,
`max_completion_tokens` or `tools`. A top-level
`limit_per_hour` sets the owner's own cap on calls to the page's routes (the
server's per-page limit still applies). Unknown fields are errors. The file is
at most 64 KB. Publishing an invalid manifest fails with 400 naming the
problem; a valid one adds `proxy: {routes, warnings}` to the publish response,
warning about variables that are missing or not pinned to a route's host. A
publish for an end user (`X-Artifact-End-User`) that ships a manifest is
refused with 403 unless its manifest JSON sets `"proxy_routes_for_end_user":
true`. `_proxy/` is a reserved path in uploads, and `.byagent/` files are never
served and cannot be the cover.

Routes run only for a live version whose manifest passed these checks when it
was published. A version published before routes existed, by a guest key that
was later claimed, or for an end user without the opt-in never runs them, and
restoring such a version does not switch them on.

Security rules, all enforced on the server:

- Values are sealed with AES-256-GCM under `VARIABLES_KEY`, bound to their
  workspace and name, and never returned by the API, the CLI or the dashboard.
  Error messages and logs name variables, never values.
- A variable is sent only to a route whose upstream host is one of its
  `allowed_upstreams` or a subdomain of one (whole labels: `openai.com` admits
  `api.openai.com`, never `evilopenai.com`). Public suffixes and hosts shared
  by strangers (`supabase.co`, `workers.dev`, `amazonaws.com`, `co.uk`) cannot
  be listed; name the service's own host (`xyz.supabase.co`). Otherwise
  nothing is sent.
- Upstreams are `https` on port 443 only. The server resolves the host itself,
  refuses it if any address is private, loopback, link-local (including the
  169.254.169.254 metadata service), CGNAT, multicast, reserved, or an IPv6 form
  of those (mapped, IPv4-compatible, SIIT, NAT64, 6to4, Teredo, SRv6, unique
  local, documentation), dials the vetted
  address, and checks the socket address again as it connects. Redirects are
  never followed (502). Routes may not point at this server's own hosts, and a
  call carrying `X-Byagent-Proxy` (one the proxy sent) is refused.
- Only `Content-Type`, `Accept` and `Accept-Language` pass upstream from the
  browser; only `Content-Type`, `Content-Language`, `ETag`, `Last-Modified`,
  `Retry-After` and rate-limit headers come back. Cookies, `Set-Cookie`,
  hop-by-hop, CORS and forwarding headers never cross. Answers carry
  `Cache-Control: no-store` and a sandboxing CSP, and an upstream that echoes a
  secret has it replaced with `[redacted]`.
- Request bodies up to 4 MB, read whole within 10 s (408 otherwise) before
  the call takes a slot, so a client sending slowly holds no slot; at most
  128 MB of bodies are held at once. Answers up to 10 MB; 60 s per call, and
  the upstream must start answering within 20 s. At most 128 calls run at
  once across the server, 16 per workspace and 32 per upstream site (its
  registrable domain, so subdomains share one cap); past that a call gets 503
  with `Retry-After`. Calls are metered per client
  address (`LIMIT_PROXY_PER_IP_PER_HOUR`, default 100; IPv6 clients by their
  /64) and per page (`LIMIT_PROXY_PER_PAGE_PER_HOUR`, default 1000, or the
  manifest's lower `limit_per_hour`). Only calls that reach a real route and
  method, resolve their variables, pass the body rules and get a slot spend
  the page's budget; a 503 for a busy server costs nothing.
- Browsers may call a route only from its own origin: a cross-site
  `Sec-Fetch-Site` or a foreign `Origin` gets 403.
- Methods are limited to what the route declares (405 otherwise), and sub-paths
  to its `paths`; a sub-path segment holds letters, digits and `-._~` only (no
  percent escapes, `;` or non-ASCII).
- A visitor whose call fails on configuration (a missing, unreadable or
  unpinned variable, a blocked upstream address) sees only `route
  unavailable`; which variable or host was at fault goes to the server log.
- A private page's routes answer only browsers that have opened the page the
  way its access mode asks: its share code, its password, or a verified
  address still on its allowlist (the same grant cookie as the page;
  changing the password or the list cuts routes off with the page;
  `/app/view` pages cannot call them). Guest workspaces cannot store variables or publish routes. Deleted
  pages answer 410.
- Without `VARIABLES_KEY`, or with `VARIABLES=off`, the variables API answers
  503 and routes answer 404.

Anyone who can open a page can call its routes. The server keeps the key
secret, not the endpoint, so set a spending limit on keys used this way.

Limits of these protections, worth knowing before storing a key:

- The same-origin check keeps other sites from calling a page's routes, but
  until per-page origins are on (`CONTENT_DOMAIN`, each page on its own
  host) every page shares the share origin, so a script on one page can call
  another page's routes as if it were that page. The per-page budget and
  limits still apply; isolation between pages comes only with per-page
  origins.
- Redaction replaces only exact copies of a secret in an answer. An upstream
  that returns it encoded (base64, URL- or JSON-escaped, split, hashed) gets
  it through. The real protection is pinning each variable to the hosts it
  belongs to, so it is never sent anywhere that would echo it.
- Store keys that can only do what the page needs: a scoped, spend-limited
  key for one API. Never store provisioning, management or admin keys (ones
  that create other keys, change billing or manage an account): a visitor
  can use whatever the route lets through.

## Custom domains

A workspace can serve its pages on hostnames it owns, at the root
(`https://docs.example.com/`) or at paths (`/guide`), like here.now's mounts.
`byagent domain add docs.example.com --page <id> [--path /guide]` connects the
hostname and prints two DNS records: a CNAME to `DOMAINS_CNAME_TARGET` (an
ALIAS or flattened CNAME at an apex, or A/AAAA records when
`DOMAINS_APEX_IPS` is set) and a TXT record `_byagent.<host>` =
`byagent-verify=<token>`. byagent checks them every minute or so while a domain
is new, and on every `GET /api/domains/:domain`.

Ownership is the TXT record. Until it is seen the hostname serves nothing and
is not even sent to the certificate provider, so a name cannot be parked by
someone who does not control its DNS; one still unverified after seven days is
removed. Once verified, the provider (below) issues the certificate and the
domain goes `active`.

On a custom domain a request is matched to the longest mount path and served
exactly as `/a/<id>/` is: visibility, the share-code, password and
restricted gates (`_unlock`, `_access/...` under the mount; their cookies
scoped to the mount and the page's `/c/<id>/` routes), pinned versions under
`v/<n>/`, `_cover.svg`, proxy routes under `_proxy/`, security headers and the
comment runtime. The runtime and the viewer comment routes are served on the
domain itself, same origin, for the pages mounted there only. The domain
serves nothing else: no API, no dashboard, no other page. Private pages
nobody outside the workspace can open answer 404 there too. Every
state-changing request on a custom domain must carry the domain's own
`Origin` (or `Sec-Fetch-Site: same-origin` when `Origin` is absent or
`null`), else 403, as on a page host. Views count against the page like any
other view. A primary domain makes the page's `/a/<id>/` addresses 308 to it;
it wins over the page-host redirect (`CONTENT_DOMAIN`) for document
navigations, while frames, images and fetches (`Sec-Fetch-Dest` other than
`document`) go to the page host when there is one, else stay on the share
origin, so dashboard previews and embeds keep working.

Providers (`DOMAINS_PROVIDER`): `cloudflare` registers each verified hostname
as a Cloudflare for SaaS custom hostname on the zone, `fly` asks the Fly app for
a certificate (the API form of `fly certs add`), `manual` leaves certificates
to the operator, `none` is for tests and for TLS handled outside byagent.
Unset turns custom domains off. Setup steps are in
[deploy/RUNBOOK.md](deploy/RUNBOOK.md#custom-domains-cloudflare-for-saas).

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
| `CONTENT_DOMAIN` | (empty) | Gives every page its own origin, `https://<slug>.<CONTENT_DOMAIN>/` (see [One origin per page](#one-origin-per-page-content_domain)). Either the share host itself (`byagent.dev`: only exact `<slug>.byagent.dev` names are pages) or a domain that does not overlap the share or API host (`byagent.page`). A bare domain takes the share origin's scheme; an origin (`http://page.localhost:8080`) sets scheme and port. Empty keeps pages under `/a/<id>/`. |
| `TRUST_PROXY_HEADERS` | `false` | Bucket per-IP rate limits by `CF-Connecting-IP` / `X-Forwarded-For` instead of the peer address. Set `true` **only** when the server is reachable exclusively through a proxy that overwrites those headers (the managed box behind Cloudflare does). Leaving it on for a directly-reachable server lets anyone bypass IP rate limits by varying the header. Page analytics also read the visitor address and `CF-IPCountry` from these headers only when it is `true`. |
| `SHOWCASE_COLLECTION` | (empty) | Share slug of the collection `/showcase` lists (the part after `/s/` in its shared link). Only its public pages show, with their comments. Empty shows an empty showcase. |
| `SHOWCASE_PAGES` | (empty) | Comma-separated page ids for `/showcase`, in order, from any project. Wins over `SHOWCASE_COLLECTION`. Pages that are gone or not public are skipped. |
| `LIMIT_VERSION_BYTES` | `26214400` (25 MB) | Max bytes per published version. |
| `LIMIT_FILES` | `500` | Max files per version. |
| `LIMIT_VERSIONS_KEPT` | `20` | Older versions are GC'd beyond this. |
| `LIMIT_PUBLISH_PER_MIN` | `10` | Publishes per key per minute. |
| `LIMIT_ARTIFACTS_PER_TENANT` | `200` | Live artifacts per tenant, shared across its keys. |
| `LIMIT_ARTIFACTS_PER_KEY` | (empty) | Deprecated fallback when `LIMIT_ARTIFACTS_PER_TENANT` is unset; logs a deprecation notice. |
| `LIMIT_COMMENT_BYTES` | `2048` | Max comment body size. |
| `LIMIT_COMMENTS_PER_MIN` | `20` | Comments per IP per minute on `/c/*`. |
| `LIMIT_ANCHOR_BYTES` | `4096` | Max serialized anchor size. |
| `LIMIT_ACCESS_MAIL_PER_WORKSPACE_DAY` | `200` | Sign-in codes a restricted page's gate mails per workspace per day. Past it, requests get the usual answer and no mail. |
| `LIMIT_ACCESS_MAIL_PER_DAY` | `2000` | The same cap for the whole server. |
| `VARIABLES_KEY` | (empty) | Turns on account variables and proxy routes. Base64 of 32 random bytes (`openssl rand -base64 32`); comma-separate keys to rotate (the first seals, all open). Unset, the feature is off. Losing it loses every stored value. |
| `VARIABLES` | (empty) | `off` turns variables and proxy routes off while keeping `VARIABLES_KEY` set. |
| `LIMIT_PROXY_PER_IP_PER_HOUR` | `100` | Proxy route calls per client address per hour, across pages. |
| `LIMIT_PROXY_PER_PAGE_PER_HOUR` | `1000` | Proxy route calls per page per hour, across addresses. |
| `LIMIT_DOMAINS_PER_TENANT` | `5` | Custom domains per workspace. |
| `DOMAINS_PROVIDER` | unset, off | `cloudflare`, `fly`, `manual` or `none`. See Custom domains. |
| `DOMAINS_CNAME_TARGET` | (empty) | Required with a provider: the hostname customers CNAME to, e.g. `domains.byagent.dev`. Defaults to `<FLY_APP_NAME>.fly.dev` for `fly`. |
| `DOMAINS_APEX_IPS` | (empty) | Comma-separated addresses an apex domain should point A/AAAA records at. Empty tells apex owners to use ALIAS / CNAME flattening. |
| `DOMAINS_DNS_RESOLVER` | `1.1.1.1:53` | DNS server ownership TXT records are read from, or `system`. |
| `CLOUDFLARE_API_TOKEN` | (empty) | Required for `cloudflare`: a token with Zone > SSL and Certificates > Edit on the zone. |
| `CLOUDFLARE_ZONE_ID` | (empty) | Required for `cloudflare`: the zone holding the CNAME target. |
| `FLY_API_TOKEN` | (empty) | Required for `fly`. |
| `FLY_APP_NAME` | (empty) | Required for `fly`. |

## Deploying

- Self-host: `deploy/docker-compose.yml` (above).
- Production (one OVHcloud VPS behind a Cloudflare Tunnel), plus the older
  managed and Fly.io setups: see [`deploy/RUNBOOK.md`](deploy/RUNBOOK.md).

## Development

```
ADMIN_TOKEN=local-dev go run ./cmd/artifacts-server
CGO_ENABLED=0 go test ./...
./scripts/smoke.sh   # end-to-end smoke test against a locally started server
```
