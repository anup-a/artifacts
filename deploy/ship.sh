#!/usr/bin/env bash
# Builds the server for the box's architecture, copies the image and the
# compose files over ssh, and restarts the stack:
#
#   ./deploy/ship.sh ubuntu@<box ip>
#
# The image travels over ssh rather than through a registry, so the box needs
# no credentials for the private repo. deploy/.env.free holds the box's
# secrets and is never committed.
#
# PLATFORM must match the box's architecture: linux/amd64 for an ordinary
# x86 VPS, linux/arm64 for an Ampere/Graviton one.
set -euo pipefail
TARGET="${1:?usage: ship.sh user@host}"
PLATFORM="${PLATFORM:-linux/amd64}"
cd "$(dirname "$0")/.."
ENV_FILE="deploy/.env.free"
[ -f "$ENV_FILE" ] || { echo "missing $ENV_FILE"; exit 1; }

echo "==> building $PLATFORM image"
docker build --platform "$PLATFORM" -t artifacts:local .

echo "==> copying image to $TARGET"
docker save artifacts:local | gzip | ssh "$TARGET" 'gunzip | sudo docker load'

echo "==> copying compose files"
scp deploy/docker-compose.free.yml "$TARGET":/tmp/docker-compose.yml
scp deploy/Caddyfile "$ENV_FILE" "$TARGET":/tmp/
ssh "$TARGET" 'sudo install -m 644 /tmp/docker-compose.yml /tmp/Caddyfile /opt/artifacts/ &&
  sudo install -m 600 /tmp/.env.free /opt/artifacts/.env &&
  rm -f /tmp/docker-compose.yml /tmp/Caddyfile /tmp/.env.free'

echo "==> starting"
ssh "$TARGET" 'cd /opt/artifacts && sudo docker compose --env-file .env up -d'

API_HOST=$(grep '^API_HOST=' "$ENV_FILE" | cut -d= -f2)
echo "==> waiting for https://$API_HOST/healthz"
for _ in $(seq 1 60); do
  curl -fsS "https://$API_HOST/healthz" >/dev/null 2>&1 && { echo "healthy"; exit 0; }
  sleep 5
done
echo "not healthy yet; check: ssh $TARGET 'sudo docker compose -f /opt/artifacts/docker-compose.yml logs'"
exit 1
