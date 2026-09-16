# Runbook

Operational reference for running `artifacts` on the managed deployment
(Hetzner CX22 + Cloudflare + Neon + R2, per §3b of the
[design spec](../docs/superpowers/specs/2026-09-15-artifacts-service-design.md)).

## Validation deployment (Fly)

The cheapest way to put the product in front of real users: one Fly machine,
sqlite and blobs on a Fly volume, `ADMIN_TOKEN` for operator API access, and Fly's edge terminating
TLS. No Neon, no R2, no Cloudflare, no Caddy. Same binary and same API as the
managed deployment, so moving up later is a change of env vars.

Hostnames: `share.creatica.app` for pages and `api.share.creatica.app` for the
API and dashboard. Both point at the same binary. Keep them as distinct
origins so published HTML cannot share an origin with dashboard sessions.

First time:

```
fly auth login
fly apps create artifacts-share
fly volumes create artifacts_data --region sin --size 1 --app artifacts-share
fly secrets import --app artifacts-share
fly deploy --config deploy/fly.free.toml
```

Import `ADMIN_TOKEN`, `KEY_SIGNING_SECRET`, `GITHUB_CLIENT_ID`,
`GITHUB_CLIENT_SECRET`, and `EMAIL_PROVIDER=log` through stdin. Use the sign-in
setup below to create the GitHub OAuth app first. Keep secret values out of
command arguments and source control.

Then the certificates and DNS:

```
fly certs add share.creatica.app     --app artifacts-share
fly certs add api.share.creatica.app --app artifacts-share
fly certs setup <hostname> --app artifacts-share   # prints every DNS option
```

At the DNS provider, one `CNAME` per hostname pointing at
`<prefix>.artifacts-share.fly.dev` (the prefix comes from `fly certs setup`).
That single record both routes traffic and proves ownership, because it
resolves to the app's AAAA. `fly certs check <hostname>` reports when the
certificate is issued.

After that, `fly deploy --config deploy/fly.free.toml` is the whole deploy loop.

### When the remote builder is unreachable

On a network where Fly's Depot builder fails its TLS handshake
(`authentication handshake failed: EOF`), build locally and deploy the image:

```
fly auth docker
docker buildx build --platform linux/amd64 \
  -f deploy/Dockerfile.fly -t registry.fly.io/artifacts-share:<tag> --push .
fly deploy --config deploy/fly.free.toml --image registry.fly.io/artifacts-share:<tag>
```

`buildx` is required: the classic builder cannot cross-build, and emulating an
amd64 toolchain on an arm64 Mac crashes the Go runtime. `Dockerfile.fly` builds
natively and cross-compiles, so nothing is emulated.

Smoke it with the CLI:

```
npm install -g ./cli   # not on npm yet; `npx artifacts` is someone else's package
printf '%s\n' "$ARTIFACTS_TOKEN" | artifacts login --api https://api.share.creatica.app
artifacts whoami
artifacts publish ./site --title "Hello"
```

Set `ARTIFACTS_TOKEN` to a key created at
`https://api.share.creatica.app/app/keys`. `ADMIN_TOKEN` still works for the
separate `admin` tenant, which has no web login.

## Sign-in and dashboard configuration

The web app is enabled when GitHub credentials or an explicit email provider
are configured. With neither, `/app/*` returns 503 with "Sign-in is not
configured on this server" and the API remains available. `ADMIN_TOKEN`
alone still supports an API-only installation. `POST /api/keys` always
returns 404; people create named keys at `PUBLIC_API_ORIGIN/app/keys`.

| Variable | Configuration |
| --- | --- |
| `KEY_SIGNING_SECRET` | Required whenever web sign-in or portal links are used, including with `ADMIN_TOKEN`. Store a strong random value as a secret. |
| `GITHUB_CLIENT_ID` | OAuth app client ID. Enables GitHub together with the client secret. |
| `GITHUB_CLIENT_SECRET` | OAuth app client secret. |
| `EMAIL_PROVIDER` | Unset disables email. Set `log` for development or validation; set `resend` for delivered magic links. |
| `RESEND_API_KEY` | Required for `EMAIL_PROVIDER=resend`; store as a secret. |
| `EMAIL_FROM` | Verified sender for Resend, such as `artifacts <login@share.creatica.app>`. |
| `PUBLIC_API_ORIGIN` | API and dashboard origin, including scheme. Also used in magic links, portal links, and the OAuth callback. |
| `PUBLIC_SHARE_ORIGIN` | Published pages and viewer comments origin. Keep distinct from the API origin. |
| `LIMIT_ARTIFACTS_PER_TENANT` | Live artifact limit shared by all keys of a tenant. Default 200. |
| `LIMIT_ARTIFACTS_PER_KEY` | Deprecated fallback only when the tenant limit is unset; emits a deprecation log line. |

