#!/usr/bin/env sh
set -eu

DEPLOY_DATA_DIR=${DEPLOY_DATA_DIR:-/deploy-data}
BEACON_NODE_URL=${BEACON_NODE_URL:-https://eth-hoodibeacon.g.alchemy.com/v2/qHvFeod_nN-w70vKmuloG0Hep9BNID3K}
ETH_RPC_URL=${ETH_RPC_URL:-http://anvil:8545}
KEY_TAG=${KEY_TAG:-34}
CHAIN_ID=${CHAIN_ID:-31337}
LISTEN=${LISTEN:-:50051}
PROVIDER_MOCK=${PROVIDER_MOCK:-true}
REQUEST_TIMEOUT=${REQUEST_TIMEOUT:-60s}

echo "Waiting for keyregistry deployment..."
until [ -f "$DEPLOY_DATA_DIR/keyregistry-address" ]; do sleep 2; done
KEYREG_ADDR=$(cat "$DEPLOY_DATA_DIR/keyregistry-address")

cat > /tmp/provider.yaml << EOFCONFIG
grpc:
  listen: "$LISTEN"

beacon:
  node_url: "$BEACON_NODE_URL"

ethereum:
  rpc_url: "$ETH_RPC_URL"

key_registry:
  address: "$KEYREG_ADDR"
  chain_id: $CHAIN_ID
  key_tag: $KEY_TAG

timeouts:
  request: "$REQUEST_TIMEOUT"

log:
  level: "info"

provider:
  mock: $PROVIDER_MOCK
EOFCONFIG

cd /workspace
exec /app/beacon-vp --config /tmp/provider.yaml
