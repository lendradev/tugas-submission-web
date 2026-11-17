#!/usr/bin/env bash
# vendor.sh — mengunduh Alpine.js build CSP ke static/ dan memverifikasi integritas.
#
# Dipakai agar dependensi frontend punya versi terkunci dan pemeriksaan hash.
# Jalankan dari folder workspace/.
set -euo pipefail

ALPINE_VERSION="3.17.5"
ALPINE_SHA256="48c170c92032e527cbae71b8ce75cf1f96ca06fa869a6120ef0fe438d8e92c36"

OUT_DIR="internal/web/static"
OUT_FILE="$OUT_DIR/alpine.min.js"
URL="https://cdn.jsdelivr.net/npm/@alpinejs/csp@${ALPINE_VERSION}/dist/cdn.min.js"

mkdir -p "$OUT_DIR"
curl -fsSL "$URL" -o "$OUT_FILE"

ACTUAL="$(sha256sum "$OUT_FILE" | cut -d' ' -f1)"
if [ "$ACTUAL" != "$ALPINE_SHA256" ]; then
  echo "PERINGATAN: hash Alpine.js tidak cocok." >&2
  echo "  diharapkan: $ALPINE_SHA256" >&2
  echo "  aktual   : $ACTUAL" >&2
  echo "  Perbarui ALPINE_SHA256 di script ini setelah memeriksa rilis upstream." >&2
  exit 1
fi

echo "Alpine.js CSP ${ALPINE_VERSION} terpasang di ${OUT_FILE}"
