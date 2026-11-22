#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ -f "$ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT/.env"
  set +a
fi

HOST_BIND="${HOST_BIND:-127.0.0.1}"
PORT="${PORT:-8787}"
INFERENCE_PORT="${INFERENCE_PORT:-8790}"
INFERENCE_SERVER_URL="${INFERENCE_SERVER_URL:-http://127.0.0.1:${INFERENCE_PORT}}"
DEVICE="${DEVICE:-cuda}"
ROUTER="${ROUTER:-192.168.1.1}"
ROOM_W="${ROOM_W:-6}"
ROOM_D="${ROOM_D:-4.5}"
CSI_UDP="${CSI_UDP:-:9001}"
CSI_STIM="${CSI_STIM:-}"
VENV="${ROOT}/inference/.venv"

cleanup() {
  if [[ -n "${INF_PID:-}" ]]; then kill "$INF_PID" 2>/dev/null || true; fi
  if [[ -n "${SRV_PID:-}" ]]; then kill "$SRV_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT INT TERM

if [[ ! -x "$VENV/bin/python" ]]; then
  echo "crie o venv: make inference-venv" >&2
  exit 1
fi

echo "→ inference em ${HOST_BIND}:${INFERENCE_PORT}"
(
  cd "$ROOT/inference"
  HOST_BIND="$HOST_BIND" PORT="$INFERENCE_PORT" DEVICE="$DEVICE" \
    "$VENV/bin/python" -m uvicorn main:app --host "$HOST_BIND" --port "$INFERENCE_PORT"
) &
INF_PID=$!

for i in $(seq 1 40); do
  if curl -sf "${INFERENCE_SERVER_URL}/health" >/dev/null; then
    break
  fi
  sleep 0.25
done

echo "→ server Go UDP em ${HOST_BIND}:${PORT} (${CSI_UDP})"
(
  cd "$ROOT/server"
  HOST_BIND="$HOST_BIND" PORT="$PORT" INFERENCE_SERVER_URL="$INFERENCE_SERVER_URL" \
    go run ./cmd/server \
      -router "$ROUTER" \
      -port "$PORT" \
      -room-w "$ROOM_W" \
      -room-d "$ROOM_D" \
      -udp "$CSI_UDP" \
      -stim "$CSI_STIM" \
      -inference "$INFERENCE_SERVER_URL" \
      -no-ui
) &
SRV_PID=$!

wait "$SRV_PID"
