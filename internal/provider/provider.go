package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	beaconapi "github.com/symbioticfi/beacon-chain-provider/internal/beacon"
	"github.com/symbioticfi/beacon-chain-provider/internal/types"
)

const (
	secondsPerEpoch    = uint64(384)
	slotsPerEpoch      = uint64(32)
	idChunkSize        = 1000
	maxStateLookback   = uint64(4)
)

var (
	ErrMalformedRequest         = errors.New("malformed request")
	ErrTimestampBeforeGenesis   = errors.New("timestamp before genesis")
	ErrEpochNotFinalized        = errors.New("epoch not finalized")
	ErrDuplicatePubkeyOwnership = errors.New("duplicate pubkey ownership")
	ErrUpstream                 = errors.New("upstream failure")
)

type BeaconClient interface {
	GetGenesis(ctx context.Context) (genesisTime uint64, err error)
	GetFinalizedSlot(ctx context.Context) (slot uint64, err error)
	GetValidatorsByState(ctx context.Context, stateID string, statuses []string, ids []string) ([]types.BeaconValidator, error)
}

type KeyRegistryClient interface {
	GetKeysAt(ctx context.Context, timestamp uint64) ([]types.OperatorWithKeys, error)
}

type Provider struct {
	beacon      BeaconClient
	keyRegistry KeyRegistryClient
	logger      *slog.Logger
	mock        bool
}

type EvaluationMeta struct {
	Epoch            uint64
	Slot             uint64
	MatchedValidator int
	OperatorCount    int
}

type Option func(*Provider)

func WithLogger(logger *slog.Logger) Option {
	return func(p *Provider) {
		if logger != nil {
			p.logger = logger
		}
	}
}

func WithMock(enabled bool) Option {
	return func(p *Provider) {
		p.mock = enabled
	}
}

