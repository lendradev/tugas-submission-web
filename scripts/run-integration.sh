#!/usr/bin/env bash
# run-integration.sh — menjalankan pengujian end-to-end terhadap Gitea lokal.
set -euo pipefail

# URL Gitea dari dalam container jaringan (bukan dari host).
GITEA_API="${TS_GITEA_API:-http://sbx-tugas-gitea:3000}"
GITEA_TOKEN="${TS_GITHUB_TOKEN:-}"
OWNER="${TS_REPO_OWNER:-tester}"
REPO="${TS_REPO_NAME:-tugas}"
DATA_DIR="${TS_DATA_DIR:-/mnt/workstation/cache/tugas-test-data}"

if [ -z "$GITEA_TOKEN" ]; then
  echo "TS_GITHUB_TOKEN wajib diisi (token Gitea untuk pengujian)." >&2
  exit 1
fi

rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

# Jalankan server di dalam container dengan build binary statis.
docker build -t sbx-tugas-app:dev . >/dev/null

docker rm -f sbx-tugas-app >/dev/null 2>&1 || true
docker run -d --name sbx-tugas-app --network sbx-tugas-net \
  --cap-drop=ALL --security-opt no-new-privileges --read-only \
  --memory 256m --cpus 1 --pids-limit 128 \
  --tmpfs /data:rw,nosuid,nodev,size=64m,mode=1777 \
  -e TS_ADDR=0.0.0.0:8080 \
  -e TS_DATA_DIR=/data \
  -e TS_REPO_OWNER="$OWNER" \
  -e TS_REPO_NAME="$REPO" \
  -e TS_REPO_BRANCH=main \
  -e TS_GITHUB_TOKEN="$GITEA_TOKEN" \
  -e TS_GITHUB_API="$GITEA_API" \
  -e TS_GITEA_MODE=1 \
  -e TS_ADMIN_USERNAME=admin \
  -e TS_ADMIN_PASSWORD=admin-password-123 \
  -p 127.0.0.1:18081:8080 \
  sbx-tugas-app:dev >/dev/null

sleep 3
docker logs sbx-tugas-app 2>&1 | tail -5

cleanup() {
  docker rm -f sbx-tugas-app >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Pengujian dijalankan di dalam container Go memakai cache bersama.
docker run --rm --network sbx-tugas-net \
  --user "$(id -u):$(id -g)" \
  --cap-drop=ALL --security-opt no-new-privileges --read-only \
  --memory 512m --cpus 1 --pids-limit 128 \
  --tmpfs /tmp:rw,exec,nosuid,nodev,size=256m,mode=1777 \
  -e HOME=/tmp -e GOCACHE=/gocache -e GOMODCACHE=/gomodcache \
  -v /mnt/workstation/cache/gobuild:/gocache \
  -v /mnt/workstation/cache/gomod:/gomodcache \
  -v "$PWD:/src" -w /src \
  -e TS_TEST_BASE=http://sbx-tugas-app:8080 \
  golang:1.26-alpine go run ./cmd/integration-test
