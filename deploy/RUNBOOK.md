# Runbook

Operational reference for running `artifacts` on the managed deployment
(Hetzner CX22 + Cloudflare + Neon + R2, per §3b of the
[design spec](../docs/superpowers/specs/2026-09-15-artifacts-service-design.md)).

## Production on a VPS (behind a Cloudflare Tunnel)

Why it replaced Fly: Fly bills every GB served and has no spending cap, so a
viral page or a flood has no ceiling. The box is an OVHcloud VPS-1 in Europe
(2 vCores, 4 GB, 40 GB NVMe, no commitment): traffic is unlimited and never
billed, and Cloudflare in front absorbs floods. Same binary, same sqlite +
local blobs, files in `deploy/box/`. Nothing in them is OVH-specific; any
Ubuntu 24.04 VPS works. (Hetzner was the first pick, but its CX plans were
out of stock in October 2026.)

Shape: `cloudflared` dials out to Cloudflare and carries `byagent.dev` and
`app.byagent.dev` to the server over the compose network. ufw admits ssh
only and the app binds to 127.0.0.1, so it is unreachable except through
Cloudflare, which is what makes `TRUST_PROXY_HEADERS=true`
(CF-Connecting-IP) safe.

On the box, `/opt/artifacts/` holds `docker-compose.yml`, `.env` (secrets,
mode 600), `data/` (sqlite + blobs, uid 65532) and `backups/`.

### One-time setup

1. Order the VPS (Ubuntu 24.04) with this laptop's ssh key. OVH emails the
   IP once the order is delivered; the login is `ubuntu`.
2. Cloudflare: add the `byagent.dev` zone and switch Namecheap's nameservers
   to Cloudflare's. Check every record came across (A/AAAA for both hosts,
   MX, TXT). Namecheap's email forwarding (`eforward*` MX) stops working off
   Namecheap DNS: recreate the forwards in Cloudflare Email Routing.
3. Cloudflare Zero Trust > Networks > Tunnels: create tunnel `byagent`
   (cloudflared), token in the Keychain:
   `security add-generic-password -s cloudflared -a byagent -w`.
   Add no public hostnames yet.
4. `./deploy/box/setup.sh ubuntu@<ip>`: root ssh keys (laptop + CI deploy
   key at `~/.ssh/byagent_deploy`), key-only ssh, ufw, Docker, unattended
   upgrades, the nightly backup. Prints the GitHub secrets for CI.

### Move the data

The Fly server must already run a build with `snapshot` and `dotenv`.

```
fly auth login
./deploy/box/ship.sh root@<ip>             # image + compose onto the box
./deploy/box/migrate.sh root@<ip>          # rehearsal: Fly stays live
```

The rehearsal copies Fly's settings (`artifacts-server dotenv`), database and
files (`artifacts-server snapshot`, VACUUM INTO, so it is consistent while
Fly serves) and starts them on the box. Check counts and a page:
`ssh root@<ip> curl -s 127.0.0.1:8080/a/<id>/`.

### Cutover

1. `./deploy/box/migrate.sh root@<ip> --final`. It sets `READ_ONLY=on` on
   Fly first: pages keep serving, publishing and comments answer 503 with
   Retry-After, so the final snapshot loses nothing.
2. Cloudflare DNS: delete the A/AAAA records for `byagent.dev` and
   `app.byagent.dev` (they point at Fly).
3. Tunnel `byagent` > Public hostnames: `byagent.dev` and `app.byagent.dev`,
   both to `http://artifacts:8080`. Cloudflare creates the proxied CNAMEs.
4. Check: `curl -sI https://app.byagent.dev/healthz` answers 200 with a
   `cf-ray` header and no `fly-request-id`. Publish a page, comment on it,
   sign in.
5. GitHub: secrets `BOX_HOST`, `BOX_SSH_KEY`, `BOX_KNOWN_HOSTS` (from
   setup.sh); repo variable `DEPLOY_TARGET=box`. Pushes to main now ship to
   the box, and the Fly deploy job stops.
