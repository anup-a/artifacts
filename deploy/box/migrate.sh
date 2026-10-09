#!/usr/bin/env bash
# Copies byagent's data and settings from the Fly machine onto the box.
#
#   ./deploy/box/migrate.sh root@<box ip>           # rehearsal
#   ./deploy/box/migrate.sh root@<box ip> --final   # the real move
#
# A rehearsal copies everything while Fly keeps serving, so the box can be
# checked before any traffic moves. --final first turns on READ_ONLY on Fly
# (publishing and comments pause; pages keep serving), so the snapshot is the
# last word and nothing written to Fly is lost. Fly stays read-only after
# that; the Cloudflare hostnames are switched to the tunnel next (RUNBOOK).
#
# Needs: setup.sh and ship.sh already run (the image is on the box), fly auth login,
# and the tunnel token in TUNNEL_TOKEN or the Keychain
# (security add-generic-password -s cloudflared -a byagent -w).
set -euo pipefail
TARGET="${1:?usage: migrate.sh user@host [--final]}"
FINAL="${2:-}"
APP="${FLY_APP:-artifacts-share}"
cd "$(dirname "$0")"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

TUNNEL_TOKEN="${TUNNEL_TOKEN:-$(security find-generic-password -s cloudflared -a byagent -w 2>/dev/null || true)}"
[ -n "$TUNNEL_TOKEN" ] || { echo "no tunnel token: set TUNNEL_TOKEN or add it to the Keychain (service cloudflared, account byagent)"; exit 1; }

if [ "$FINAL" = "--final" ]; then
  echo "==> pausing writes on Fly (READ_ONLY=on; the machine restarts)"
  fly secrets set READ_ONLY=on --app "$APP"
  # The restart drops in-flight requests; give it a moment to come back.
  for _ in $(seq 1 30); do
    curl -fsS https://app.byagent.dev/healthz >/dev/null 2>&1 && break
    sleep 2
  done
fi

echo "==> reading settings from Fly"
fly ssh console --app "$APP" -C "/artifacts-server dotenv" | tr -d '\r' | grep -E '^[A-Z_]+=' >"$WORK/.env"
grep -q '^KEY_SIGNING_SECRET=' "$WORK/.env" || { echo "KEY_SIGNING_SECRET missing from Fly's settings; stopping"; exit 1; }
printf 'TUNNEL_TOKEN=%s\n' "$TUNNEL_TOKEN" >>"$WORK/.env"

echo "==> snapshotting Fly's database and files"
fly ssh console --app "$APP" -C "/artifacts-server snapshot /tmp/snapshot.tgz"
fly ssh sftp get /tmp/snapshot.tgz "$WORK/snapshot.tgz" --app "$APP"
tar tzf "$WORK/snapshot.tgz" | grep -qx artifacts.db || { echo "snapshot has no artifacts.db; stopping"; exit 1; }
echo "    $(du -h "$WORK/snapshot.tgz" | cut -f1), $(tar tzf "$WORK/snapshot.tgz" | grep -c '^blobs/') blobs"

echo "==> restoring on $TARGET"
scp "$WORK/snapshot.tgz" "$TARGET":/tmp/snapshot.tgz
scp "$WORK/.env" "$TARGET":/tmp/byagent.env
ssh "$TARGET" 'set -e
  cd /opt/artifacts
  install -m 600 /tmp/byagent.env .env && rm -f /tmp/byagent.env
  docker compose stop artifacts 2>/dev/null || true
  # Keep the previous data until the restore has worked.
  [ -d data ] && [ -n "$(ls -A data)" ] && mv data "data.before-$(date +%Y%m%d%H%M%S)"
  install -d -o 65532 -g 65532 -m 750 data
  tar -xzf /tmp/snapshot.tgz -C data && rm -f /tmp/snapshot.tgz
  chown -R 65532:65532 data
  docker compose up -d
  for _ in $(seq 1 30); do curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && break; sleep 2; done
  curl -fsS http://127.0.0.1:8080/healthz >/dev/null
  sqlite3 -readonly data/artifacts.db "select count(*) || \" artifacts, \" || (select count(*) from tenants) || \" workspaces\" from artifacts"'

echo "==> done. The box serves the copied data on 127.0.0.1:8080."
if [ "$FINAL" = "--final" ]; then
  echo "Next: point byagent.dev and app.byagent.dev at the tunnel (RUNBOOK, Production on a VPS > Cutover)."
else
  echo "Rehearsal only: Fly is still live and writable."
fi
