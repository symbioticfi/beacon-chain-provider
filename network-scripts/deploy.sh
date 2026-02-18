#!/usr/bin/env sh
set -eu

ANVIL_RPC_URL=${ANVIL_RPC_URL:-http://anvil:8545}
SETTLEMENT_RPC_URL=${SETTLEMENT_RPC_URL:-http://anvil-settlement:8546}
DEPLOYER_PRIVATE_KEY=${DEPLOYER_PRIVATE_KEY:-0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80}
DEPLOY_DATA_DIR=${DEPLOY_DATA_DIR:-/deploy-data}
KEYREG_ADDR=${KEYREG_ADDR:-}
SUPERSUM_REPO=${SUPERSUM_REPO:-/super-sum}
BEACON_VP_CHAIN_ID=${BEACON_VP_CHAIN_ID:-4000000000}
DRIVER_ADDRESS=${DRIVER_ADDRESS:-0x43C27243F96591892976FFf886511807B65a33d5}
BEACON_VP_PROVIDER_ADDRESS=${BEACON_VP_PROVIDER_ADDRESS:-0x0000000000000000000000000000000000000001}

mkdir -p "$DEPLOY_DATA_DIR"

echo "Waiting for anvils..."
until cast client --rpc-url "$ANVIL_RPC_URL" >/dev/null 2>&1; do sleep 1; done
until cast client --rpc-url "$SETTLEMENT_RPC_URL" >/dev/null 2>&1; do sleep 1; done

if [ ! -d "$SUPERSUM_REPO" ] || [ ! -f "$SUPERSUM_REPO/network-scripts/deploy.sh" ]; then
  echo "missing symbiotic-super-sum repo at $SUPERSUM_REPO"
  exit 1
fi

if [ ! -f "$SUPERSUM_REPO/network-scripts/deploy.sh" ]; then
  echo "missing super-sum deploy script at $SUPERSUM_REPO/network-scripts/deploy.sh"
  exit 1
fi

echo "Running super-sum deploy pipeline..."
DEPLOY_LOG=$(mktemp)
(
  cd "$SUPERSUM_REPO"
  ANVIL_RPC_URL="$ANVIL_RPC_URL" \
  SETTLEMENT_RPC_URL="$SETTLEMENT_RPC_URL" \
  OPERATOR_COUNT="${OPERATOR_COUNT:-4}" \
  NUM_AGGREGATORS="${NUM_AGGREGATORS:-1}" \
  NUM_COMMITTERS="${NUM_COMMITTERS:-1}" \
  DEPLOYER_PRIVATE_KEY="$DEPLOYER_PRIVATE_KEY" \
  ./network-scripts/deploy.sh
) | tee "$DEPLOY_LOG"

PARSED_KEYREG=$(grep -E "KeyRegistry deployed at:" "$DEPLOY_LOG" | grep -Eo '0x[0-9a-fA-F]{40}' | tail -1 || true)
PARSED_DRIVER=$(grep -E "ValSetDriver deployed at:" "$DEPLOY_LOG" | grep -Eo '0x[0-9a-fA-F]{40}' | tail -1 || true)
rm -f "$DEPLOY_LOG"

if [ -n "$PARSED_DRIVER" ]; then
  DRIVER_ADDRESS="$PARSED_DRIVER"
fi
echo "$DRIVER_ADDRESS" > "$DEPLOY_DATA_DIR/driver-address"

DRIVER_CODE=$(cast code --rpc-url "$ANVIL_RPC_URL" "$DRIVER_ADDRESS" 2>/dev/null || echo "0x")
if [ "$DRIVER_CODE" != "0x" ]; then
  echo "ValSetDriver deployed at $DRIVER_ADDRESS"
else
  echo "ValSetDriver not found at $DRIVER_ADDRESS after deploy"
  exit 1
fi

if [ -n "$PARSED_KEYREG" ]; then
  KEYREG_ADDR="$PARSED_KEYREG"
fi
if [ -z "$KEYREG_ADDR" ] || [ "$KEYREG_ADDR" = "null" ]; then
  KEYREG_ADDR=$(cast call --rpc-url "$ANVIL_RPC_URL" "$DRIVER_ADDRESS" "getKeyRegistry()((uint64,address))" | awk -F'[(), ]+' '{print $3}')
fi
if [ -z "$KEYREG_ADDR" ] || [ "$KEYREG_ADDR" = "null" ] || [ "$KEYREG_ADDR" = "0x0000000000000000000000000000000000000000" ]; then
  echo "failed to resolve key registry address from driver"
  exit 1
fi
echo "$KEYREG_ADDR" > "$DEPLOY_DATA_DIR/keyregistry-address"

echo "Advancing finalized block so relay can read driver at finalized..."
cast rpc --rpc-url "$ANVIL_RPC_URL" evm_setIntervalMining 1 >/dev/null
cast rpc --rpc-url "$SETTLEMENT_RPC_URL" evm_setIntervalMining 1 >/dev/null
cast rpc --rpc-url "$ANVIL_RPC_URL" anvil_mine 2 >/dev/null
cast rpc --rpc-url "$SETTLEMENT_RPC_URL" anvil_mine 2 >/dev/null

retries=0
while :; do
  FINALIZED_DRIVER_CODE=$(cast rpc --rpc-url "$ANVIL_RPC_URL" eth_getCode "$DRIVER_ADDRESS" finalized | tr -d '"')
  if [ "$FINALIZED_DRIVER_CODE" != "0x" ]; then
    break
  fi
  retries=$((retries + 1))
  if [ "$retries" -ge 30 ]; then
    echo "driver code is still missing at finalized after waiting"
    exit 1
  fi
  cast rpc --rpc-url "$ANVIL_RPC_URL" anvil_mine 1 >/dev/null
  sleep 1
done

echo "Registering external voting power provider in ValSetDriver..."
set +e
  cast send --rpc-url "$ANVIL_RPC_URL" --private-key "$DEPLOYER_PRIVATE_KEY" "$DRIVER_ADDRESS" \
    "addVotingPowerProvider((uint64,address))" "($BEACON_VP_CHAIN_ID,$BEACON_VP_PROVIDER_ADDRESS)" >/dev/null
ADD_VP_EXIT=$?
set -e
if [ "$ADD_VP_EXIT" -ne 0 ]; then
  echo "external voting power provider may already be registered; continuing"
fi

printf '{"key_registry":"%s","beacon_voting_power_provider_chain_id":%s}\n' \
  "$KEYREG_ADDR" "$BEACON_VP_CHAIN_ID" > "$DEPLOY_DATA_DIR/deployment-completed.json"
echo "$(date): deployment completed" > "$DEPLOY_DATA_DIR/deployment-complete.marker"

echo "KeyRegistry used from env/artifact: $KEYREG_ADDR"
echo "Driver address: $DRIVER_ADDRESS"
echo "Beacon voting power provider chainId configured: $BEACON_VP_CHAIN_ID"
