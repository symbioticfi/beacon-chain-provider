# KeyRegistry-ID-Filtered Beacon Fetch Design

Date: 2026-02-16
Status: Approved

## Goal
Replace the current full beacon validator-state fetch with targeted validator fetches using pubkeys returned by `KeyRegistry.getKeysAt(timestamp)`, to avoid downloading the full validator set.

## Scope
- Apply this architecture as the primary path (not an optional extension).
- Keep existing determinism and error semantics.
- Keep status filters in beacon queries (`active_ongoing`, `active_exiting`, `active_slashed`).

## Chosen Approach
Use beacon validator queries filtered by key-registry pubkeys (`id` query params), chunked with a fixed size of **1000 IDs/request**, then merge and aggregate.

### Alternatives considered
1. Fixed chunking with `id` filters (chosen)
- Low complexity, deterministic, avoids full-state payloads.

2. Adaptive chunking
- Better resilience to upstream request limits, but higher complexity.

3. Full-state fetch + local filtering
- Simplest call pattern, but too heavy and timeout-prone.

## Architecture
- Provider remains responsible for timestamp validation, epoch/slot computation, and finalized-slot guardrail.
- Key registry flow remains the same for `GetKeysAt(timestamp)` and duplicate pubkey ownership validation.
- Beacon fetch logic switches from "all validators by state" to "validators by state + pubkey IDs + active statuses".

### Interface direction
- Replace existing broad validator-fetch usage with ID-filtered validator fetch usage.
- Status filters remain explicit in request query params for correctness and efficiency.

## Data Flow
1. Receive `timestamp`.
2. Fetch beacon genesis; compute epoch and epoch-start slot.
3. Ensure `slot <= finalizedSlot`; otherwise `FailedPrecondition`.
4. Fetch keys from key registry at timestamp.
5. Build normalized `pubkey -> operator` map and detect duplicate ownership.
6. Build unique normalized pubkey list.
7. Chunk pubkeys into groups of 1000.
8. For each chunk, request beacon validators at `/eth/v1/beacon/states/{slot}/validators` with repeated:
- `status=active_ongoing`
- `status=active_exiting`
- `status=active_slashed`
- `id=<pubkey>` (for each chunk item)
9. Merge returned validators and aggregate `effective_balance` by mapped operator.
10. Sort operator addresses ascending and return.

## Why Status Filters Stay
- Correctness: voting power includes only active validator statuses.
- Efficiency: server-side filtering reduces payload.
- Determinism: explicit status constraints avoid implicit behavior changes.

## Error Handling
- Keep current gRPC code mappings.
- Any beacon chunk call failure is treated as upstream failure and mapped via existing logic (`Unavailable`/`DeadlineExceeded` as applicable).
- Empty key set should short-circuit to an empty response without beacon validator calls.

## Determinism
Unchanged guarantees:
- Epoch-start slot state source.
- Finalized EL block for key registry call.
- Configured key tag filtering.
- Sorted response by operator address ascending.

## Testing Plan
1. Provider chunking behavior
- `<=1000` IDs => one beacon call.
- `>1000` IDs => multiple beacon calls and merged result.

2. Provider correctness
- Aggregation remains correct across chunk boundaries.
- Duplicate IDs do not double-count.
- Empty key set returns empty output and no beacon calls.

3. Beacon client request-shape tests
- Repeated `id` params encoded correctly.
- Repeated `status` params encoded correctly.

4. Regression checks
- Existing epoch/finality/duplicate-ownership error semantics remain unchanged.

## Non-Goals
- Caching.
- Adaptive chunk backoff/retry policy.
- API semantic changes outside this fetch strategy.
