#!/usr/bin/env bash
set -euo pipefail
POSTURE="${1:?uso: collect_pose.sh standing|sitting|lying}"
PORT="${PORT:-8787}"
INFERENCE_URL="${INFERENCE_SERVER_URL:-http://127.0.0.1:8790}"

curl -sf -X POST "$INFERENCE_URL/collect/start" \
  -H 'Content-Type: application/json' \
  -d "{\"posture\":\"$POSTURE\"}" >/dev/null

echo "gravando $POSTURE — fique na pose 2s…"
sleep 2

curl -sS -X POST "http://127.0.0.1:${PORT}/api/collect/save" \
  -H 'Content-Type: application/json' \
  -d "{\"posture\":\"$POSTURE\"}" | python3 -m json.tool