func New(beacon BeaconClient, keyRegistry KeyRegistryClient, opts ...Option) *Provider {
	p := &Provider{
		beacon:      beacon,
		keyRegistry: keyRegistry,
		logger:      slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

func (p *Provider) GetVotingPowersAt(ctx context.Context, timestamp uint64) ([]types.OperatorVotingPower, EvaluationMeta, error) {
	p.logger.DebugContext(ctx, "get voting powers request", "timestamp", timestamp)

	if timestamp == 0 {
		p.logger.WarnContext(ctx, "invalid timestamp", "timestamp", timestamp)
		return nil, EvaluationMeta{}, fmt.Errorf("%w: timestamp must be non-zero", ErrMalformedRequest)
	}

	genesis, err := p.beacon.GetGenesis(ctx)
	if err != nil {
		p.logger.WarnContext(ctx, "failed to fetch beacon genesis", "error", err)
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon genesis: %w", ErrUpstream, err)
	}
	if timestamp < genesis {
		p.logger.WarnContext(ctx, "timestamp before genesis", "timestamp", timestamp, "genesis", genesis)
		return nil, EvaluationMeta{}, fmt.Errorf("%w: ts=%d genesis=%d", ErrTimestampBeforeGenesis, timestamp, genesis)
	}

	epoch := (timestamp - genesis) / secondsPerEpoch
	if epoch > math.MaxUint64/slotsPerEpoch {
		p.logger.ErrorContext(ctx, "epoch overflow", "epoch", epoch)
		return nil, EvaluationMeta{}, fmt.Errorf("epoch overflow: %d", epoch)
	}
	slot := epoch * slotsPerEpoch

	finalizedSlot, err := p.beacon.GetFinalizedSlot(ctx)
	if err != nil {
		p.logger.WarnContext(ctx, "failed to fetch finalized slot", "error", err)
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon finalized slot: %w", ErrUpstream, err)
	}
	if slot > finalizedSlot {
		p.logger.WarnContext(ctx, "requested epoch not finalized", "slot", slot, "finalized_slot", finalizedSlot)
		return nil, EvaluationMeta{}, fmt.Errorf("%w: slot=%d finalized=%d", ErrEpochNotFinalized, slot, finalizedSlot)
	}
	p.logger.DebugContext(ctx, "resolved epoch and slots", "epoch", epoch, "slot", slot, "finalized_slot", finalizedSlot)

	ops, err := p.keyRegistry.GetKeysAt(ctx, timestamp)
	if err != nil {
		p.logger.WarnContext(ctx, "failed to fetch key registry keys", "error", err)
		return nil, EvaluationMeta{}, fmt.Errorf("%w: key registry: %w", ErrUpstream, err)
	}
	p.logger.DebugContext(ctx, "loaded operator keys", "operator_rows", len(ops))

	pubkeyToOperator := make(map[string]common.Address)
	for _, op := range ops {
		for _, k := range op.Keys {
			normalized, err := NormalizeBLS12381KeyPayload(k.Payload)
			if err != nil {
				p.logger.WarnContext(ctx, "failed to normalize key payload", "error", err)
				return nil, EvaluationMeta{}, fmt.Errorf("normalize key payload: %w", err)
			}
			if existing, ok := pubkeyToOperator[normalized]; ok && existing != op.Operator {
				p.logger.WarnContext(ctx, "duplicate pubkey ownership detected", "pubkey", normalized, "operator_a", existing.Hex(), "operator_b", op.Operator.Hex())
				return nil, EvaluationMeta{}, fmt.Errorf("%w: pubkey=%s operators=%s,%s", ErrDuplicatePubkeyOwnership, normalized, existing.Hex(), op.Operator.Hex())
			}
			pubkeyToOperator[normalized] = op.Operator
		}
	}
	p.logger.DebugContext(ctx, "built pubkey index", "pubkey_count", len(pubkeyToOperator))

	if len(pubkeyToOperator) == 0 {
		if p.mock {
			p.logger.InfoContext(ctx, "mock mode enabled, deriving voting powers by validator index", "operator_rows", len(ops))
			return p.computeMockVotingPowers(ctx, epoch, slot, finalizedSlot, ops)
		}
		p.logger.InfoContext(ctx, "no operator keys matched configured tag", "timestamp", timestamp, "epoch", epoch, "slot", slot)
		return []types.OperatorVotingPower{}, EvaluationMeta{Epoch: epoch, Slot: slot, MatchedValidator: 0, OperatorCount: 0}, nil
	}

	ids := make([]string, 0, len(pubkeyToOperator))
	for normalized := range pubkeyToOperator {
		ids = append(ids, "0x"+normalized)
	}
	sort.Strings(ids)

	validators, stateSlot, err := p.fetchValidatorsAtOrBeforeSlot(ctx, slot, finalizedSlot, ids)
	if err != nil {
		if errors.Is(err, ErrEpochNotFinalized) {
			return nil, EvaluationMeta{}, err
		}
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon validators: %w", ErrUpstream, err)
	}
	p.logger.DebugContext(ctx, "fetched validators", "state_id", strconv.FormatUint(stateSlot, 10), "validator_rows", len(validators))

	sums := make(map[common.Address]uint64)
	matched := 0
	for _, v := range validators {
		normalized, err := NormalizeBLSPubkeyHex(v.Pubkey)
		if err != nil {
			p.logger.WarnContext(ctx, "failed to normalize validator pubkey", "pubkey", v.Pubkey, "error", err)
			return nil, EvaluationMeta{}, fmt.Errorf("normalize validator pubkey: %w", err)
		}
		op, ok := pubkeyToOperator[normalized]
		if !ok {
			continue
		}
		if math.MaxUint64-sums[op] < v.EffectiveBalance {
			p.logger.ErrorContext(ctx, "voting power overflow", "operator", op.Hex(), "current", sums[op], "delta", v.EffectiveBalance)
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
	p.logger.InfoContext(ctx, "computed voting powers",
		"timestamp", timestamp,
		"epoch", epoch,
		"slot", stateSlot,
		"matched_validators", matched,
		"operator_count", len(out),
	)

	return out, EvaluationMeta{Epoch: epoch, Slot: stateSlot, MatchedValidator: matched, OperatorCount: len(out)}, nil
}

func (p *Provider) computeMockVotingPowers(ctx context.Context, epoch, slot, finalizedSlot uint64, ops []types.OperatorWithKeys) ([]types.OperatorVotingPower, EvaluationMeta, error) {
	operators := sortedOperators(ops)
	if len(operators) == 0 {
		return []types.OperatorVotingPower{}, EvaluationMeta{Epoch: epoch, Slot: slot, MatchedValidator: 0, OperatorCount: 0}, nil
	}

	stateID, stateSlot, err := p.resolveStateIDAtOrBeforeSlot(ctx, slot, finalizedSlot, []string{"0"})
	if err != nil {
		return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon validators: %w", ErrUpstream, err)
	}

	out := make([]types.OperatorVotingPower, 0, len(operators))
	matched := 0
	for i, op := range operators {
		id := strconv.Itoa(i)
		rows, err := p.fetchValidatorsByStateID(ctx, stateID, []string{id})
		if err != nil {
			return nil, EvaluationMeta{}, fmt.Errorf("%w: beacon validators: %w", ErrUpstream, err)
		}
		if len(rows) == 0 {
			continue
		}
		out = append(out, types.OperatorVotingPower{
			Operator:        op,
			VotingPowerGwei: rows[0].EffectiveBalance,
		})
		matched++
	}

	p.logger.InfoContext(ctx, "computed voting powers (mock mode)",
		"epoch", epoch,
		"slot", stateSlot,
		"matched_validators", matched,
		"operator_count", len(out),
	)
	return out, EvaluationMeta{Epoch: epoch, Slot: stateSlot, MatchedValidator: matched, OperatorCount: len(out)}, nil
}

func sortedOperators(ops []types.OperatorWithKeys) []common.Address {
	if len(ops) == 0 {
		return nil
	}
	seen := make(map[common.Address]struct{}, len(ops))
	out := make([]common.Address, 0, len(ops))
	for _, op := range ops {
		if _, ok := seen[op.Operator]; ok {
			continue
		}
		seen[op.Operator] = struct{}{}
		out = append(out, op.Operator)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Hex() < out[j].Hex()
	})
	return out
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

func (p *Provider) fetchValidatorsAtOrBeforeSlot(ctx context.Context, requestedSlot, finalizedSlot uint64, ids []string) ([]types.BeaconValidator, uint64, error) {
	stateID, stateSlot, err := p.resolveStateIDAtOrBeforeSlot(ctx, requestedSlot, finalizedSlot, ids)
	if err != nil {
		return nil, 0, err
	}
	validators, err := p.fetchValidatorsByStateID(ctx, stateID, ids)
	if err != nil {
		return nil, 0, err
	}
	return validators, stateSlot, nil
}

func (p *Provider) resolveStateIDAtOrBeforeSlot(ctx context.Context, requestedSlot, finalizedSlot uint64, probeIDs []string) (string, uint64, error) {
	maxLookback := maxStateLookback
	if requestedSlot < maxLookback {
		maxLookback = requestedSlot
	}

	if len(probeIDs) == 0 {
		probeIDs = []string{"0"}
	} else if len(probeIDs) > 1 {
		probeIDs = probeIDs[:1]
	}

	for lookedBack := uint64(0); lookedBack <= maxLookback; lookedBack++ {
		candidate := requestedSlot - lookedBack
		stateID := strconv.FormatUint(candidate, 10)

		_, err := p.fetchValidatorsByStateID(ctx, stateID, probeIDs)
		if err == nil {
			return stateID, candidate, nil
		}
		if !isNotFound(err) {
			return "", 0, err
		}
		p.logger.WarnContext(ctx, "validator state missing, decrementing slot", "state_id", stateID, "requested_slot", requestedSlot)
	}

	p.logger.WarnContext(
		ctx,
		"falling back to finalized beacon state for validator lookup",
		"requested_slot",
		requestedSlot,
		"finalized_slot",
		finalizedSlot,
	)
	_, err := p.fetchValidatorsByStateID(ctx, "finalized", probeIDs)
	if err != nil {
		return "", 0, err
	}
	return "finalized", finalizedSlot, nil
}

func (p *Provider) fetchValidatorsByStateID(ctx context.Context, stateID string, ids []string) ([]types.BeaconValidator, error) {
	chunks := chunkStrings(ids, idChunkSize)
	validators := make([]types.BeaconValidator, 0, len(ids))
	for _, chunk := range chunks {
		rows, err := p.beacon.GetValidatorsByState(ctx, stateID, nil, chunk)
		if err != nil {
			return nil, err
		}
		validators = append(validators, rows...)
	}
	return validators, nil
}

func isNotFound(err error) bool {
	return beaconapi.IsNotFoundError(err)
}