6. Rollback while Fly still exists: point the hostnames back at Fly
   (delete the tunnel hostnames, re-add the A/AAAA records) and
   `fly secrets unset READ_ONLY --app artifacts-share`. Writes made on the box
   in between stay on the box.
7. After a quiet week: `fly apps destroy artifacts-share` and remove the
   `FLY_API_TOKEN` secret.

### Running it

- Deploy: push to main (CI runs `ship.sh`), or `./deploy/box/ship.sh root@<ip>`.
- Roll back a release: `docker tag artifacts:<sha> artifacts:local && docker compose up -d`
  in `/opt/artifacts` (ship.sh keeps the five newest builds).
- Logs: `cd /opt/artifacts && docker compose logs -f artifacts`.
- Backups: `/usr/local/bin/byagent-backup` runs at 02:17 UTC (cron) and keeps
  five days of `backups/byagent-YYYY-MM-DD.tgz`, matching the privacy
  policy. Restore with `migrate.sh`'s steps: stop, unpack into an empty
  `data/`, chown 65532, start. Those copies live on the same disk; OVH's
  included daily backup covers losing the disk. Copy one off the box before
  any risky change.
- Updates: unattended-upgrades installs security fixes and reboots at 04:30
  UTC when needed; both containers restart on boot.
- The box's disk is not encrypted at rest. Keep the public security page in
  line with that.

## Page hosts ({slug}.byagent.dev)

`CONTENT_DOMAIN=byagent.dev` gives every page its own origin,
`https://<slug>.byagent.dev/`, so one page's script cannot read another page
or act with its visitors' unlocks (README: "One origin per page"). The slug
is 16 lower-case base32 characters. Only a name that is exactly one such
label is a page host: `byagent.dev`, `www`, `app`, `domains` and every other
name keep their normal routes, so no new domain is needed. It needs a
wildcard DNS record and a wildcard certificate. Until `CONTENT_DOMAIN` is set
the server ignores all of it, so do the DNS and certificate first, check
them, and only then turn it on.

Because page hosts are same-site with byagent.dev and app.byagent.dev, the
server also:

- issues the dashboard's cookies host-only with the `__Host-` prefix
  (`__Host-artifacts_session`: Secure, `Path=/`, no `Domain`), so a page
  cannot set, shadow or read them. Sessions under the old name
  (`artifacts_session` at `/app`) that existed before the deploy are moved
  to the new one on their next request; nobody is signed out.
- refuses (403) any state-changing request to byagent's own hosts whose
  `Origin` is a page host, and requires every write on a page host to carry
  that page's own `Origin`.

Nothing in the server sets `Domain=byagent.dev`, and nothing may: a cookie
with that attribute would be sent to every page host.

