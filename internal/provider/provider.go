package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/symbioticfi/beacon-chain-provider/internal/types"
)

const (
	secondsPerEpoch = uint64(384)
	slotsPerEpoch   = uint64(32)
	idChunkSize     = 1000
)

var (
	ErrMalformedRequest         = errors.New("malformed request")
	ErrTimestampBeforeGenesis   = errors.New("timestamp before genesis")
	ErrEpochNotFinalized        = errors.New("epoch not finalized")
	ErrDuplicatePubkeyOwnership = errors.New("duplicate pubkey ownership")
	ErrUpstream                 = errors.New("upstream failure")
)

var activeStatuses = []string{"active_ongoing", "active_exiting", "active_slashed"}

type BeaconClient interface {
	GetGenesis(ctx context.Context) (genesisTime uint64, err error)
	GetFinalizedSlot(ctx context.Context) (slot uint64, err error)
	StateExists(ctx context.Context, stateID string) (bool, error)
	GetValidatorsByState(ctx context.Context, stateID string, statuses []string, ids []string) ([]types.BeaconValidator, error)
}

type KeyRegistryClient interface {
	GetKeysAt(ctx context.Context, timestamp uint64) ([]types.OperatorWithKeys, error)
}

type Provider struct {
	beacon      BeaconClient
	keyRegistry KeyRegistryClient
}

type EvaluationMeta struct {
	Epoch            uint64
	Slot             uint64
	MatchedValidator int
	OperatorCount    int
}

func New(beacon BeaconClient, keyRegistry KeyRegistryClient) *Provider {
	return &Provider{beacon: beacon, keyRegistry: keyRegistry}
}

func (p *Provider) GetVotingPowersAt(ctx context.Context, timestamp uint64) ([]types.OperatorVotingPower, EvaluationMeta, error) {
	if timestamp == 0 {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: timestamp must be non-zero", ErrMalformedRequest)
	}

	genesis, err := p.beacon.GetGenesis(ctx)
	if err != nil {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon genesis: %w", ErrUpstream, err)
	}
	if timestamp < genesis {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: ts=%d genesis=%d", ErrTimestampBeforeGenesis, timestamp, genesis)
	}

	epoch := (timestamp - genesis) / secondsPerEpoch
	if epoch > math.MaxUint64/slotsPerEpoch {
		return nil, EvaluationMeta{}, fmt.Errorf("epoch overflow: %d", epoch)
	}
	slot := epoch * slotsPerEpoch

	finalizedSlot, err := p.beacon.GetFinalizedSlot(ctx)
	if err != nil {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon finalized slot: %w", ErrUpstream, err)
	}
	if slot > finalizedSlot {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: slot=%d finalized=%d", ErrEpochNotFinalized, slot, finalizedSlot)
	}

	ops, err := p.keyRegistry.GetKeysAt(ctx, timestamp)
	if err != nil {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: key registry: %w", ErrUpstream, err)
	}

	pubkeyToOperator := make(map[string]common.Address)
	for _, op := range ops {
		for _, k := range op.Keys {
			normalized, err := NormalizeBLS12381KeyPayload(k.Payload)
			if err != nil {
				return nil, EvaluationMeta{}, fmt.Errorf("normalize key payload: %w", err)
			}
			if existing, ok := pubkeyToOperator[normalized]; ok && existing != op.Operator {
				return nil, EvaluationMeta{}, fmt.Errorf("%w: pubkey=%s operators=%s,%s", ErrDuplicatePubkeyOwnership, normalized, existing.Hex(), op.Operator.Hex())
			}
			pubkeyToOperator[normalized] = op.Operator
		}
	}

	if len(pubkeyToOperator) == 0 {
		return []types.OperatorVotingPower{}, EvaluationMeta{Epoch: epoch, Slot: slot, MatchedValidator: 0, OperatorCount: 0}, nil
	}

	ids := make([]string, 0, len(pubkeyToOperator))
	for normalized := range pubkeyToOperator {
		ids = append(ids, "0x"+normalized)
	}
	sort.Strings(ids)

	stateSlot, err := p.resolveStateSlot(ctx, slot)
	if err != nil {
		if errors.Is(err, ErrEpochNotFinalized) {
			return nil, EvaluationMeta{}, err
		}
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon state resolve: %w", ErrUpstream, err)
	}
	stateID := strconv.FormatUint(stateSlot, 10)

	validators := make([]types.BeaconValidator, 0, len(ids))
	for _, chunk := range chunkStrings(ids, idChunkSize) {
		rows, err := p.beacon.GetValidatorsByState(ctx, stateID, activeStatuses, chunk)
		if err != nil {
			return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon validators: %w", ErrUpstream, err)
		}
		validators = append(validators, rows...)
	}

	sums := make(map[common.Address]uint64)
	matched := 0
	for _, v := range validators {
		normalized, err := NormalizeBLSPubkeyHex(v.Pubkey)
		if err != nil {
			return nil, EvaluationMeta{}, fmt.Errorf("normalize validator pubkey: %w", err)
		}
		op, ok := pubkeyToOperator[normalized]
		if !ok {
			continue
		}
		if math.MaxUint64-sums[op] < v.EffectiveBalance {
			return nil, EvaluationMeta{}, fmt.Errorf("voting power overflow for operator %s", op.Hex())
		}
		sums[op] += v.EffectiveBalance
		matched++
	}

	out := make([]types.OperatorVotingPower, 0, len(sums))
	for op, power := range sums {
		if power == 0 {
			continue
		}
		out = append(out, types.OperatorVotingPower{Operator: op, VotingPowerGwei: power})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Operator.Hex() < out[j].Operator.Hex()
	})

	return out, EvaluationMeta{Epoch: epoch, Slot: stateSlot, MatchedValidator: matched, OperatorCount: len(out)}, nil
}

func chunkStrings(items []string, size int) [][]string {
	if size <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(items)+size-1)/size)
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func (p *Provider) resolveStateSlot(ctx context.Context, startSlot uint64) (uint64, error) {
	for candidate := startSlot; ; candidate-- {
		exists, err := p.beacon.StateExists(ctx, strconv.FormatUint(candidate, 10))
		if err != nil {
			return 0, err
		}
		if exists {
			return candidate, nil
		}
		if candidate == 0 {
			return 0, fmt.Errorf("%w: no available state at or below slot=%d", ErrEpochNotFinalized, startSlot)
		}
	}
}
