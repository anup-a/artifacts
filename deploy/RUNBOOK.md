# Runbook

Operational reference for running `artifacts` on the managed deployment
(Hetzner CX22 + Cloudflare + Neon + R2, per §3b of the
[design spec](../docs/superpowers/specs/2026-09-15-artifacts-service-design.md)).

## Validation deployment (Fly)

The cheapest way to put the product in front of real users: one Fly machine,
sqlite and blobs on a Fly volume, one `ADMIN_TOKEN`, and Fly's edge terminating
TLS. No Neon, no R2, no Cloudflare, no Caddy. Same binary and same API as the
managed deployment, so moving up later is a change of env vars.

Hostnames: `share.creatica.app` for pages and `api.share.creatica.app` for the
API. Both point at the same app; the server routes on path, not on host.

First time:

```
fly auth login
fly apps create artifacts-share
fly volumes create artifacts_data --region bom --size 1 --app artifacts-share
fly secrets set ADMIN_TOKEN=<token> --app artifacts-share
fly deploy --config deploy/fly.free.toml
```

Then the certificates and DNS. `fly certs add` prints the records to create:

```
fly certs add share.creatica.app     --app artifacts-share
fly certs add api.share.creatica.app --app artifacts-share
```

At the registrar, a `CNAME` for each hostname pointing at
`artifacts-share.fly.dev`, plus the `_acme-challenge` records Fly asks for.
`fly certs check <hostname>` reports when the certificate is issued.

After that, `fly deploy --config deploy/fly.free.toml` is the whole deploy loop.

Smoke it with the CLI:

```
echo "$ADMIN_TOKEN" | npx artifacts login --api https://api.share.creatica.app
npx artifacts publish ./site --title "Hello"
```

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
PUBLIC_SHARE_ORIGIN=https://share.<domain>
PUBLIC_API_ORIGIN=https://api.<domain>
```

The managed box uses `deploy/docker-compose.managed.yml` (NOT the self-host
`deploy/docker-compose.yml`, which is sqlite + local disk + `ADMIN_TOKEN`).
Every value in it comes from `.env`; it sets `TRUST_PROXY_HEADERS=true`
because the box is reachable only through Cloudflare, and it deliberately
sets no `ADMIN_TOKEN` — keys are issued per user and signed with
`KEY_SIGNING_SECRET`.

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
