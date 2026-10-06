#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# SMOKE_PORT moves it off 8091 when something else already listens there.
PORT=${SMOKE_PORT:-8091}
export ADMIN_TOKEN=smoke ADDR=:$PORT DATA_DIR="$(mktemp -d)" PUBLIC_SHARE_ORIGIN=http://localhost:$PORT PUBLIC_API_ORIGIN=http://localhost:$PORT
export DB=sqlite STORAGE=local KEY_SIGNING_SECRET= EMAIL_PROVIDER= GITHUB_CLIENT_ID= GITHUB_CLIENT_SECRET=
# Build first and run the binary: `go run` execs the server as a child, so the
# trap below would kill the go wrapper and leave the server holding the port,
# making a second run of this script fail.
go build -o "$DATA_DIR/server" ./cmd/artifacts-server
"$DATA_DIR/server" & PID=$!
trap 'kill $PID' EXIT
for _ in $(seq 1 50); do curl -sf localhost:$PORT/healthz >/dev/null && break; sleep 0.2; done
H='Authorization: Bearer smoke'
STATUS=$(curl -s -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{"email":"removed@example.test"}' localhost:$PORT/api/keys)
test "$STATUS" = 404 || { echo "FAIL: POST /api/keys returned $STATUS, expected 404"; exit 1; }
BODY='<html><body><h1>hello world</h1></body></html>'
SHA=$(printf '%s' "$BODY" | shasum -a 256 | cut -d' ' -f1)
printf '%s' "$BODY" > "$DATA_DIR/index.html"
MAN=$(printf '{"title":"Smoke","files":[{"path":"index.html","sha256":"%s","size":%d}]}' "$SHA" "${#BODY}")
RES=$(curl -sf -H "$H" -H 'X-Artifact-End-User: smoke-user' -F "manifest=$MAN" -F "$SHA=@$DATA_DIR/index.html" localhost:$PORT/api/artifacts)
ID=$(echo "$RES" | sed -E 's/.*"id":"([^"]+)".*/\1/')
echo "published $ID"
FILTERED=$(curl -sf -H "$H" "localhost:$PORT/api/artifacts?end_user=smoke-user")
echo "$FILTERED" | grep -q "$ID" || { echo "FAIL: end-user filter omitted the artifact"; exit 1; }
echo "$FILTERED" | grep -q '"end_user":"smoke-user"' || { echo "FAIL: end-user attribution missing"; exit 1; }
OTHER=$(curl -sf -H "$H" "localhost:$PORT/api/artifacts?end_user=other-user")
echo "$OTHER" | grep -q '"artifacts":\[\]' || { echo "FAIL: end-user filter returned another user's artifact"; exit 1; }
curl -sf "localhost:$PORT/a/$ID/" | grep -q 'data-artifact' || { echo "runtime not injected"; exit 1; }
curl -sf "localhost:$PORT/a/$ID/" | grep -q 'hello world' || { echo "body missing"; exit 1; }
# The embedded runtime must be the real build, not a stub. Matching on a class
# the bundle only defines once it actually paints highlights.
rt=$(curl -fsS "localhost:$PORT/_rt/comments.js")
case "$rt" in
  *artifacts-hl*) ;;
  *) echo "FAIL: served runtime is not the real bundle"; exit 1 ;;
esac
if [ "${#rt}" -lt 2000 ]; then
  echo "FAIL: runtime bundle looks truncated (${#rt} bytes)"
  exit 1
