#!/usr/bin/env bash
# Live check against the Meta Model API. Run from the repo root AFTER putting
# your key in .env. Discovers model ids and probes the chat endpoint.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi
if [ -z "${META_API_KEY:-}" ]; then
  echo "META_API_KEY is empty. Put your key in .env first." >&2
  exit 1
fi
BASE="${META_BASE_URL:-https://api.meta.ai/v1}"

echo "== 1. list models =="
MODELS="$(curl -sf "$BASE/models" -H "Authorization: Bearer $META_API_KEY")"
echo "$MODELS" | head -c 2000
echo

MUSE_ID=""
if [ -n "${META_MUSE_MODEL:-}" ]; then
  MUSE_ID="$META_MUSE_MODEL"
else
  MUSE_ID="$(echo "$MODELS" | grep -o 'muse-spark-1\.3-contributor' | head -n 1 || true)"
  if [ -z "$MUSE_ID" ]; then
    MUSE_ID="$(echo "$MODELS" | grep -o 'muse-spark[^"]*' | head -n 1 || true)"
  fi
fi
if [ -z "$MUSE_ID" ]; then
  echo "No muse-spark model id found. Set META_MUSE_MODEL explicitly." >&2
  exit 2
fi

echo "== 2. chat probe (model: $MUSE_ID) =="
curl -sf "$BASE/chat/completions" \
  -H "Authorization: Bearer $META_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"$MUSE_ID\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly: live-ok\"}]}" \
  | head -c 800
echo

echo "== 3. sam-3.1 route probe (POST /responses, invalid body on purpose) =="
CODE="$(curl -s -o /tmp/pl-sam-probe.json -w "%{http_code}" "$BASE/responses" \
  -H "Authorization: Bearer $META_API_KEY" -H "Content-Type: application/json" \
  -d '{"model":"sam-3.1","input":[]}')"
echo "HTTP $CODE (404 = route missing; 400/422 = route exists, body rejected as intended)"
head -c 400 /tmp/pl-sam-probe.json; echo
