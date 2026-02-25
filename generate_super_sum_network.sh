#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUPER_SUM_DIR="${ROOT_DIR}/super-sum"
NETWORK_DIR="${SUPER_SUM_DIR}/temp-network"
COMPOSE_FILE="${NETWORK_DIR}/docker-compose.yml"
DEPLOY_DATA_DIR="${NETWORK_DIR}/deploy-data"
REGISTER_SCRIPT="${DEPLOY_DATA_DIR}/register-external-vp.sh"
PROVIDER_CONFIG="${DEPLOY_DATA_DIR}/beacon-vp.config.yaml"
DEPLOY_WRAPPER_SCRIPT="${DEPLOY_DATA_DIR}/deploy.sh"
GENESIS_WRAPPER_SCRIPT="${DEPLOY_DATA_DIR}/genesis-generator.sh"
SIDECAR_WRAPPER_SCRIPT="${DEPLOY_DATA_DIR}/sidecar-start.sh"
SIDECAR_BASE_SCRIPT="${SUPER_SUM_DIR}/network-scripts/sidecar-start.sh"

EXTERNAL_VP_CHAIN_ID="${EXTERNAL_VP_CHAIN_ID:-4000000000}"
EXTERNAL_VP_ADDRESS="${EXTERNAL_VP_ADDRESS:-0x0000000000000000000000000000000000000001}"
DRIVER_ADDRESS="${DRIVER_ADDRESS:-0x43C27243F96591892976FFf886511807B65a33d5}"
DEPLOYER_PRIVATE_KEY="${DEPLOYER_PRIVATE_KEY:-0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80}"
ANVIL_RPC_URL="${ANVIL_RPC_URL:-http://anvil:8545}"
EXTERNAL_VP_GRPC_URL="${EXTERNAL_VP_GRPC_URL:-beacon-vp-provider:50051}"
BEACON_VP_IMAGE="${BEACON_VP_IMAGE:-beacon-vp-local:dev}"
BEACON_NODE_URL="${BEACON_NODE_URL:-https://ethereum-hoodi-beacon-api.publicnode.com}"
KEY_REGISTRY_ADDRESS="${KEY_REGISTRY_ADDRESS:-0xe1557A820E1f50dC962c3392b875Fe0449eb184F}"
RELAY_IMAGE="symbioticfi/relay:local"
EXTERNAL_VP_PROVIDER_ID="0x${EXTERNAL_VP_ADDRESS#0x}"
EXTERNAL_VP_PROVIDER_ID="${EXTERNAL_VP_PROVIDER_ID:0:22}"

die() {
  echo "$1"
  exit 1
}

if [ ! -d "${SUPER_SUM_DIR}" ]; then
  die "missing submodule directory: ${SUPER_SUM_DIR}
run: git submodule update --init --recursive"
fi

if [ ! -x "${SUPER_SUM_DIR}/generate_network.sh" ]; then
  die "missing super-sum generator: ${SUPER_SUM_DIR}/generate_network.sh"
fi

echo "[INFO] Generating super-sum network..."
(
  cd "${SUPER_SUM_DIR}"
  ./generate_network.sh
)

if [ ! -f "${COMPOSE_FILE}" ]; then
  die "missing generated compose file: ${COMPOSE_FILE}"
fi

if ! docker image inspect "${RELAY_IMAGE}" >/dev/null 2>&1; then
  die "missing local relay image ${RELAY_IMAGE}
build/tag it before running this script"
fi

perl -i -pe "s#image:\\s*symbioticfi/relay:[^\\s]+#image: ${RELAY_IMAGE}#g" "${COMPOSE_FILE}"

cat > "${PROVIDER_CONFIG}" <<EOF
grpc:
  listen: ":50051"

beacon:
  node_url: "${BEACON_NODE_URL}"

ethereum:
  rpc_url: "${ANVIL_RPC_URL}"

key_registry:
  address: "${KEY_REGISTRY_ADDRESS}"
  chain_id: 31337
  key_tag: 32

timeouts:
  request: 15s

log:
  level: "debug"

mock: true
EOF

cat > "${REGISTER_SCRIPT}" <<EOF
#!/usr/bin/env sh
set -eu