For GitHub, create an OAuth app with the dashboard as its homepage and this
authorization callback URL:

```
https://api.share.creatica.app/app/login/github/callback
```

For another deployment, replace the origin with `PUBLIC_API_ORIGIN` and keep
`/app/login/github/callback`. Store both GitHub credentials in the server's
secret configuration and restart through the normal deploy process. The app
requests `read:user user:email` and uses a primary verified email address.

`EMAIL_PROVIDER=log` is the local and Fly validation path. The server emits
an INFO entry with `magic_link_url` for each accepted email request. Treat
those entries as credentials: restrict log access and use Resend for public
email delivery. Do not paste magic links into issue reports or commit logs.
Magic links are single use and expire after 15 minutes. Resend requires its
API key and an `EMAIL_FROM` address on a verified sending domain.

Sessions last 30 days and slide when active, refreshing at most hourly.
The `artifacts_session` cookie is HttpOnly, SameSite=Lax, Path=/app, and
Secure on HTTPS. Do not proxy `/app/*` onto the share origin or cache
authenticated pages and `/p/*`. Static files under `/app/static/*` have
content hashes and immutable cache headers.

Integrators create a workspace in Settings, create a key there, and publish:

```
artifacts publish ./site --end-user customer-42 --json
artifacts list --end-user customer-42 --json
artifacts portal-link customer-42
```

`--end-user` sends `X-Artifact-End-User`, an opaque 1 to 128 character
printable ASCII ID. A different ID on republish returns 403. The portal link
shows only that customer's artifacts, uses no session, and expires after
seven days. Rotating `KEY_SIGNING_SECRET` invalidates every existing portal
link immediately; individual portal links cannot be revoked.

Usage counts publishes and views by calendar month in UTC. Storage and open
comments are evaluated at the month cutoff from retained versions and
deletion or resolution times. A pruned historical version is unavailable for
historical storage totals. Views flush every 30 seconds and at graceful
shutdown, so let the server finish its normal termination before forcing it.

### Moving admin artifacts to a signed-in owner

The operator's first GitHub or email sign-in creates a personal tenant,
separate from `admin`. This is a manual database operation; there is no admin
console. Back up the database and pause publishing during the transfer.
Identify the owner's destination tenant with:

```sql
SELECT t.id, t.name, u.email
FROM tenants t
JOIN memberships m ON m.tenant_id = t.id
JOIN users u ON u.id = m.user_id
WHERE u.email = 'owner@example.com' AND t.kind = 'personal';
```

After verifying the destination, replace `t_DESTINATION` below with its ID.
The same SQL works in SQLite and PostgreSQL. The destination must not already
have end users with the same external IDs as `admin`; resolve those collisions
before running the transaction.

```sql
BEGIN;
UPDATE end_users SET tenant_id = 't_DESTINATION' WHERE tenant_id = 'admin';
UPDATE artifacts SET tenant_id = 't_DESTINATION' WHERE tenant_id = 'admin';
COMMIT;
```

This changes tenant ownership while preserving artifact IDs, URLs, versions,
and comments. Existing admin keys stay in `admin` and lose access to the
transferred artifacts. Create a destination key at `/app/keys` and update
publisher credentials. Artifact `key_id` is historical attribution, not
authorization; it may be null after a revoked key is collected. Verify the
destination with `artifacts whoami` and `artifacts list` before resuming writes.

## Validation box on a plain VPS (alternative)

`bootstrap.sh` and `ship.sh` deploy the same stack to any Ubuntu box over ssh,
with Caddy terminating TLS instead of Fly's edge. Secrets live in
`deploy/.env.free` (gitignored).

