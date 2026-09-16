# Tenants, integrator dashboard and end-user portal

Date: 2026-09-16. Extends the v1 design
(`2026-09-15-artifacts-service-design.md`), which listed "web console,
accounts/OAuth" as out of scope. This spec pulls them in, on top of the same
API, without changing how agents publish today.

## 1. Purpose

Two audiences that v1 could not serve:

- **Integrators.** A SaaS product (an AI IDE, an agent platform, a chat app)
  publishes artifacts on behalf of *its* users. It needs API keys it can
  rotate, a list of what it published, usage numbers, and a way to show one
  of its customers "all your artifacts" without those customers ever having an
  account with us.
- **Direct users.** A person who runs the CLI or the skill themselves. They
  need to sign in once, create a key, and see everything they published in
  one place.

Both get the same web app. The only difference is what the app shows.

## 2. Decisions (locked)

- **Data model** is `tenant → end_user → artifact`. A direct user is a tenant
  of kind `personal` whose only end user is themselves. One code path.
- **Integrators' customers never sign in to us.** They arrive through a
  signed portal link the integrator generates. No accounts, no cookies.
- **Sign-in for tenants** (integrators and direct users): GitHub OAuth and
  email magic link. Both land in one `identities` table keyed by
  `(provider, subject)`, so a third provider later is a row, not a migration.
  No passwords, ever.
- **The web app lives on the API origin**, under `/app/`, never on the share
  origin. Artifacts are same-origin under `share.<domain>/a/<id>/` and run
  arbitrary HTML, so a session cookie on the share origin would be stealable
  by any published page.
- **Existing publish flow is unchanged.** `Authorization: Bearer <key>` still
  works exactly as before. Integrators add one optional header to attribute a
  publish to one of their users.
- **v1 dashboard scope** is API keys, Artifacts, Usage, End users, Settings.
  Left out on purpose: webhooks, team members and roles, custom domains,
  charts, widget branding, billing (a `plan` column exists so billing can
  attach later without a migration).
- **No frontend framework.** The app is Go `html/template` pages embedded
  with `go:embed`, one hand-written stylesheet, and small vanilla JS where a
  page needs it (copy-to-clipboard, confirm-before-revoke). The single-binary
  deploy story stays true. Quality bar for the look is Vercel/Geist and
  Linear: neutral greys, one accent, 13 to 14 px UI type, dense tables, no
  gradients, no decorative illustration.

## 3. Data model

New tables, migration `0002_tenants.sql`. Both sqlite and postgres.

```
tenants       (id TEXT PK, kind TEXT 'personal'|'integrator', name TEXT,
               plan TEXT DEFAULT 'free', created_at)
users         (id TEXT PK, email TEXT UNIQUE, name TEXT, created_at)
identities    (provider TEXT, subject TEXT, user_id → users, created_at,
               PRIMARY KEY (provider, subject))
memberships   (tenant_id → tenants, user_id → users, role TEXT 'owner',
               created_at, PRIMARY KEY (tenant_id, user_id))
sessions      (id TEXT PK,            -- 32 random bytes base62; sha256 stored
               user_id → users, tenant_id → tenants, created_at, expires_at,
               last_seen_at)
magic_links   (token_sha256 TEXT PK, email TEXT, created_at, expires_at,
               consumed_at)
oauth_states  (state TEXT PK, created_at, expires_at)
end_users     (id TEXT PK, tenant_id → tenants, external_id TEXT,
               first_seen_at, last_seen_at,
               UNIQUE (tenant_id, external_id))
artifact_views(artifact_id → artifacts, day TEXT 'YYYY-MM-DD', count INTEGER,
               PRIMARY KEY (artifact_id, day))
```

Changes to existing tables:

```
keys       + tenant_id → tenants, + name TEXT DEFAULT '', + last_used_at
artifacts  + tenant_id → tenants, + end_user_id → end_users NULL
           + INDEX (tenant_id, created_at), + INDEX (end_user_id, created_at)
```

Migration backfill: every existing key gets its own new tenant of kind
`personal` named after the key's email, and every artifact copies
`tenant_id` from its key. The admin key (`admin@local`, created from
`ADMIN_TOKEN`) becomes tenant `admin`. Nothing published so far changes URL
or ownership.

Ids: `t_` + 12 base62 for tenants, `u_` for users, `eu_` for end users,
`k_` stays the integer key id. Session ids are never logged.

## 4. Attribution: which end user published this

Integrators send one extra header on `POST /api/artifacts` and
`PUT /api/artifacts/:id`:

