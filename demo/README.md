# Demo Artifacts

This folder contains data and tooling for the local infra demo network.

## Files

- `hoodi_validators_50.json`:
  - hardcoded Hoodi validator references (`index`, `pubkey`) collected from the public endpoint.
  - useful as a reference list only.

## Integration Note

Demo uses an existing KeyRegistry address (same style as super-sum relay deployment).
`network-scripts/deploy.sh` reads `KEYREG_ADDR` (or `deployment-completed.json`) and writes it into `deploy-data/keyregistry-address` for provider startup.

## Voting Power Provider Chain Registration

In the generated infra demo network, `network-scripts/deploy.sh` automatically:

1. Uses existing KeyRegistry address from env/artifacts.
2. Tries to register keys provider + voting power provider in ValSetDriver (if deployed at configured driver address).
3. Persists `beacon_voting_power_provider_chain_id=4000000000` in deployment artifacts.

Deployment outputs are written to `temp-network/deploy-data/deployment-completed.json`.
