#!/usr/bin/env bash

set -euo pipefail

readonly REPO_ROOT="$(git rev-parse --show-toplevel)"
readonly LOCAL_GO_PACKAGE="github.com/symbioticfi/beacon-chain-provider/api/gen/votingpower/v1;votingpowerv1"
readonly BUF_VERSION="v1.65.0"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

cd "$REPO_ROOT"

go mod download github.com/symbioticfi/relay
readonly RELAY_DIR="$(go list -m -f '{{.Dir}}' github.com/symbioticfi/relay)"
readonly SOURCE_PROTO="$RELAY_DIR/votingpower/proto/v1/votingpower.proto"
readonly STAGED_PROTO="$tmpdir/v1/votingpower.proto"
readonly TEMP_BUF_CONFIG="$tmpdir/buf.yaml"

if [[ ! -f "$SOURCE_PROTO" ]]; then
  echo "missing relay votingpower proto: $SOURCE_PROTO" >&2
  exit 1
fi

mkdir -p "$(dirname "$STAGED_PROTO")"
cp "$SOURCE_PROTO" "$STAGED_PROTO"

perl -0pi -e "s|option go_package = \".*\";|option go_package = \"$LOCAL_GO_PACKAGE\";|" "$STAGED_PROTO"

if ! grep -qxF "option go_package = \"$LOCAL_GO_PACKAGE\";" "$STAGED_PROTO"; then
  echo "failed to rewrite go_package in staged proto" >&2
  exit 1
fi

cat > "$TEMP_BUF_CONFIG" <<'EOF'
version: v2
modules:
  - path: .
EOF

rm -rf "$REPO_ROOT/api/gen/votingpower"
mkdir -p "$REPO_ROOT/api/gen/votingpower"

GOWORK=off go run "github.com/bufbuild/buf/cmd/buf@$BUF_VERSION" generate "$tmpdir" --template "$REPO_ROOT/buf.votingpower.gen.yaml"