The setting is read at start-up. Unsetting it is the rollback: pages go back
to `byagent.dev/a/<id>/` at once. Browsers keep the 308 from `/a/<id>/` to the
page host for up to an hour (`Cache-Control: max-age=3600`), and links
already handed out as `https://<slug>.byagent.dev/` stop finding their page
(the host falls through to byagent's own routes). Roll back only for a real
fault, and leave the DNS and certificate in place. The cookie rename is not
rolled back by it and does not need to be.

### DNS (Cloudflare zone `byagent.dev`)

Add one wildcard record, `*`, pointing where `byagent.dev` points (next two
sections). Specific records keep priority over the wildcard in DNS, so
`app`, `domains` (the custom-domain CNAME target), `_acme-challenge` and any
other existing name are untouched; the wildcard only answers names that have
no record of their own. Do not add records for names under it that should
be pages.

### While on Fly

```
fly certs add "*.byagent.dev" --app artifacts-share
fly certs setup "*.byagent.dev" --app artifacts-share   # prints the exact records
fly ips list --app artifacts-share
```

In the `byagent.dev` zone, with the same proxy status as the existing
`byagent.dev` records (DNS only, grey cloud, when Fly terminates TLS):

| Type | Name | Value |
| --- | --- | --- |
| `A` | `*` | the app's shared IPv4 (same as byagent.dev) |
| `AAAA` | `*` | the app's IPv6 |
| `CNAME` | `_acme-challenge` | the target `fly certs add "*.byagent.dev"` prints, `byagent.dev.<id>.flydns.net` |

A wildcard certificate can only be issued over DNS-01, which is what the
`_acme-challenge` CNAME is for: it lets Fly answer Let's Encrypt's challenge
for the zone. Leave it in place, renewals use it. Wait for
`fly certs check "*.byagent.dev"` to say Issued, then check from outside:

```
dig +short abcdefghijklmnop.byagent.dev A @1.1.1.1   # Fly's IPv4
curl -sI https://app.byagent.dev/healthz | head -1    # still 200: specific records win
```

Turn it on, without a redeploy of new code:

```
fly secrets set CONTENT_DOMAIN=byagent.dev --app artifacts-share
```

That restarts the machine (a few seconds). Rollback: `fly secrets unset
CONTENT_DOMAIN --app artifacts-share`.

### On the VPS (Cloudflare Tunnel)

Cloudflare's free Universal SSL covers `byagent.dev` and `*.byagent.dev`
(one level, which is all page hosts use), so the edge needs nothing more.

1. Delete the Fly wildcard records above (`A`/`AAAA` for `*`; the
   `_acme-challenge` CNAME can go once Fly no longer serves the zone).
2. Zero Trust > Networks > Tunnels > `byagent` > Public hostnames, add
   `*.byagent.dev` (subdomain `*`) to `http://artifacts:8080`, below the
   existing specific hostnames. Check that a proxied `CNAME *` to
   `<tunnel-id>.cfargotunnel.com` exists and add it if the dashboard did not.
   If the tunnel is ever moved to a local `config.yml`, the same thing reads
   (specific names first; cloudflared matches rules in order):

   ```yaml
   ingress:
     - hostname: byagent.dev
       service: http://artifacts:8080
     - hostname: app.byagent.dev
       service: http://artifacts:8080
     - hostname: "*.byagent.dev"
       service: http://artifacts:8080
     - service: http_status:404
   ```
3. `/opt/artifacts/.env`: `CONTENT_DOMAIN=byagent.dev`, then
   `docker compose up -d` in `/opt/artifacts`. Rollback: empty it and
   `docker compose up -d`.
4. Check: `curl -sI https://<slug>.byagent.dev/` (any page's slug, from the
   API's `url`) answers 200 with a `cf-ray` header;
   `curl -sI https://byagent.dev/a/<id>/` answers 308 to it;
   `curl -sI https://app.byagent.dev/app/login` still answers 200.

Cache rules: none are needed, and none may key on host alone. Pages answer
`Cache-Control: no-cache` so visibility changes take effect, and the 308s
that carry a share code are `no-store`.

### After it is on

- Publish a page and open it: the URL is `https://<slug>.byagent.dev/`.
  Leave a comment on it. Open an old `byagent.dev/a/<id>/?k=<code>` link for
  a private page: it lands on the page host, unlocked.
- Sign in to the dashboard and check the browser holds
  `__Host-artifacts_session` on app.byagent.dev only.

### Optional, later: a domain of its own

The server also supports page hosts on a separate registrable domain
(`CONTENT_DOMAIN=byagent.page`): its apex redirects to byagent.dev and
other names under it answer 404. Listing such a domain on the Public Suffix
List would make each page host a site of its own, so SameSite would separate
pages too. Not planned; the draft request is kept in
[docs/psl-byagent-page.md](../docs/psl-byagent-page.md) for if it ever is.

### Custom domains (Cloudflare for SaaS)

Customers CNAME their hostname to `domains.byagent.dev`; Cloudflare issues the
certificate and sends the traffic down the same tunnel with the customer's
Host header intact; the server matches the Host to a connected domain. Nothing
here is needed until custom domains are turned on, and the server runs fine
without them (`DOMAINS_PROVIDER` unset: the routes answer 404).

One time, in the Cloudflare dashboard for `byagent.dev`:

1. SSL/TLS > Custom Hostnames: enable Cloudflare for SaaS. It is available on
   the Free plan with 100 custom hostnames included, then $0.10 per hostname
   a month; Cloudflare may ask for a payment method on the account first.
2. Zero Trust > Networks > Tunnels > `byagent` > Public hostnames: add
   `domains.byagent.dev` to `http://artifacts:8080`. That creates the proxied
   CNAME the fallback origin needs.
3. Back in Custom Hostnames, set Fallback Origin to `domains.byagent.dev` and
   wait for it to read Active.
4. Tunnel catch-all. A customer's request reaches cloudflared with their own
   Host (`docs.example.com`), which matches none of the public hostnames, so
   the tunnel's last rule must send it to the app instead of answering 404.
   If the dashboard does not let you change that rule, set it with the API
   (this replaces the whole ingress list, so keep the three hostnames in it):

   ```
   curl -X PUT "https://api.cloudflare.com/client/v4/accounts/$ACCOUNT_ID/cfd_tunnel/$TUNNEL_ID/configurations" \
     -H "Authorization: Bearer $CF_TUNNEL_TOKEN" -H "Content-Type: application/json" \
     --data '{"config":{"ingress":[
       {"hostname":"byagent.dev","service":"http://artifacts:8080"},
       {"hostname":"app.byagent.dev","service":"http://artifacts:8080"},
       {"hostname":"domains.byagent.dev","service":"http://artifacts:8080"},
       {"service":"http://artifacts:8080"}]}}'
   ```

   (`GET` the same URL first and keep any other rules it has. The token needs
   Account > Cloudflare Tunnel > Edit.) Only hostnames Cloudflare routes to the
   tunnel ever reach it, and the server serves a Host only when it is byagent's
   own or a connected, verified domain, so the catch-all opens nothing else.
5. My Profile > API Tokens > Create custom token: Zone > SSL and Certificates
   > Edit, limited to the `byagent.dev` zone. This is what the server uses to
   create and delete custom hostnames. Copy the Zone ID from the zone's
   Overview page.
6. Add to `/opt/artifacts/.env`, then `docker compose up -d`:

   ```
   DOMAINS_PROVIDER=cloudflare
   DOMAINS_CNAME_TARGET=domains.byagent.dev
   CLOUDFLARE_API_TOKEN=<token from step 5>
   CLOUDFLARE_ZONE_ID=<zone id>
   # optional: LIMIT_DOMAINS_PER_TENANT=5, DOMAINS_DNS_RESOLVER=1.1.1.1:53
   ```

   Ownership TXT records are read from 1.1.1.1 over port 53; ufw already
   allows outgoing traffic. Set `DOMAINS_DNS_RESOLVER=system` if that is ever
   blocked.
7. Check with a hostname you own: `byagent domain add test.<your domain>
   --page <id>`, add the two records it prints, poll `byagent domain
   test.<your domain>` until `active`, open `https://test.<your domain>/`,
   leave a comment there, then `byagent domain rm test.<your domain>`.

Notes:

- Zone rules apply to custom hostname traffic too. Write rate-limit and WAF
  rules without a hostname condition (for example `/c/` paths on any host)
  so they cover customer domains.
- An apex (`example.com`) needs the customer's DNS to support ALIAS, ANAME or
  CNAME flattening. Cloudflare's apex proxying (fixed IPs) is Enterprise only.
- A hostname Cloudflare moves (CNAME taken away) shows `error` in the API;
  byagent asks Cloudflare to look again about hourly, so a fixed record
  recovers without anyone deleting and re-adding the domain.
- On Fly (current production), custom domains run on the `fly` provider:
  `DOMAINS_PROVIDER=fly`, `FLY_APP_NAME=artifacts-share`, `FLY_API_TOKEN`
  (`fly tokens create deploy -a artifacts-share`), `DOMAINS_APEX_IPS` from
  `fly ips list`, and `DOMAINS_CNAME_TARGET=domains.byagent.dev`. That name
  resolves to Fly through the `*.byagent.dev` records, so customers CNAME to
  the same target they will use after the move. At the cutover, point
  `domains.byagent.dev` at the tunnel, switch to `DOMAINS_PROVIDER=cloudflare`
  and the server registers the existing domains with Cloudflare by itself; no
  customer has to change their DNS.

## Validation deployment (Fly)

The cheapest way to put the product in front of real users: one Fly machine,
sqlite and blobs on a Fly volume, `ADMIN_TOKEN` for operator API access, and Fly's edge terminating
TLS. No Neon, no R2, no Cloudflare, no Caddy. Same binary and same API as the
managed deployment, so moving up later is a change of env vars.

Hostnames: `byagent.dev` for pages and `app.byagent.dev` for the API and
dashboard. Both point at the same binary. Keep them as distinct origins so
published HTML cannot share an origin with dashboard sessions.

The second host is `app`, not `api`, even though `PUBLIC_API_ORIGIN` names it:
one host serves both `/api/*` and the `/app/*` dashboard, so either label is
half right, and `app` is the half a person lands on from a sign-in email.

Pages live on the apex so a share URL stays short: `byagent.dev/a/<id>/`.
Because published HTML runs on the parent of the API host, a page that sets a
cookie scoped to `.byagent.dev` would have it sent to `app.byagent.dev`.
Dashboard cookies must therefore stay host-only (no `Domain` attribute), which
is what the server issues (`__Host-` over https). With `CONTENT_DOMAIN` set
(see Page hosts above) no page content runs on byagent.dev itself any more:
`/a/<id>/` only redirects to the page's own host, `<slug>.byagent.dev`.

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
setup below to create the GitHub OAuth app first. After the app is live, set GA with
`fly secrets set GOOGLE_ANALYTICS_ID=G-DFTQ6Y5L6W --app artifacts-share` (see Google Analytics below). Keep secret values out of
command arguments and source control.

Then the certificates and DNS:

```
fly certs add byagent.dev     --app artifacts-share
fly certs add app.byagent.dev --app artifacts-share
fly certs setup <hostname> --app artifacts-share   # prints every DNS option
fly ips list --app artifacts-share                 # the addresses to point at
```

An apex cannot be a `CNAME`, so both hostnames use address records instead, and
`fly certs add` prints the exact pair:

| Type | Host | Value |
| --- | --- | --- |
| `A` | `@` and `app` | the app's shared IPv4, e.g. `66.241.125.194` |
| `AAAA` | `@` and `app` | the app's dedicated IPv6 |

Those records both route traffic and prove ownership, because they resolve to
the app. `fly certs check <hostname>` reports when the certificate is issued.

DNS for `byagent.dev` is authoritative at Namecheap BasicDNS (the registrar),
so records added in Namecheap's Advanced DNS tab take effect. Delete the two
records a new Namecheap domain ships with first: the `www` parking `CNAME` and
the `@` URL-redirect record. Verify against a public resolver, never the local
one: `dig +short byagent.dev A @8.8.8.8`.

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
npm install -g byagent
printf '%s\n' "$ARTIFACTS_TOKEN" | byagent login --api https://app.byagent.dev
byagent whoami
byagent publish ./site --title "Hello"
```

Set `ARTIFACTS_TOKEN` to a key created at
`https://app.byagent.dev/app/keys`. `ADMIN_TOKEN` still works for the
separate `admin` tenant, which has no web login.

## Search and AI crawlers

- After a deploy that adds or changes public pages (guides, agent pages), run `./scripts/indexnow.sh` (or pass just the changed paths). It submits them to IndexNow, which feeds Bing, and through Bing ChatGPT search and Copilot. 200 or 202 means accepted.
- Google: the sitemap is at `/sitemap.xml`; submit it once in Search Console for the `byagent.dev` domain property.
- Which pages AI crawlers read: `fly logs -a artifacts-share | grep '"msg":"crawler"'` (on the box: `cd /opt/artifacts && docker compose logs artifacts | grep '"msg":"crawler"'`). Each line has the bot, path and status. People's requests are not logged.

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
| `EMAIL_FROM` | Verified sender for Resend, such as `artifacts <login@byagent.dev>`. |
| `PUBLIC_API_ORIGIN` | API and dashboard origin, including scheme. Also used in magic links, portal links, and the OAuth callback. |
| `PUBLIC_SHARE_ORIGIN` | Published pages and viewer comments origin. Keep distinct from the API origin. |
| `GUEST_PUBLISH` | `on` enables `POST /api/guest`: a key that needs no account, lasts 24 hours and publishes up to three public pages of 5 MB, five keys an hour per address. Guest pages carry a "Temporary guest page" badge, and the claim link in every guest response moves them into a signed-in workspace and revokes the guest key. Requires web sign-in. Off by default. |
| `LIMIT_ARTIFACTS_PER_TENANT` | Live artifact limit shared by all keys of a tenant. Default 200. |
| `LIMIT_ARTIFACTS_PER_KEY` | Deprecated fallback only when the tenant limit is unset; emits a deprecation log line. |
| `GOOGLE_ANALYTICS_ID` | GA4 Measurement ID for first-party app surfaces (`/`, `/app/*`, login, landing on `app.byagent.dev`). Empty disables GA and keeps CSP free of Google hosts. Alias: `GA_MEASUREMENT_ID`. Not applied to published `/a/…` pages or the share-origin gate. |
| `VARIABLES_KEY` | Turns on account variables and proxy routes. Base64 of 32 random bytes; store as a secret. Unset (or `VARIABLES=off`) turns the feature off. See below. |


### Account variables (VARIABLES_KEY)

Variables are API keys a workspace stores so its pages' proxy routes can call
other services. Values are sealed with AES-256-GCM under `VARIABLES_KEY`, so
the database and snapshots never hold them in the clear. Generate the key once
and keep a copy in the password manager; without it every stored value is
unreadable and users must set them again:

```
openssl rand -base64 32
```

Put it in `/opt/artifacts/.env` on the box as `VARIABLES_KEY=<value>` and
restart. The server refuses to start if the value is not 32 bytes of base64.
`artifacts-server dotenv` carries it over in a server move.

Rotation: set `VARIABLES_KEY=<new>,<old>` (the first key seals new values,
every listed key still opens old ones), restart, have users re-save their
variables (or leave both keys in place), then drop the old key. To switch the
feature off in an incident without losing the key, set `VARIABLES=off` and
restart: the variables API answers 503 and every proxy route 404.

Proxy routes are metered per client address (`LIMIT_PROXY_PER_IP_PER_HOUR`,
default 100, IPv6 by /64) and per page (`LIMIT_PROXY_PER_PAGE_PER_HOUR`,
default 1000). At most 128 calls run at once (16 per workspace), each for at
most 60 s. A visitor sees only "route unavailable" when a route is
misconfigured; the reason is in the `proxy route unavailable` log line.
Per-address limits rely on `TRUST_PROXY_HEADERS=true` behind Cloudflare, as
on the box. Each call logs `proxy` with the page, route, upstream host, status
and size; values never appear in logs.

### Google Analytics (GA4)

First-party app surfaces load GA4 when `GOOGLE_ANALYTICS_ID` is set. Published
artifact pages under `/a/…` and the share-code gate never receive the snippet.

For the Fly validation app (`artifacts-share`):

```
fly secrets set GOOGLE_ANALYTICS_ID=G-DFTQ6Y5L6W --app artifacts-share
```

Measurement ID `G-DFTQ6Y5L6W` is the byagent.dev GA4 stream. Leave the secret
unset for local/dev so no third-party scripts load.

For GitHub, create an OAuth app with the dashboard as its homepage and this
authorization callback URL:

```
https://app.byagent.dev/app/login/github/callback
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
byagent publish ./site --end-user customer-42 --json
byagent list --end-user customer-42 --json
byagent portal-link customer-42
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
destination with `byagent whoami` and `byagent list` before resuming writes.

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
