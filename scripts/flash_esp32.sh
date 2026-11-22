#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SERIAL="${SERIAL:-/dev/ttyUSB0}"
IDF_EXPORT="${IDF_EXPORT:-$HOME/.espressif/v6.1/esp-idf/export.sh}"
CSI_NODE_ID="${CSI_NODE_ID:-ESP_NODE_01}"
CSI_UDP_HOST="${CSI_UDP_HOST:-192.168.1.26}"
CSI_UDP_PORT="${CSI_UDP_PORT:-9001}"
CSI_WIFI_SSID="${CSI_WIFI_SSID:-Godi Transportes}"
CSI_WIFI_PASSWORD="${CSI_WIFI_PASSWORD:-Godi2026}"

if [[ ! -f "$IDF_EXPORT" ]]; then
  echo "ESP-IDF export não encontrado: $IDF_EXPORT" >&2
  exit 1
fi

SDK="$ROOT/firmware/sdkconfig"
if [[ ! -f "$SDK" ]]; then
  echo "sdkconfig ausente: $SDK" >&2
  exit 1
fi

set_cfg() {
  local key="$1" val="$2"
  if grep -q "^${key}=" "$SDK"; then
    sed -i "s|^${key}=.*|${key}=${val}|" "$SDK"
  else
    echo "${key}=${val}" >>"$SDK"
  fi
}

set_cfg CONFIG_CSI_NODE_ID "\"${CSI_NODE_ID}\""
set_cfg CONFIG_CSI_UDP_HOST "\"${CSI_UDP_HOST}\""
set_cfg CONFIG_CSI_UDP_PORT "${CSI_UDP_PORT}"
set_cfg CONFIG_CSI_WIFI_SSID "\"${CSI_WIFI_SSID}\""
set_cfg CONFIG_CSI_WIFI_PASSWORD "\"${CSI_WIFI_PASSWORD}\""

echo "flash ${CSI_NODE_ID} -> ${CSI_UDP_HOST}:${CSI_UDP_PORT} ssid=${CSI_WIFI_SSID} serial=${SERIAL}"

# shellcheck disable=SC1090
sg dialout -c ". \"$IDF_EXPORT\" && cd \"$ROOT/firmware\" && idf.py build && idf.py -p \"$SERIAL\" flash"