```
X-Artifact-End-User: <opaque id, 1..128 chars, printable ASCII>
```

The server upserts `end_users (tenant_id, external_id)`, bumps
`last_seen_at`, and stores `end_user_id` on the artifact. The id is the
integrator's own user id; we never interpret it. Missing header means the
artifact belongs to the tenant with no end user, which is also what every
direct user's publish looks like (their personal tenant is the owner).

On `PUT`, a header that names a *different* end user than the artifact
already has is rejected with `403 FORBIDDEN` ("artifact belongs to another
end user"). Reassignment is not a feature.

`GET /api/artifacts` gains `?end_user=<external_id>` and each item gains
`end_user` (the external id or null).

## 5. Portal links (integrators' customers)

Owner endpoint:

```
POST /api/end-users/{external_id}/portal-link   → {url, expires_at}
```

`url` is `PUBLIC_API_ORIGIN/p/<token>`. Token = base64url of
`tenant_id.end_user_id.expires_unix.hmac_sha256(KEY_SIGNING_SECRET, the rest)`.
Lifetime 7 days, fixed. Stateless: nothing stored, nothing to revoke except
by rotating `KEY_SIGNING_SECRET`, which is the documented behaviour.

`GET /p/<token>` renders the portal page (section 7.4) for that end user:
their artifacts, newest first, with title, id, current version, open comment
count, updated time, and a link to the live page. Read-only. No cookie is
set. Cache-Control: `no-store`. An expired or bad token renders a plain
"This link has expired" page with status 410.

`KEY_SIGNING_SECRET` becomes required whenever the web app is enabled
(section 6), including on self-host deployments that set `ADMIN_TOKEN`.

## 6. Sign-in

Enabled when at least one provider is configured. If none is, `/app/*`
returns 503 with a plain page "Sign-in is not configured on this server",
and the API keeps working exactly as v1.

Env:

```
KEY_SIGNING_SECRET      required for /app and /p
GITHUB_CLIENT_ID        optional, enables GitHub sign-in
GITHUB_CLIENT_SECRET
EMAIL_PROVIDER          log | resend        (default log)
RESEND_API_KEY          required when EMAIL_PROVIDER=resend
EMAIL_FROM              e.g. "artifacts <login@share.creatica.app>"
```

`EMAIL_PROVIDER=log` writes the magic link to the server log at INFO with
key `magic_link_url`. That is the development and validation-deploy path;
the Fly box can run this way until Resend is set up.

### 6.1 GitHub

`GET /app/login/github` → store a random `state` (10 min) → redirect to
GitHub with scope `read:user user:email`. `GET /app/login/github/callback`
verifies state, exchanges the code, fetches the primary verified email,
then runs `signIn(provider="github", subject=<github user id>, email, name)`.

### 6.2 Magic link

`POST /app/login/email` with form field `email`. Rate limit 5 per hour per
email and 20 per hour per IP (reuse `ratelimit`). Always responds with the
same "Check your inbox" page whether or not the address is known, so the
form cannot enumerate users. Token = 32 random bytes base62, sha256 stored,
expires in 15 minutes, single use. `GET /app/login/email/verify?token=…`
consumes it and runs `signIn(provider="email", subject=<lowercased email>,
email, name="")`.

### 6.3 signIn

1. Look up `identities(provider, subject)`. If found, load the user.
2. Otherwise look up `users.email`. If found, link the new identity to that
   user (this is how a GitHub user later signs in by email, and vice versa).
   If not found, create the user and a `personal` tenant named after the
   email's local part, with an `owner` membership.
3. Create a session (30 days, sliding: `last_seen_at` refreshed at most once
   per hour, `expires_at` extended to now+30d on refresh). Set cookie
   `artifacts_session` with `HttpOnly; Secure; SameSite=Lax; Path=/app`.
   `Secure` is dropped when `PUBLIC_API_ORIGIN` starts with `http://`
   (local dev only).
4. Redirect to `/app/`.

`POST /app/logout` deletes the session row and clears the cookie.

Session middleware loads the session, the user, and the current tenant.
A user can belong to several tenants (an integrator's owner also has a
personal tenant). `POST /app/switch-tenant` with `tenant_id` updates
`sessions.tenant_id` after checking membership. The header shows the current
tenant name with a switcher when there is more than one.

### 6.4 Becoming an integrator

Settings has "Create an integrator workspace" with a name field. It creates
a tenant of kind `integrator`, adds the owner membership, and switches to
it. That is the whole onboarding. Keys created inside an integrator tenant
are integrator keys; the API does not otherwise distinguish kinds.

### 6.5 CSRF

Every state-changing `/app/*` form carries a hidden `csrf` field: the
session id's sha256 truncated to 32 hex chars, verified with a constant-time
compare. `SameSite=Lax` is defence in depth, not the mechanism.

## 7. The web app

All pages share one layout: left nav (Artifacts, End users, API keys, Usage,
Settings), header with tenant name and switcher, user email, sign-out. On a
`personal` tenant the End users entry is hidden and the Artifacts page is
the whole "portal for end users to see all their artifacts in one place".

Routes (all under `PUBLIC_API_ORIGIN`, session required unless noted):

| Method | Path | Page |
|---|---|---|
| GET | `/app/login` | Sign-in: GitHub button and email form. No session. |
| GET | `/app/` | Redirects to `/app/artifacts` |
| GET | `/app/artifacts` | 7.1 |
| POST | `/app/artifacts/{id}/delete` | Soft delete, then redirect back |
| GET | `/app/end-users` | 7.2 (integrator tenants only, 404 otherwise) |
| POST | `/app/end-users/{external_id}/portal-link` | Creates the link, re-renders the row with it |
| GET | `/app/keys` | 7.3 |
| POST | `/app/keys` | Create; shows the plaintext once on the next render |
| POST | `/app/keys/{id}/revoke` | Sets `revoked_at` |
| GET | `/app/usage` | 7.5 |
| GET | `/app/settings` | 7.6 |
| POST | `/app/settings/tenant` | Rename tenant |
| POST | `/app/settings/integrator` | Create integrator workspace |
| POST | `/app/switch-tenant` | |
| POST | `/app/logout` | |
| GET | `/p/{token}` | 7.4, no session |

### 7.1 Artifacts

Table: title (falls back to the id), id, end user (integrator tenants only,
click filters), version, open comments, updated. Row actions: Open (new
tab, share URL), Delete (confirm in JS, then POST). Filters: `?end_user=`,
`?q=` substring on title. Pagination: 50 per page, cursor on
`(updated_at, id)`. Empty state for a personal tenant explains the two ways
to publish: install the CLI and run `artifacts publish`, or give the agent
`/skill.md`.

### 7.2 End users

Table: external id, artifacts count, first seen, last seen, button
"Portal link". Clicking it POSTs and shows the URL with expiry inline and a
copy button. Support staff use this to see what a customer sees.

### 7.3 API keys

Table: name, prefix (`ak_` + first 6 chars, stored as its own column
`prefix` so no plaintext is needed), created, last used, Revoke. Form at the
top: name → Create. After creation the page shows the full key once inside
a callout with a copy button and the sentence "This is the only time the
key is shown." Revoked keys stay listed, greyed, for 30 days, then the GC
deletes them.

`last_used_at` is updated by `withAuth` at most once per minute per key, so
publishing loops do not turn every request into a write.

### 7.4 Portal (`/p/<token>`)

A single page with the integrator tenant's name as the heading ("Artifacts
from <tenant name>"), then the same table as 7.1 minus the end-user column
and minus Delete. Same stylesheet. Footer says when the link expires.

### 7.5 Usage

Four numbers for the current calendar month (UTC): publishes (versions
created), storage (sum of `bytes` over every live version the tenant owns),
page views (sum of `artifact_views.count`), open comments. Below, the same
four for the previous month. Numbers only. Views are counted in the serve
handler on every 200 or 304 response for `/a/{id}/` and `/a/{id}/index.html`
(current version only), buffered in memory and flushed to `artifact_views`
every 30 seconds and on shutdown.

### 7.6 Settings

Tenant name (editable). Tenant kind and plan (read-only). For a personal
tenant: the "Create an integrator workspace" form. Below: your identities
(GitHub, email) as read-only rows, and Sign out.

## 8. API additions and changes

| Method | Path | Notes |
|---|---|---|
| `POST` | `/api/keys` | **Removed** as an open endpoint. Keys are created in the app. Returns 404 always. |
| `GET` | `/api/artifacts?end_user=` | New filter; items gain `end_user`. |
| `POST` | `/api/artifacts`, `PUT /api/artifacts/:id` | Honour `X-Artifact-End-User` (section 4). |
| `POST` | `/api/end-users/{external_id}/portal-link` | Section 5. Integrator tenants only, else 403. |
| `GET` | `/api/me` | `{tenant:{id,kind,name}, key:{id,name}}`. Lets the CLI print who it is. |

Authorization on every owner route changes from "artifact.key_id == key.id"
to "artifact.tenant_id == key.tenant_id". Any key of a tenant may manage any
of that tenant's artifacts; that is what integrators need when they rotate
keys.

Quota `ArtifactsPerKey` becomes per tenant and is renamed
`LIMIT_ARTIFACTS_PER_TENANT` (the old env name is still read as a fallback,
with a deprecation log line).

## 9. CLI and skill

- `artifacts login` unchanged: paste a key created in the app. The help text
  and the served `/skill.md` point at `PUBLIC_API_ORIGIN/app/keys` as the
  place to get one.
- `artifacts publish --end-user <id>` sets the header. `artifacts list
  --end-user <id>` filters. Both `--json` outputs include `end_user`.
- `artifacts whoami` calls `/api/me`.
- `artifacts portal-link <end-user id>` prints the URL.

## 10. Deploy

- Fly validation box: set `KEY_SIGNING_SECRET` and `GITHUB_CLIENT_ID` /
  `GITHUB_CLIENT_SECRET` as secrets; `EMAIL_PROVIDER` stays `log`. The
  GitHub OAuth app's callback is
  `https://api.share.creatica.app/app/login/github/callback`.
- `ADMIN_TOKEN` keeps working as an API key for the `admin` tenant. It has no
  web login; the operator signs in with GitHub like anyone else and gets a
  personal tenant. Moving the admin tenant's artifacts under a real user is
  a manual SQL step documented in the runbook, not a feature.
- RUNBOOK gains the new env vars and the OAuth app setup.

## 11. Security

- Session cookie only on the API origin, `HttpOnly`, `SameSite=Lax`,
  `Path=/app`. Never on the share origin.
- `/app/*` and `/p/*` responses carry
  `Content-Security-Policy: default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; frame-ancestors 'none'`
  plus `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`,
  `X-Content-Type-Options: nosniff`. Inline scripts and styles are not
  allowed; the app's CSS and JS are served from `/app/static/*` with
  content-hashed names and immutable caching.
- Every template value is escaped by `html/template`. Artifact titles, end
  user ids and tenant names are attacker-controlled strings and must never
  go through `template.HTML`.
- Portal tokens are HMAC-verified with a constant-time compare before any
  database read.
- Magic link and OAuth `state` tokens are single use and expire; sessions
  are looked up by hash, never by the raw id.
- Rate limits: login email 5/h per address and 20/h per IP, key creation
  20/h per tenant, portal link creation 60/min per tenant.

## 12. Errors

App pages render an error page (`error.html`) with the message and the
request id for 4xx and a generic message for 5xx; details go to the log.
API routes keep the v1 JSON envelope and codes. New API codes: none.

## 13. Testing

- Go unit and `httptest` coverage for: migration backfill on a v1 database
  fixture (every artifact gets a tenant, URLs unchanged), `signIn` linking
  rules, session expiry and sliding refresh, CSRF rejection, portal token
  sign/verify including expiry and tamper, `X-Artifact-End-User`
  validation and the 403 on reassignment, tenant-scoped authorization
  (key A of tenant 1 cannot read tenant 2's artifact; key B of tenant 1
  can), view counting and flush, magic link single use, GitHub callback
  against a fake GitHub server. Target stays 80%+.
- Playwright (in `runtime/`, existing setup) against the real binary with
  `EMAIL_PROVIDER=log`: sign in by magic link (read the URL from the log),
  create a key, publish with that key through the CLI, see the artifact on
  `/app/artifacts`, create an integrator workspace, publish with
  `--end-user`, open the end user's portal link in a fresh context and see
  the artifact there and nothing else.
- `scripts/smoke.sh` gains: the old `POST /api/keys` returns 404; a publish
  with `X-Artifact-End-User` shows up under `?end_user=`.

## 14. Out of scope

Team members and roles, webhooks, custom domains, usage charts, widget
branding, billing and plan enforcement, SSO for integrators' customers,
rotating `KEY_SIGNING_SECRET` without invalidating portal links, admin
console for the operator. Each is a later slice on this model.

## 15. Repository layout additions

```
internal/auth/        sessions, identities, signIn, csrf, github, magiclink, email providers
internal/portal/      token sign/verify
internal/app/         html/template handlers for /app/* and /p/*, static assets (go:embed)
internal/app/templates/*.html
internal/app/static/app.css, app.js
internal/store/migrations/0002_tenants.sql
internal/usage/       view counter + flush, month aggregates
```
