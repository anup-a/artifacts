#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export ADMIN_TOKEN=smoke ADDR=:8091 DATA_DIR="$(mktemp -d)" PUBLIC_SHARE_ORIGIN=http://localhost:8091 PUBLIC_API_ORIGIN=http://localhost:8091
# Build first and run the binary: `go run` execs the server as a child, so the
# trap below would kill the go wrapper and leave the server holding the port,
# making a second run of this script fail.
go build -o "$DATA_DIR/server" ./cmd/artifacts-server
"$DATA_DIR/server" & PID=$!
trap 'kill $PID' EXIT
for _ in $(seq 1 50); do curl -sf localhost:8091/healthz >/dev/null && break; sleep 0.2; done
H='Authorization: Bearer smoke'
BODY='<html><body><h1>hello world</h1></body></html>'
SHA=$(printf '%s' "$BODY" | shasum -a 256 | cut -d' ' -f1)
printf '%s' "$BODY" > "$DATA_DIR/index.html"
MAN=$(printf '{"title":"Smoke","files":[{"path":"index.html","sha256":"%s","size":%d}]}' "$SHA" "${#BODY}")
RES=$(curl -sf -H "$H" -F "manifest=$MAN" -F "$SHA=@$DATA_DIR/index.html" localhost:8091/api/artifacts)
ID=$(echo "$RES" | sed -E 's/.*"id":"([^"]+)".*/\1/')
echo "published $ID"
curl -sf "localhost:8091/a/$ID/" | grep -q 'data-artifact' || { echo "runtime not injected"; exit 1; }
curl -sf "localhost:8091/a/$ID/" | grep -q 'hello world' || { echo "body missing"; exit 1; }
# The embedded runtime must be the real build, not a stub. Matching on a class
# the bundle only defines once it actually paints highlights.
rt=$(curl -fsS "localhost:8091/_rt/comments.js")
case "$rt" in
  *artifacts-hl*) ;;
  *) echo "FAIL: served runtime is not the real bundle"; exit 1 ;;
esac
if [ "${#rt}" -lt 2000 ]; then
  echo "FAIL: runtime bundle looks truncated (${#rt} bytes)"
  exit 1
fi
echo "runtime bundle served: ${#rt} bytes"
TH=$(curl -sf -H 'Content-Type: application/json' -d '{"author_name":"Smoke","body":"typo","anchor":{"kind":"text","quote":"hello"},"version":1}' "localhost:8091/c/$ID/comments")
TID=$(echo "$TH" | sed -E 's/.*"id":"(th_[^"]+)".*/\1/')
curl -sf -H "$H" "localhost:8091/api/artifacts/$ID/comments" | grep -q "$TID" || { echo "owner cannot see comment"; exit 1; }
curl -sf -X POST -H "$H" "localhost:8091/api/artifacts/$ID/comments/$TID/resolve" | grep -q '"resolved"' || { echo "resolve failed"; exit 1; }
BODY2='<html><body><h1>hello again</h1></body></html>'
SHA2=$(printf '%s' "$BODY2" | shasum -a 256 | cut -d' ' -f1)
printf '%s' "$BODY2" > "$DATA_DIR/index2.html"
MAN2=$(printf '{"files":[{"path":"index.html","sha256":"%s","size":%d}]}' "$SHA2" "${#BODY2}")
curl -sf -X PUT -H "$H" -F "manifest=$MAN2" -F "$SHA2=@$DATA_DIR/index2.html" "localhost:8091/api/artifacts/$ID" | grep -q '"version":2' || { echo "update failed"; exit 1; }
curl -sf -X POST -H "$H" "localhost:8091/api/artifacts/$ID/rollback/1" | grep -q '"version":3' || { echo "rollback failed"; exit 1; }
curl -sf "localhost:8091/a/$ID/" | grep -q 'hello world' || { echo "rollback content wrong"; exit 1; }
curl -sf "localhost:8091/a/$ID/v/2/" | grep -q 'hello again' || { echo "pinned version wrong"; exit 1; }
echo "SMOKE OK"
