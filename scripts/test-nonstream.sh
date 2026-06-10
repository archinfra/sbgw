#!/usr/bin/env bash
set -euo pipefail
BASE_URL=${BASE_URL:-http://127.0.0.1:12224}
TOKEN=${TOKEN:-sk-local-dev-001}
curl -sS "$BASE_URL/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"model":"qwen3.6-w8a8","messages":[{"role":"user","content":"你好，计算下中国的面积，对比太平洋的"}],"stream":false}'
