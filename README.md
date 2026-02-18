# Beacon Voting Power Provider

Standalone gRPC provider that implements `VotingPowerProviderService.GetVotingPowersAt` for beacon-chain-derived voting power.

## What it does

- Maps beacon validator BLS12-381 pubkeys to operators using Symbiotic Key Registry `getKeysAt(timestamp)`.
- Uses beacon epoch-start state (`slot = epoch * 32`, with `epoch = (timestamp - genesis_time) / 384`).
- Sums `effective_balance` (Gwei) for active validators.
- Returns deterministic output sorted by operator address ascending.

## Configuration

Example config: `example.config.yaml`

Fields:

- `grpc.listen`
- `beacon.node_url`
- `ethereum.rpc_url`
- `key_registry.address`
- `key_registry.chain_id`
- `key_registry.key_tag`
- `timeouts.request`
- `log.level`
- `provider.mock` (optional: deterministic operator->hoodi validator remap using `demo/hoodi_validators_50.json`)

## Run

```bash
go run ./cmd/beacon-vp --config example.config.yaml
```

## Build

```bash
go build ./cmd/beacon-vp
```

## Test

```bash
go test ./...
```

## API

Proto is currently in `api/proto/votingpower/v1/votingpower.proto` and generated stubs are in `api/proto/votingpower/v1`.
This provider-local proto is temporary; in future it will live in the relay repository and this provider will import relay-published stubs directly.

## Infra Demo Network (Super-Sum Style)

This repo includes an infrastructure-only local demo network generator similar to `symbiotic-super-sum` flow.

### Quick Start

1. Generate network config (interactive, max 50 operators):

```bash
./generate_network.sh
```

2. Start network:

```bash
docker compose --project-directory temp-network up -d
```

3. Check status:

```bash
docker compose --project-directory temp-network ps
```

### Services

Core services:

- `anvil` (port 8545)
- `anvil-settlement` (port 8546)
- `deployer` (uses existing KeyRegistry from env/artifacts, registers it in ValSetDriver if available, and configures beacon voting power provider chain id `4000000000`)
- `genesis-generator` (infra marker stage)
- `network-validator` (readiness gate)
- `beacon-vp-provider` (port 50051, built locally from this repo)

Relay sidecars:

- `relay-sidecar-1` (port 8081)
- `relay-sidecar-2` (port 8082)
- ... up to selected operator count

### Logs / Stop / Cleanup

```bash
# Logs

docker compose --project-directory temp-network logs -f

# Stop

docker compose --project-directory temp-network down

# Cleanup

docker compose --project-directory temp-network down -v
rm -rf temp-network
```

### Notes

- Demo deployment registers keys/voting-power providers in ValSetDriver when driver is deployed at configured address.
- Demo deployment persists beacon voting power provider chain id `4000000000` in deployment artifacts.
- Hoodi beacon endpoint is used by default for the provider in demo mode.
- Relay services prefer local binaries (`/workspace/bin/relay_sidecar`, `/workspace/bin/relay_utils`) when present, otherwise use image binaries.

## Error semantics

- `InvalidArgument`: malformed request, timestamp before genesis.
- `FailedPrecondition`: epoch not finalized, duplicate key ownership, insufficient hoodi keys for `provider.mock`.
- `Unavailable`: upstream beacon/ethereum/key-registry failures.
- `DeadlineExceeded`: upstream/request timeouts.
- `Internal`: unexpected parsing/overflow failures.
