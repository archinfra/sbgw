#!/usr/bin/env bash
set -euo pipefail
BASE_URL=${BASE_URL:-http://127.0.0.1:12224}
ROUTE_PREFIX=${ROUTE_PREFIX:-}
TOKEN=${TOKEN:-sk-local-dev-001}
MODEL=${MODEL:-qwen3.6}

payload=$(cat <<JSON
{"model":"${MODEL}","messages":[{"role":"user","content":"你好，计算下中国的面积，对比太平洋的"}],"stream":true}
JSON
)

curl -N "$BASE_URL${ROUTE_PREFIX}/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d "$payload"
