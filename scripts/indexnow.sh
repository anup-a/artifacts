#!/usr/bin/env bash
# Tells IndexNow (Bing, which ChatGPT search and Copilot read, plus Yandex,
# Seznam and Naver) about byagent.dev's public pages, so a new or changed page
# is crawled in hours rather than whenever the bot next comes by.
#
#   ./scripts/indexnow.sh                       every URL in the sitemap
#   ./scripts/indexnow.sh /guides/codex ...     just these paths
#
# The key is served at /<key>.txt by internal/agents and is public by design.
set -euo pipefail
SITE="${SITE:-https://byagent.dev}"
KEY=$(sed -n 's/^const indexNowKey = "\(.*\)"$/\1/p' "$(dirname "$0")/../internal/agents/agents.go")
test -n "$KEY" || { echo "no indexNowKey in internal/agents/agents.go"; exit 1; }
curl -sf "$SITE/$KEY.txt" | grep -qx "$KEY" || { echo "$SITE/$KEY.txt does not serve the key; deploy first"; exit 1; }

if [ $# -gt 0 ]; then
  urls=$(for p in "$@"; do echo "$SITE$p"; done)
else
  urls=$(curl -sf "$SITE/sitemap.xml" | sed -n 's:.*<loc>\(.*\)</loc>.*:\1:p')
fi
list=$(printf '%s\n' "$urls" | sed 's/.*/"&"/' | paste -sd, -)
host=${SITE#https://}
code=$(curl -s -o /dev/stderr -w '%{http_code}' -H 'Content-Type: application/json; charset=utf-8' \
  -d "{\"host\":\"$host\",\"key\":\"$KEY\",\"keyLocation\":\"$SITE/$KEY.txt\",\"urlList\":[$list]}" \
  https://api.indexnow.org/indexnow)
echo "IndexNow: HTTP $code for $(printf '%s\n' "$urls" | wc -l | tr -d ' ') URLs (200 or 202 means accepted)"
case "$code" in 200|202) ;; *) exit 1 ;; esac
