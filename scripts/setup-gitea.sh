#!/usr/bin/env bash
# setup-gitea.sh — menyiapkan Gitea lokal sebagai simulasi GitHub untuk pengujian.
#
# Membuat: user "tester" (password), token API, dan repo "tugas" (branch main).
# Semua data resides di /mnt/workstation/cache/gitea-test.
set -euo pipefail

GITEA_TAG="28.4.0"
BASE="${TS_GITEA_DATA_DIR:-/mnt/workstation/cache/gitea-test}"
NAME="sbx-tugas-gitea"
NET="sbx-tugas-net"
PORT="13000"
API="http://127.0.0.1:${PORT}"
OWNER="tester"
REPO="tugas"
USER="tester"
# Kredensial khusus mesin uji lokal, bukan kredensial produksi.
PASS="${TS_GITEA_PASS:-tester-password-123}"
TOKEN="${GITEA_TOKEN:-gitea-token-for-integration-test}"

mkdir -p "$BASE/data" "$BASE/log"

if ! docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; then
  docker network create "$NET" 2>/dev/null || true
  docker run -d --rm --name "$NAME" --network "$NET" \
    --cap-drop=ALL \
    --cap-add=CHOWN --cap-add=SETUID --cap-add=SETGID --cap-add=DAC_OVERRIDE --cap-add=FOWNER \
    --security-opt no-new-privileges \
    --memory 1g --cpus 2 --pids-limit 256 \
    -e USER_UID=1000 -e USER_GID=1000 \
    -e GITEA__database__DB_TYPE=sqlite3 \
    -e GITEA__server__ROOT_URL="http://127.0.0.1:${PORT}/" \
    -e GITEA__server__HTTP_PORT=3000 \
    -e GITEA__server__DISABLE_SSH=true \
    -e GITEA__service__DISABLE_REGISTRATION=true \
    -e GITEA__security__INSTALL_LOCK=true \
    -e GITEA__repository__DEFAULT_BRANCH=main \
    -v "$BASE/data:/data" \
    -p "127.0.0.1:${PORT}:3000" \
    "gitea/gitea:${GITEA_TAG}"
fi

echo "Menunggu Gitea siap..."
for i in $(seq 1 60); do
  if curl -fsS "${API}/api/v1/version" >/dev/null 2>&1; then
    echo "Gitea siap di ${API}"
    break
  fi
  sleep 1
done

# Akun admin pertama dibuat lewat CLI di dalam container.
if ! curl -fsS -u "${USER}:${PASS}" "${API}/api/v1/user" >/dev/null 2>&1; then
  docker exec -u git "$NAME" gitea admin user create \
    --username "$USER" --password "$PASS" --email "tester@example.com" \
    --must-change-password=false --admin 2>&1 | tail -2 || true
fi

# Token API untuk klien.
curl -fsS -X POST "${API}/api/v1/users/${USER}/tokens" \
  -H "Content-Type: application/json" \
  -u "${USER}:${PASS}" \
  -d "{\"name\":\"tugas-test\",\"scopes\":[\"write:repository\",\"write:user\"]}" \
  > "$BASE/token.json" 2>/dev/null || true

if [ ! -s "$BASE/token.json" ]; then
  # Gitea modern: token lewat basic auth pada endpoint tokens.
  curl -fsS -X POST "${API}/api/v1/users/${USER}/tokens" \
    -u "${USER}:${PASS}" \
    -d "{\"name\":\"tugas-test-2\",\"scopes\":[\"write:repository\"]}" \
    > "$BASE/token.json"
fi

# Repo tujuan.
if ! curl -fsS -u "${USER}:${PASS}" "${API}/api/v1/repos/${OWNER}/${REPO}" >/dev/null 2>&1; then
  curl -fsS -X POST "${API}/api/v1/user/repos" \
    -H "Content-Type: application/json" \
    -u "${USER}:${PASS}" \
    -d "{\"name\":\"${REPO}\",\"private\":false,\"auto_init\":true,\"default_branch\":\"main\"}" \
    >/dev/null
  echo "Repo ${OWNER}/${REPO} dibuat"
else
  echo "Repo ${OWNER}/${REPO} sudah ada"
fi

# Pastikan branch main ada.
curl -fsS -u "${USER}:${PASS}" "${API}/api/v1/repos/${OWNER}/${REPO}/branches/main" >/dev/null 2>&1 \
  || curl -fsS -X POST "${API}/api/v1/repos/${OWNER}/${REPO}/branches" \
       -H "Content-Type: application/json" -u "${USER}:${PASS}" \
       -d "{\"new_branch_name\":\"main\",\"old_branch_name\":\"master\"}" >/dev/null

TOKEN_VALUE="$(python3 -c "import json;print(json.load(open('$BASE/token.json')).get('sha1',''))" 2>/dev/null || true)"
if [ -z "$TOKEN_VALUE" ]; then
  TOKEN_VALUE="$(python3 -c "import json;d=json.load(open('$BASE/token.json'));print(d.get('id') or d.get('token') or d.get('sha1') or '')" 2>/dev/null || true)"
fi

echo "TOKEN=${TOKEN_VALUE}"
echo "API=${API}"
echo "OWNER=${OWNER}"
echo "REPO=${REPO}"
