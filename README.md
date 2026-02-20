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

## Demo Network (super-sum submodule)

This repo includes `super-sum` as a git submodule.

Generate a demo network and patch it to register an external voting power provider **after genesis commit**:

```bash
./generate_super_sum_network.sh
```

Then start it:

```bash
docker compose --project-directory super-sum/temp-network up -d --build
```

Optional env vars for the wrapper script:

- `EXTERNAL_VP_CHAIN_ID` (default `4000000000`)
- `EXTERNAL_VP_ADDRESS` (default `0x0000000000000000000000000000000000000001`)
- `DRIVER_ADDRESS` (default `0x43C27243F96591892976FFf886511807B65a33d5`)
- `DEPLOYER_PRIVATE_KEY`

## Error semantics

- `InvalidArgument`: malformed request, timestamp before genesis.
- `FailedPrecondition`: epoch not finalized, duplicate key ownership.
- `Unavailable`: upstream beacon/ethereum/key-registry failures.
- `DeadlineExceeded`: upstream/request timeouts.
- `Internal`: unexpected parsing/overflow failures.
