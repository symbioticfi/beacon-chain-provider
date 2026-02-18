# Demo Network Design

## Goal
Provide a super-sum-like local Docker demo flow for beacon provider in this repository, with interactive topology selection and default wiring to the existing relay KeyRegistry.

## Scope
- Interactive `generate_network.sh` with max operator cap of `50`.
- Core services: anvils, deployer, genesis marker stage, network validator gate, local beacon provider.
- Relay sidecars generated per selected operator count.
- Demo uses existing relay-style `KeyRegistry` address flow from env/artifacts.

## Constraints
- Driver/genesis in this repo is infra-marker based for bootstrap.

## Deliverables
- `generate_network.sh`
- `network-scripts/*`
- `Dockerfile`
- updated root README quickstart section