if [ -f /deploy-data/external-vp-registered.marker ]; then
  echo "External voting power provider already registered, skipping."
  exit 0
fi

echo "Registering external voting power provider..."
cast send \\
  --rpc-url "${ANVIL_RPC_URL}" \\
  --private-key "${DEPLOYER_PRIVATE_KEY}" \\
  "${DRIVER_ADDRESS}" \\
  "addVotingPowerProvider((uint64,address))" \\
  "(${EXTERNAL_VP_CHAIN_ID},${EXTERNAL_VP_ADDRESS})"

echo "\$(date): external voting power provider registered" > /deploy-data/external-vp-registered.marker
echo "External voting power provider registered."
EOF

cat > "${DEPLOY_WRAPPER_SCRIPT}" <<'EOF'
#!/usr/bin/env sh
set -eu

ANVIL_RPC_URL=${ANVIL_RPC_URL:-http://anvil:8545}
SETTLEMENT_RPC_URL=${SETTLEMENT_RPC_URL:-http://anvil-settlement:8546}
VALSET_DRIVER_ADDRESS=${VALSET_DRIVER_ADDRESS:-0x43C27243F96591892976FFf886511807B65a33d5}
DEPLOY_DATA_DIR=${DEPLOY_DATA_DIR:-/deploy-data}
DEPLOY_CONFIG_TEMPLATE=${DEPLOY_CONFIG_TEMPLATE:-/app/script/my-relay-deploy.toml}
DEPLOY_CONFIG_TARGET=${DEPLOY_CONFIG_TARGET:-/app/temp-network/my-relay-deploy.toml}

echo "Waiting for anvil endpoints..."
until cast client --rpc-url "${ANVIL_RPC_URL}" >/dev/null 2>&1; do sleep 1; done
until cast client --rpc-url "${SETTLEMENT_RPC_URL}" >/dev/null 2>&1; do sleep 1; done

existing_driver_code="$(cast code --rpc-url "${ANVIL_RPC_URL}" "${VALSET_DRIVER_ADDRESS}" 2>/dev/null || true)"
if [ -n "${existing_driver_code}" ] && [ "${existing_driver_code}" != "0x" ]; then
  echo "Detected existing ValSetDriver at ${VALSET_DRIVER_ADDRESS}, skipping deployment."
  echo "{}" > "${DEPLOY_DATA_DIR}/deployment-completed.json"
  echo "$(date): Deployment already present, skipped redeploy" > "${DEPLOY_DATA_DIR}/deployment-complete.marker"
  exit 0
fi

if [ -f "${DEPLOY_DATA_DIR}/deployment-complete.marker" ]; then
  echo "Found stale deployment marker; cleaning up."
  rm -f \
    "${DEPLOY_DATA_DIR}/deployment-complete.marker" \
    "${DEPLOY_DATA_DIR}/deployment-completed.json" \
    "${DEPLOY_DATA_DIR}/genesis-complete.marker" \
    "${DEPLOY_DATA_DIR}/external-vp-registered.marker"
fi

echo "Resetting deploy config from template..."
cp "${DEPLOY_CONFIG_TEMPLATE}" "${DEPLOY_CONFIG_TARGET}"

yes | /bin/sh /app/network-scripts/deploy.sh
EOF

cat > "${GENESIS_WRAPPER_SCRIPT}" <<'EOF'
#!/usr/bin/env sh
set -eu

DEPLOY_DATA_DIR=${DEPLOY_DATA_DIR:-/deploy-data}
ANVIL_RPC_URL=${ANVIL_RPC_URL:-http://anvil:8545}
DRIVER_ADDRESS=${DRIVER_ADDRESS:-0x43C27243F96591892976FFf886511807B65a33d5}

if [ -f "${DEPLOY_DATA_DIR}/genesis-complete.marker" ]; then
  echo "Genesis already completed, skipping."
  exit 0
fi

echo "Waiting for deployment completion..."
until [ -f "${DEPLOY_DATA_DIR}/deployment-complete.marker" ]; do sleep 2; done

current_epoch="$(cast call --rpc-url "${ANVIL_RPC_URL}" "${DRIVER_ADDRESS}" "getCurrentEpoch()(uint48)" 2>/dev/null || true)"
case "${current_epoch}" in
  "" )
    ;;
  0x*|[0-9]* )
    current_epoch_num=$((current_epoch))
    if [ "${current_epoch_num}" -gt 0 ]; then
      echo "$(date): Genesis already committed on-chain (epoch=${current_epoch_num})" > "${DEPLOY_DATA_DIR}/genesis-complete.marker"
      echo "Genesis already committed on-chain, skipping regeneration."
      exit 0
    fi
    ;;
esac

exec /bin/sh /workspace/network-scripts/genesis-generator.sh
EOF

cp "${SIDECAR_BASE_SCRIPT}" "${SIDECAR_WRAPPER_SCRIPT}"
perl -0777 -i -pe 's#max-calls: 30\n#max-calls: 30\n\nexternal-voting-power-providers:\n  - id: "'"${EXTERNAL_VP_PROVIDER_ID}"'"\n    url: "'"${EXTERNAL_VP_GRPC_URL}"'"\n    secure: false\n#' "${SIDECAR_WRAPPER_SCRIPT}"
chmod +x "${REGISTER_SCRIPT}" "${DEPLOY_WRAPPER_SCRIPT}" "${GENESIS_WRAPPER_SCRIPT}" "${SIDECAR_WRAPPER_SCRIPT}"

perl -0777 -i -pe 's#working_dir: /app\n    command: \./network-scripts/deploy\.sh#working_dir: /app\n    entrypoint:\n      - /bin/sh\n      - /deploy-data/deploy.sh#g' "${COMPOSE_FILE}"
perl -0777 -i -pe 's#working_dir: /workspace\n    command: \./network-scripts/genesis-generator\.sh#working_dir: /workspace\n    entrypoint:\n      - /bin/sh\n      - /deploy-data/genesis-generator.sh#g' "${COMPOSE_FILE}"
perl -i -pe 's#/workspace/network-scripts/sidecar-start\.sh#/deploy-data/sidecar-start.sh#g' "${COMPOSE_FILE}"

perl -0777 -i -pe 's/depends_on:\n\s+genesis-generator:\n\s+condition: service_completed_successfully/depends_on:\n      external-vp-registrar:\n        condition: service_completed_successfully/g' "${COMPOSE_FILE}"

INSERT_FILE="$(mktemp)"
cat > "${INSERT_FILE}" <<'EOF'
  beacon-vp-provider:
    image: beacon-vp-local:dev
    container_name: beacon-vp-provider
    command:
      - --config
      - /app/config.yaml
    ports:
      - 50051:50051
    volumes:
      - ./deploy-data/beacon-vp.config.yaml:/app/config.yaml:ro
    depends_on:
      anvil:
        condition: service_healthy
    networks:
      - symbiotic-network

  external-vp-registrar:
    image: symbiotic-deployer
    container_name: symbiotic-external-vp-registrar
    entrypoint:
      - /bin/sh
      - /deploy-data/register-external-vp.sh
    volumes:
      - ./deploy-data:/deploy-data
    depends_on:
      deployer:
        condition: service_completed_successfully
      genesis-generator:
        condition: service_completed_successfully
    networks:
      - symbiotic-network

EOF
perl -i -pe "s#image: beacon-vp-local:dev#image: ${BEACON_VP_IMAGE}#g" "${INSERT_FILE}"

awk -v insert_file="${INSERT_FILE}" '
  BEGIN {
    while ((getline line < insert_file) > 0) {
      insert = insert line "\n"
    }
    close(insert_file)
  }
  /^networks:/ && !done {
    printf "%s", insert
    done = 1
  }
  { print }
' "${COMPOSE_FILE}" > "${COMPOSE_FILE}.tmp"
mv "${COMPOSE_FILE}.tmp" "${COMPOSE_FILE}"
rm -f "${INSERT_FILE}"

echo "[INFO] Patched ${COMPOSE_FILE} with post-genesis external voting power registration."
echo "[INFO] Relay image set to ${RELAY_IMAGE}."
echo "[INFO] Start network with:"
echo "  docker compose --project-directory ${NETWORK_DIR} up -d --build"
