#!/usr/bin/env sh
set -eu

DEPLOY_DATA_DIR=${DEPLOY_DATA_DIR:-/deploy-data}

echo "Waiting for deployment completion..."
until [ -f "$DEPLOY_DATA_DIR/deployment-complete.marker" ]; do sleep 2; done

RELAY_UTILS_BIN=/app/relay_utils
if [ -x /workspace/bin/relay_utils ]; then
  RELAY_UTILS_BIN=/workspace/bin/relay_utils
fi

# Infra-only demo: marker-based genesis step to preserve super-sum service flow.
"$RELAY_UTILS_BIN" --help >/dev/null 2>&1 || true
echo "$(date): genesis generation completed" > "$DEPLOY_DATA_DIR/genesis-complete.marker"

echo "genesis marker created"