fi
echo "runtime bundle served: ${#rt} bytes"
# No -q here: grep -q stops at the first match, curl then fails writing the
# rest of a large file, and pipefail reports that as a smoke failure.
curl -fsS "localhost:$PORT/skill.md" | grep 'byagent publish' >/dev/null || { echo "FAIL: /skill.md is not the agent skill"; exit 1; }
TH=$(curl -sf -H 'Content-Type: application/json' -d '{"author_name":"Smoke","body":"typo","anchor":{"kind":"text","quote":"hello"},"version":1}' "localhost:$PORT/c/$ID/comments")
TID=$(echo "$TH" | sed -E 's/.*"id":"(th_[^"]+)".*/\1/')
curl -sf -H "$H" "localhost:$PORT/api/artifacts/$ID/comments" | grep -q "$TID" || { echo "owner cannot see comment"; exit 1; }
curl -sf -X POST -H "$H" "localhost:$PORT/api/artifacts/$ID/comments/$TID/resolve" | grep -q '"resolved"' || { echo "resolve failed"; exit 1; }
BODY2='<html><body><h1>hello again</h1></body></html>'
SHA2=$(printf '%s' "$BODY2" | shasum -a 256 | cut -d' ' -f1)
printf '%s' "$BODY2" > "$DATA_DIR/index2.html"
MAN2=$(printf '{"files":[{"path":"index.html","sha256":"%s","size":%d}]}' "$SHA2" "${#BODY2}")
curl -sf -X PUT -H "$H" -F "manifest=$MAN2" -F "$SHA2=@$DATA_DIR/index2.html" "localhost:$PORT/api/artifacts/$ID" | grep -q '"version":2' || { echo "update failed"; exit 1; }
# Rollback is a pointer flip: v1 goes live again and no v3 is made.
RB=$(curl -sf -X POST -H "$H" "localhost:$PORT/api/artifacts/$ID/rollback/1")
echo "$RB" | grep -q '"version":1' && echo "$RB" | grep -q '"previous_version":2' || { echo "rollback failed: $RB"; exit 1; }
curl -sf "localhost:$PORT/a/$ID/" | grep -q 'hello world' || { echo "rollback content wrong"; exit 1; }
curl -sf "localhost:$PORT/a/$ID/v/2/" | grep -q 'hello again' || { echo "pinned version wrong"; exit 1; }
STATUS=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "$H" "localhost:$PORT/api/artifacts/$ID/versions/1")
[ "$STATUS" = 409 ] || { echo "deleting the live version answered $STATUS, want 409"; exit 1; }
curl -sf -X DELETE -H "$H" "localhost:$PORT/api/artifacts/$ID/versions/2" | grep -q '"deleted_version":2' || { echo "version delete failed"; exit 1; }
STATUS=$(curl -s -o /dev/null -w '%{http_code}' "localhost:$PORT/a/$ID/v/2/")
[ "$STATUS" = 404 ] || { echo "deleted version still served: $STATUS"; exit 1; }
curl -sf -X PUT -H "$H" -F "manifest=$MAN2" "localhost:$PORT/api/artifacts/$ID" | grep -q '"version":3' || { echo "publish after restore and delete did not take v3"; exit 1; }
# Page hosts: the same data, restarted with CONTENT_DOMAIN. The page moves to
# a host of its own; its old address redirects there; another page's origin
# cannot post to it.
kill $PID; wait $PID 2>/dev/null || true
CONTENT_DOMAIN="http://page.localhost:$PORT" "$DATA_DIR/server" & PID=$!
for _ in $(seq 1 50); do curl -sf localhost:$PORT/healthz >/dev/null && break; sleep 0.2; done
URL=$(curl -sf -H "$H" "localhost:$PORT/api/artifacts/$ID" | sed -E 's/.*"url":"([^"]+)".*/\1/')
case "$URL" in
  http://*.page.localhost:$PORT/) ;;
  *) echo "FAIL: url is not a page host: $URL"; exit 1 ;;
esac
PAGEHOST=${URL#http://}; PAGEHOST=${PAGEHOST%%/*}
curl -sf -H "Host: $PAGEHOST" "localhost:$PORT/" | grep -q 'hello again' || { echo "FAIL: page host does not serve the page"; exit 1; }
LOC=$(curl -s -o /dev/null -w '%{redirect_url}' "localhost:$PORT/a/$ID/v/1/")
[ "$LOC" = "${URL}v/1/" ] || { echo "FAIL: /a/$ID/v/1/ redirected to '$LOC'"; exit 1; }
STATUS=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: $PAGEHOST" -H "Origin: http://aaaaaaaaaaaaaaaa.page.localhost:$PORT" -H 'Content-Type: application/json' \
  -d '{"author_name":"Smoke","body":"forged","anchor":{"kind":"text","quote":"hello"},"version":3}' "localhost:$PORT/c/$ID/comments")
[ "$STATUS" = 403 ] || { echo "FAIL: cross-page comment answered $STATUS, want 403"; exit 1; }
STATUS=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: $PAGEHOST" -H "Origin: ${URL%/}" -H 'Content-Type: application/json' \
  -d '{"author_name":"Smoke","body":"own","anchor":{"kind":"text","quote":"hello"},"version":3}' "localhost:$PORT/c/$ID/comments")
[ "$STATUS" = 201 ] || { echo "FAIL: the page's own comment answered $STATUS, want 201"; exit 1; }
echo "page host $PAGEHOST"
# Page hosts under the share host itself ({slug}.localhost, as production's
# {slug}.byagent.dev): the share host keeps its routes and no redirect loops,
# a slug host serves the page, and a page cannot post to the API.
kill $PID; wait $PID 2>/dev/null || true
CONTENT_DOMAIN="http://localhost:$PORT" "$DATA_DIR/server" & PID=$!
for _ in $(seq 1 50); do curl -sf localhost:$PORT/healthz >/dev/null && break; sleep 0.2; done
URL=$(curl -sf -H "$H" "localhost:$PORT/api/artifacts/$ID" | sed -E 's/.*"url":"([^"]+)".*/\1/')
case "$URL" in
  http://*.localhost:$PORT/) ;;
  *) echo "FAIL: shared url is not a page host: $URL"; exit 1 ;;
esac
PAGEHOST=${URL#http://}; PAGEHOST=${PAGEHOST%%/*}
curl -sf -H "Host: $PAGEHOST" "localhost:$PORT/" | grep -q 'hello again' || { echo "FAIL: shared page host does not serve the page"; exit 1; }
curl -sf -H "Host: app.localhost:$PORT" "localhost:$PORT/healthz" >/dev/null || { echo "FAIL: a non-slug name lost its routes"; exit 1; }
STATUS=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "$H" -H "Origin: ${URL%/}" "localhost:$PORT/api/artifacts/$ID/share-code")
[ "$STATUS" = 403 ] || { echo "FAIL: a page host wrote to the API: $STATUS"; exit 1; }
echo "shared page host $PAGEHOST"
echo "SMOKE OK"
