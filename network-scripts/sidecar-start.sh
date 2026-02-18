#!/usr/bin/env sh
set -eu

DEPLOY_DATA_DIR=${DEPLOY_DATA_DIR:-/deploy-data}
DRIVER_ADDRESS_FILE="$DEPLOY_DATA_DIR/driver-address"

if [ ! -f "$DRIVER_ADDRESS_FILE" ]; then
  echo "missing required driver address file: $DRIVER_ADDRESS_FILE"
  exit 1
fi
DRIVER_ADDRESS=$(cat "$DRIVER_ADDRESS_FILE")
if [ -z "$DRIVER_ADDRESS" ]; then
  echo "empty driver address in file: $DRIVER_ADDRESS_FILE"
  exit 1
fi
echo "Using driver address: $DRIVER_ADDRESS"

EXTERNAL_VP_ENABLED=${EXTERNAL_VP_ENABLED:-true}
EXTERNAL_VP_ID=${EXTERNAL_VP_ID:-0x00000000000000000000}
EXTERNAL_VP_URL=${EXTERNAL_VP_URL:-dns:///beacon-vp-provider:50051}

cat > /tmp/sidecar.yaml << EOFCONFIG
log:
  level: "debug"
  mode: "pretty"

api:
  listen: ":8080"

metrics:
  pprof: true

p2p:
  listen: "/ip4/0.0.0.0/tcp/8880"
  bootnodes:
    - /dns4/relay-sidecar-1/tcp/8880/p2p/16Uiu2HAmFUiPYAJ7bE88Q8d7Kznrw5ifrje2e5QFyt7uFPk2G3iR
  dht-mode: "server"
  mdns: true

evm:
  chains:
    - "http://anvil:8545"
    - "http://anvil-settlement:8546"
  max-calls: 30
EOFCONFIG

if [ "$EXTERNAL_VP_ENABLED" = "true" ]; then
  cat >> /tmp/sidecar.yaml << EOFCONFIG

external-voting-power-providers:
  - id: "$EXTERNAL_VP_ID"
    url: "$EXTERNAL_VP_URL"
    secure: false
EOFCONFIG
fi

RELAY_SIDECAR_BIN=${RELAY_SIDECAR_BIN:-/app/relay_sidecar}
if [ -x /workspace/bin/relay_sidecar ]; then
  RELAY_SIDECAR_BIN=/workspace/bin/relay_sidecar
fi

exec "$RELAY_SIDECAR_BIN" \
  --config /tmp/sidecar.yaml \
  --driver.chain-id 31337 \
  --driver.address "$DRIVER_ADDRESS" \
  --secret-keys "$1" \
  --storage-dir "$2"
