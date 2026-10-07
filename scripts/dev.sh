#!/usr/bin/env bash
# Builds the server and (re)starts it in the background for a local look.
#
#   scripts/dev.sh [port]         build, free the port, start, wait for /healthz
#   scripts/dev.sh stop [port]    stop whatever listens on the port
#
# Run it again after every edit: static/ and templates/ are go:embed'ed, so a
# running server keeps serving the files it was built with. The port is freed
# by whoever holds it (lsof), not by process name, so a server started some
# other way cannot linger and answer with old CSS.
#
# DEV_GA=1 sets a fake GA ID, which turns on the consent banner and ga.js.
# Data persists in $DEV_DATA_DIR (default /tmp/byagent-dev-<port>) across
# restarts; the log is server.log in it.
set -euo pipefail
cd "$(dirname "$0")/.."

stop() {
  local pids
  pids=$(lsof -tiTCP:"$1" -sTCP:LISTEN 2>/dev/null || true)
  [ -z "$pids" ] && return 0
  kill $pids
  for _ in $(seq 1 50); do lsof -tiTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1 || return 0; sleep 0.1; done
  echo "port $1 still busy after kill" >&2
  exit 1
}

if [ "${1:-}" = stop ]; then
  stop "${2:-${DEV_PORT:-8792}}"
  exit 0
fi

PORT=${1:-${DEV_PORT:-8792}}
DIR=${DEV_DATA_DIR:-/tmp/byagent-dev-$PORT}
mkdir -p "$DIR/blobs"
go build -o "$DIR/server" ./cmd/artifacts-server
stop "$PORT"

BASE=http://localhost:$PORT
env DB=sqlite STORAGE=local DATA_DIR="$DIR" ADDR=:"$PORT" \
  PUBLIC_SHARE_ORIGIN="$BASE" PUBLIC_API_ORIGIN="$BASE" \
  KEY_SIGNING_SECRET=dev-signing-secret ADMIN_TOKEN=dev EMAIL_PROVIDER=log \
  GITHUB_CLIENT_ID= GITHUB_CLIENT_SECRET= \
  GA_MEASUREMENT_ID="$([ "${DEV_GA:-}" = 1 ] && echo G-TEST123 || true)" \
  nohup "$DIR/server" >"$DIR/server.log" 2>&1 &

for _ in $(seq 1 100); do
  if curl -sf "$BASE/healthz" >/dev/null; then
    echo "up: $BASE  (log: $DIR/server.log, stop: scripts/dev.sh stop $PORT)"
    exit 0
  fi
  sleep 0.1
done
echo "server did not come up; last log lines:" >&2
tail -20 "$DIR/server.log" >&2
exit 1
