#!/usr/bin/env bash
# Builds the server image, loads it onto the box over ssh, and restarts the
# stack. CI runs this on every push to main once DEPLOY_TARGET=box; it
# also works from a laptop:
#
#   ./deploy/box/ship.sh root@<box ip>
#
# The image travels over ssh rather than through a registry, so the box needs
# no credentials for the private repo. Each build is also tagged with its git
# sha, so rolling back is: docker tag artifacts:<sha> artifacts:local, then
# docker compose up -d. The box's .env is not touched here; migrate.sh writes
# it once.
set -euo pipefail
TARGET="${1:?usage: ship.sh user@host}"
cd "$(dirname "$0")/../.."
SHA="$(git rev-parse --short HEAD)"

echo "==> building linux/amd64 image artifacts:$SHA"
docker build --platform linux/amd64 -t artifacts:local -t "artifacts:$SHA" .

echo "==> loading it on $TARGET"
docker save artifacts:local "artifacts:$SHA" | gzip | ssh "$TARGET" 'gunzip | docker load'

echo "==> starting"
scp deploy/box/docker-compose.yml "$TARGET":/opt/artifacts/docker-compose.yml
ssh "$TARGET" 'cd /opt/artifacts && test -f .env || { echo "no /opt/artifacts/.env yet: run migrate.sh first"; exit 1; }
  docker compose up -d --remove-orphans
  # Keep the five newest builds for rollback.
  docker images artifacts --format "{{.Tag}} {{.CreatedAt}}" | grep -v "^local " | sort -k2 -r | tail -n +6 |
    cut -d" " -f1 | xargs -r -I{} docker rmi -f artifacts:{} >/dev/null'

echo "==> waiting for /healthz on the box"
for _ in $(seq 1 30); do
  ssh "$TARGET" 'curl -fsS http://127.0.0.1:8080/healthz' >/dev/null 2>&1 && { echo "healthy"; exit 0; }
  sleep 2
done
echo "not healthy: ssh $TARGET 'cd /opt/artifacts && docker compose logs --tail 50 artifacts'"
exit 1
