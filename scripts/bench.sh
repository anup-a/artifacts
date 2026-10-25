#!/usr/bin/env bash
# Times the byagent CLI against a live server, end to end from this machine:
# one first publish, then republishes to the same link, then comment reads.
# The page is private, expires in an hour, and is deleted at the end.
# Runs are spaced out to stay under LIMIT_PUBLISH_PER_MIN (10 by default).
#
#   scripts/bench.sh [page.md] [runs]
set -euo pipefail
command -v byagent >/dev/null || { echo "needs the byagent CLI: npm i -g byagent" >&2; exit 1; }
src=${1:-README.md}
runs=${2:-8}
dir=$(mktemp -d)
cp "$src" "$dir/page.md"
now() { python3 -c 'import time; print(time.time())'; }
since() { python3 -c "print(round($(now) - $1, 3))"; }
median() { python3 -c 'import sys, statistics as s; v = [float(x) for x in sys.stdin]; print(f"median {s.median(v):.2f}s  min {min(v):.2f}s  max {max(v):.2f}s  (n={len(v)})")'; }

t=$(now)
id=$(byagent publish "$dir/page.md" --private --expires 1h --title "byagent bench (deleted after)" --json | python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])')
first=$(since "$t")
cleanup() {
  local gone=""
  for _ in 1 2 3; do byagent delete "$id" --json >/dev/null 2>&1 && { gone=1; break; }; sleep 3; done
  [ -n "$gone" ] || echo "could not delete $id: run byagent delete $id" >&2
  rm -rf "$dir"
}
trap cleanup EXIT

: > "$dir/publish.txt"; : > "$dir/comments.txt"
for i in $(seq 1 "$runs"); do
  sleep 7
  echo "<!-- run $i -->" >> "$dir/page.md"
  t=$(now); byagent publish "$dir/page.md" --id "$id" --json >/dev/null && since "$t" >> "$dir/publish.txt"
  t=$(now); byagent comments "$id" --json >/dev/null && since "$t" >> "$dir/comments.txt"
done

echo "page:        $(wc -c < "$src" | tr -d ' ') bytes of Markdown"
echo "first publish: ${first}s"
echo "republish:   $(median < "$dir/publish.txt")"
echo "read comments: $(median < "$dir/comments.txt")"
