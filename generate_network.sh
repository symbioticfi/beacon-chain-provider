#!/bin/bash
set -eu

RELAY_IMAGE_TAG="1.0.0"
DEFAULT_OPERATORS=4
DEFAULT_COMMITTERS=1
DEFAULT_AGGREGATORS=1
MAX_OPERATORS=50
DEMO_BEACON_NODE_URL="https://ethereum-hoodi-beacon-api.publicnode.com"
DEMO_KEY_TAG=15
DEMO_BEACON_VP_CHAIN_ID=4000000000
DEMO_BEACON_VP_PROVIDER_ADDRESS="0x0000000000000000000000000000000000000001"
DEMO_KEYREG_ADDRESS="0xe1557A820E1f50dC962c3392b875Fe0449eb184F"
DEFAULT_SUPERSUM_REPO_PATH=""
SUPERSUM_REPO_PATH=""
BASE_PRIVATE_KEY=1000000000000000000

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_status() { echo -e "${GREEN}[INFO]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
print_error() { echo -e "${RED}[ERROR]${NC} $1"; }
print_header() {
  echo -e "${BLUE}================================${NC}"
  echo -e "${BLUE}$1${NC}"
  echo -e "${BLUE}================================${NC}"
}

validate_number() {
  local num=$1
  local name=$2
  if ! [[ "$num" =~ ^[0-9]+$ ]] || [ "$num" -lt 1 ]; then
    print_error "$name must be a positive integer"
    exit 1
  fi
}

check_prereqs() {
  command -v docker >/dev/null 2>&1 || { print_error "docker is required"; exit 1; }
  if ! command -v docker-compose >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
    print_error "docker compose is required"
    exit 1
  fi

  if [ -z "$DEFAULT_SUPERSUM_REPO_PATH" ]; then
    DEFAULT_SUPERSUM_REPO_PATH=$(cd ../symbiotic-super-sum 2>/dev/null && pwd || true)
  fi
}

get_user_input() {
  echo
  print_header "Beacon Provider Demo Network Configuration"
  echo

  read -p "Enter number of operators (default: $DEFAULT_OPERATORS, max: $MAX_OPERATORS): " operators
  operators=${operators:-$DEFAULT_OPERATORS}
  validate_number "$operators" "Number of operators"

  read -p "Enter number of commiters (default: $DEFAULT_COMMITTERS): " committers
  committers=${committers:-$DEFAULT_COMMITTERS}
  validate_number "$committers" "Number of commiters"

  read -p "Enter number of aggregators (default: $DEFAULT_AGGREGATORS): " aggregators
  aggregators=${aggregators:-$DEFAULT_AGGREGATORS}
  validate_number "$aggregators" "Number of aggregators"

  if [ "$operators" -gt "$MAX_OPERATORS" ]; then
    print_error "Maximum operators is $MAX_OPERATORS"
    exit 1
  fi

  local total_special_roles=$((committers + aggregators))
  if [ "$total_special_roles" -gt "$operators" ]; then
    print_error "commiters + aggregators cannot exceed operators"
    exit 1
  fi

  print_status "Configuration: operators=$operators commiters=$committers aggregators=$aggregators"

  read -p "Path to symbiotic-super-sum repo (default: ${DEFAULT_SUPERSUM_REPO_PATH:-<required>}): " supersum_repo_path
  SUPERSUM_REPO_PATH=${supersum_repo_path:-$DEFAULT_SUPERSUM_REPO_PATH}
  if [ -z "$SUPERSUM_REPO_PATH" ] || [ ! -d "$SUPERSUM_REPO_PATH" ]; then
    print_error "symbiotic-super-sum repo path is required and must exist"
    exit 1
  fi
  if [ ! -f "$SUPERSUM_REPO_PATH/network-scripts/deploy.sh" ]; then
    print_error "invalid symbiotic-super-sum repo: network-scripts/deploy.sh not found"
    exit 1
  fi
}

generate_docker_compose() {
  local operators=$1
  local committers=$2
  local aggregators=$3

  local network_dir="temp-network"
  rm -rf "$network_dir"
  mkdir -p "$network_dir/deploy-data"
  chmod 777 "$network_dir/deploy-data"
  mkdir -p "$network_dir/cache" "$network_dir/broadcast" "$network_dir/out"
  chmod 777 "$network_dir/cache" "$network_dir/broadcast" "$network_dir/out"

  cat > "$network_dir/my-relay-deploy.toml" << EOFCONF
[31337]
endpoint_url = "http://anvil:8545"

[31338]
endpoint_url = "http://anvil-settlement:8546"

[1234567890]
endpoint_url = ""
keyRegistry = 31337
votingPowerProvider = [31337]
settlement = [
    31337,
    31338,
]
valSetDriver = 31337
EOFCONF

  for i in $(seq 1 "$operators"); do
    mkdir -p "$network_dir/data-$(printf "%02d" "$i")"
    chmod 777 "$network_dir/data-$(printf "%02d" "$i")"
  done

  cat > "$network_dir/docker-compose.yml" << EOFYAML
services:
  anvil:
    image: ghcr.io/foundry-rs/foundry:v1.4.3
    container_name: beacon-demo-anvil
    entrypoint: ["anvil"]
    command: "--port 8545 --chain-id 31337 --timestamp 1754051800 --auto-impersonate --slots-in-an-epoch 1 --accounts 20 --balance 10000 --gas-limit 30000000 --gas-price 10000000"
    environment:
      - ANVIL_IP_ADDR=0.0.0.0
    ports:
      - "8545:8545"
    networks:
      - beacon-network
    healthcheck:
      test: ["CMD", "cast", "client", "--rpc-url", "http://localhost:8545"]
      interval: 2s
      timeout: 1s
      retries: 15

  anvil-settlement:
    image: ghcr.io/foundry-rs/foundry:v1.4.3
    container_name: beacon-demo-anvil-settlement
    entrypoint: ["anvil"]
    command: "--port 8546 --chain-id 31338 --timestamp 1754051800 --auto-impersonate --slots-in-an-epoch 1 --accounts 20 --balance 10000 --gas-limit 30000000 --gas-price 10000000"
    environment:
      - ANVIL_IP_ADDR=0.0.0.0
    ports:
      - "8546:8546"
    networks:
      - beacon-network
    healthcheck:
      test: ["CMD", "cast", "client", "--rpc-url", "http://localhost:8546"]
      interval: 2s
      timeout: 1s
      retries: 15

  deployer:
    build:
      context: ..
      dockerfile: network-scripts/deployer.Dockerfile
    image: beacon-provider-deployer
    container_name: beacon-provider-deployer
    user: "1000:1000"
    volumes:
      - ../:/workspace
      - $SUPERSUM_REPO_PATH:/super-sum
      - ./deploy-data:/deploy-data
      - ./cache:/super-sum/cache
      - ./broadcast:/super-sum/broadcast
      - ./out:/super-sum/out
      - ./my-relay-deploy.toml:/super-sum/temp-network/my-relay-deploy.toml
      - ./deploy-data:/super-sum/temp-network/deploy-data
    working_dir: /workspace
    command: ./network-scripts/deploy.sh
    depends_on:
      anvil:
        condition: service_healthy
      anvil-settlement:
        condition: service_healthy
    environment:
      - DEPLOY_DATA_DIR=/deploy-data
      - ANVIL_RPC_URL=http://anvil:8545
      - SETTLEMENT_RPC_URL=http://anvil-settlement:8546
      - BEACON_VP_CHAIN_ID=$DEMO_BEACON_VP_CHAIN_ID
      - KEYREG_ADDR=$DEMO_KEYREG_ADDRESS
      - BEACON_VP_PROVIDER_ADDRESS=$DEMO_BEACON_VP_PROVIDER_ADDRESS
      - OPERATOR_COUNT=$operators
      - NUM_AGGREGATORS=$aggregators
      - NUM_COMMITTERS=$committers
      - SUPERSUM_REPO=/super-sum
    networks:
      - beacon-network

  genesis-generator:
    image: symbioticfi/relay:$RELAY_IMAGE_TAG
    container_name: beacon-provider-genesis-generator
    volumes:
      - ../:/workspace
      - ./deploy-data:/deploy-data
    working_dir: /workspace
    command: ./network-scripts/genesis-generator.sh
    depends_on:
      deployer:
        condition: service_completed_successfully
    environment:
      - DEPLOY_DATA_DIR=/deploy-data
    networks:
      - beacon-network

  network-validator:
    image: alpine:3.21
    container_name: beacon-provider-network-validator
    command: >
      sh -c 'until [ -f /deploy-data/deployment-complete.marker ] && [ -f /deploy-data/genesis-complete.marker ]; do sleep 2; done; echo ready'
    volumes:
      - ./deploy-data:/deploy-data
    depends_on:
      genesis-generator:
        condition: service_completed_successfully
    networks:
      - beacon-network

  beacon-vp-provider:
    build:
      context: ..
      dockerfile: Dockerfile
    image: beacon-vp-provider-local
    container_name: beacon-vp-provider
    entrypoint: ["/bin/sh", "/workspace/network-scripts/provider-start.sh"]
    volumes:
      - ../:/workspace
      - ./deploy-data:/deploy-data
    depends_on:
      deployer:
        condition: service_completed_successfully
    ports:
      - "50051:50051"
    environment:
      - DEPLOY_DATA_DIR=/deploy-data
      - BEACON_NODE_URL=$DEMO_BEACON_NODE_URL
      - ETH_RPC_URL=http://anvil:8545
      - KEY_TAG=$DEMO_KEY_TAG
      - CHAIN_ID=31337
      - LISTEN=:50051
      - PROVIDER_MOCK=true
    networks:
      - beacon-network

EOFYAML

  local relay_start_port=8081
  local committer_count=0
  local aggregator_count=0

  for i in $(seq 1 "$operators"); do
    local role_name="signer"
    if [ "$committer_count" -lt "$committers" ]; then
      role_name="committer"
      committer_count=$((committer_count + 1))
    elif [ "$aggregator_count" -lt "$aggregators" ]; then
      role_name="aggregator"
      aggregator_count=$((aggregator_count + 1))
    fi

    local key_idx=$((i - 1))
    local symb_key_dec=$((BASE_PRIVATE_KEY + key_idx))
    local beacon_key_dec=$((BASE_PRIVATE_KEY + key_idx + 20000))
    local symb_pk
    local beacon_pk
    symb_pk=$(printf "0x%064x" "$symb_key_dec")
    beacon_pk=$(printf "0x%064x" "$beacon_key_dec")

    local port=$((relay_start_port + i - 1))
    local storage_dir="data-$(printf "%02d" "$i")"

    cat >> "$network_dir/docker-compose.yml" << EOFYAML

  relay-sidecar-$i:
    image: symbioticfi/relay:$RELAY_IMAGE_TAG
    container_name: beacon-relay-$i
    command:
      - /workspace/network-scripts/sidecar-start.sh
      - symb/0/15/$symb_pk,symb/2/2/$beacon_pk,symb/1/0/$symb_pk,evm/1/31337/$symb_pk,evm/1/31338/$symb_pk,p2p/1/1/${symb_pk#0x}
      - /app/$storage_dir
    ports:
      - "$port:8080"
    volumes:
      - ../:/workspace
      - ./$storage_dir:/app/$storage_dir
      - ./deploy-data:/deploy-data
    depends_on:
      network-validator:
        condition: service_completed_successfully
    networks:
      - beacon-network
    restart: unless-stopped
    labels:
      - "demo.role=$role_name"
    environment:
      - DEPLOY_DATA_DIR=/deploy-data
EOFYAML
  done

  cat >> "$network_dir/docker-compose.yml" << EOFYAML

networks:
  beacon-network:
    driver: bridge
EOFYAML
}

main() {
  print_header "Beacon Provider Demo Network Generator"
  check_prereqs
  get_user_input
  print_status "Generating temp-network/docker-compose.yml ..."
  generate_docker_compose "$operators" "$committers" "$aggregators"

  print_header "Setup Complete"
  print_status "Generated: temp-network/docker-compose.yml"
  print_status "Start: docker compose --project-directory temp-network up -d"
  print_status "Status: docker compose --project-directory temp-network ps"
  print_status "Logs: docker compose --project-directory temp-network logs -f"
  print_warning "This is infra-only demo: no SumTask/sum-node services are generated."
}

main "$@"
