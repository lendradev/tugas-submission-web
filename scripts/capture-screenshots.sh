#!/usr/bin/env bash
# capture-screenshots.sh — mengambil tangkapan layar UI ke folder screenshots/.
#
# Syarat: container dengan Chromium + modul puppeteer-core (image mcp/puppeteer)
# dan aplikasi yang sudah berjalan. Root project dua level di atas workspace/.
#
#   TS_BASE_URL=http://172.17.0.4:8080 scripts/capture-screenshots.sh
set -euo pipefail

BASE_URL="${TS_BASE_URL:-http://127.0.0.1:8080}"
CONTAINER="${TS_BROWSER_CONTAINER:-}"
# Folder screenshots/ berada di root project, satu level di atas workspace/.
DEST="$(cd "$(dirname "$0")/../.." && pwd)/screenshots"

if [ -z "$CONTAINER" ]; then
  echo "TS_BROWSER_CONTAINER wajib diisi: nama container browser yang punya puppeteer-core." >&2
  exit 1
fi

TMP_OUT="/tmp/tugas-shots"
docker exec "$CONTAINER" rm -rf "$TMP_OUT"
docker exec "$CONTAINER" mkdir -p "$TMP_OUT"
docker cp "$(dirname "$0")/shots.mjs" "$CONTAINER:/tmp/shots.mjs"
docker exec -w /project "$CONTAINER" node /tmp/shots.mjs "$BASE_URL" "$TMP_OUT"

mkdir -p "$DEST"
docker cp "$CONTAINER:$TMP_OUT/." "$DEST/"
ls -1 "$DEST"
