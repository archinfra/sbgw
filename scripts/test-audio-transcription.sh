#!/usr/bin/env bash
set -euo pipefail

BASE_URL=${BASE_URL:-http://127.0.0.1:12224}
ROUTE_PREFIX=${ROUTE_PREFIX:-/mimo-asr}
TOKEN=${TOKEN:-sk-local-dev-001}
MODEL=${MODEL:-mimo-asr}
AUDIO_FILE=${AUDIO_FILE:-}
LANGUAGE=${LANGUAGE:-zh}
RESPONSE_FORMAT=${RESPONSE_FORMAT:-json}

if [[ -z "$AUDIO_FILE" ]]; then
  echo "usage: AUDIO_FILE=/path/to/audio.wav $0" >&2
  exit 2
fi

curl -sS "$BASE_URL${ROUTE_PREFIX}/v1/audio/transcriptions" \
  -H "Authorization: Bearer $TOKEN" \
  -F "model=$MODEL" \
  -F "file=@${AUDIO_FILE}" \
  -F "language=$LANGUAGE" \
  -F "response_format=$RESPONSE_FORMAT"