```
./deploy/bootstrap.sh ubuntu@<box ip>   # docker, host firewall, /opt/artifacts
./deploy/ship.sh      ubuntu@<box ip>   # build, copy image over ssh, up, health check
```

`ship.sh` carries the image over ssh instead of a registry, so the box needs no
credentials for the private repo. Set `PLATFORM=linux/arm64` for an Ampere or
Graviton box; it defaults to `linux/amd64`. DNS is an `A` record per hostname
pointing at the box IP.

## Managed box (Hetzner CX22)

```
apt update && apt install -y docker.io docker-compose-plugin
mkdir -p /opt/artifacts
cat > /opt/artifacts/.env   # then fill in the values below
```

`/opt/artifacts/.env`:

```
DB=postgres
DATABASE_URL=<neon connection string>
STORAGE=s3
S3_ENDPOINT=<account>.r2.cloudflarestorage.com
S3_BUCKET=artifacts
S3_ACCESS_KEY=<r2 access key>
S3_SECRET_KEY=<r2 secret key>
KEY_SIGNING_SECRET=<random secret>
GITHUB_CLIENT_ID=<github client id>
GITHUB_CLIENT_SECRET=<github client secret>
EMAIL_PROVIDER=resend
RESEND_API_KEY=<resend api key>
EMAIL_FROM=artifacts <login@example.com>
PUBLIC_SHARE_ORIGIN=https://share.<domain>
PUBLIC_API_ORIGIN=https://api.<domain>
```

The managed box uses `deploy/docker-compose.managed.yml` (NOT the self-host
`deploy/docker-compose.yml`, which is sqlite + local disk + `ADMIN_TOKEN`).
Every value in it comes from `.env`; it sets `TRUST_PROXY_HEADERS=true`
because the box is reachable only through Cloudflare, and it deliberately
sets no `ADMIN_TOKEN`. Keys are created after sign-in at `/app/keys` and belong
to the selected tenant. Only a hash and a display prefix are stored.
Ensure the container environment forwards the sign-in variables above, in
addition to the database, storage, and origin settings.

```
cp deploy/docker-compose.managed.yml /opt/artifacts/docker-compose.yml
cd /opt/artifacts
docker compose --env-file .env up -d
```

## Cloudflare

- DNS: A records for `share` and `api` pointing at the box IP, proxied
  (orange cloud).
- SSL/TLS mode: Full (strict) with an origin certificate installed on the
  box, or Flexible for the first day while DNS/certs settle.
- Cache rule: cache everything under `share.<domain>/a/*` and
  `share.<domain>/_rt/*`, respecting origin cache headers.
- Rate-limit rule: `share.<domain>/c/*` limited to 60 requests/min per IP.
- WAF: enable the managed rules ruleset.

## Deploy

```
cd /opt/artifacts
docker compose pull && docker compose up -d
curl -s https://api.<domain>/healthz
```

## Rebuild the box

The box holds no state, so rebuilding is the three steps above, in order:

1. `apt install docker.io docker-compose-plugin` (Managed box section).
2. Recreate `/opt/artifacts/.env` with the same secrets (Managed box section).
3. `docker compose up -d` (Deploy section).

## Backups

- Neon Postgres has point-in-time recovery (PITR); no separate DB backup
  is needed.
- R2 objects are content-addressed by sha256 and immutable once written;
  no separate blob backup is needed.
- Nothing else on the box needs backing up.

## Upgrade to Fly

When HA matters, move to Fly.io using the same image, keeping
`deploy/fly.toml` in the repo for this:

```
fly launch --copy-config --image ghcr.io/anup-a/artifacts:<tag>
fly secrets set DATABASE_URL=<neon connection string>
fly secrets set S3_ENDPOINT=<account>.r2.cloudflarestorage.com
fly secrets set S3_BUCKET=artifacts
fly secrets set S3_ACCESS_KEY=<r2 access key>
fly secrets set S3_SECRET_KEY=<r2 secret key>
fly secrets set KEY_SIGNING_SECRET=<random secret>
fly secrets set PUBLIC_SHARE_ORIGIN=https://share.<domain>
fly secrets set PUBLIC_API_ORIGIN=https://api.<domain>
```

Then point DNS at Fly instead of the Hetzner box.
