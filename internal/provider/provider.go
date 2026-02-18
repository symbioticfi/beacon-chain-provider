package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	beaconapi "github.com/symbioticfi/beacon-chain-provider/internal/beacon"
	"github.com/symbioticfi/beacon-chain-provider/internal/types"
)

const (
	secondsPerEpoch    = uint64(384)
	slotsPerEpoch      = uint64(32)
	idChunkSize        = 1000
	defaultMockMapPath = "demo/hoodi_validators_50.json"
)

var (
	ErrMalformedRequest         = errors.New("malformed request")
	ErrTimestampBeforeGenesis   = errors.New("timestamp before genesis")
	ErrEpochNotFinalized        = errors.New("epoch not finalized")
	ErrDuplicatePubkeyOwnership = errors.New("duplicate pubkey ownership")
	ErrMockMapInsufficientKeys  = errors.New("insufficient hoodi keys for mock map")
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

type notFoundError interface {
	NotFound() bool
}

type Provider struct {
	beacon      BeaconClient
	keyRegistry KeyRegistryClient
	logger      *slog.Logger
	mockMapPath string

	mockMapOnce    sync.Once
	mockMapPubkeys [][]byte
	mockMapErr     error
}

type EvaluationMeta struct {
	Epoch            uint64
	Slot             uint64
	MatchedValidator int
	OperatorCount    int
}

type Option func(*Provider)

func WithMockMap(path string) Option {
	return func(p *Provider) {
		if path == "" {
			p.mockMapPath = defaultMockMapPath
			return
		}
		p.mockMapPath = path
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(p *Provider) {
		if logger != nil {
			p.logger = logger
		}
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
	if p.mockMapPath != "" {
		ops, err = p.applyMockMap(ops)
		if err != nil {
			p.logger.WarnContext(ctx, "failed applying mock map", "path", p.mockMapPath, "error", err)
			return nil, EvaluationMeta{}, err
		}
		p.logger.DebugContext(ctx, "applied mock map", "path", p.mockMapPath)
	}

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
		p.logger.InfoContext(ctx, "no operator keys matched configured tag", "timestamp", timestamp, "epoch", epoch, "slot", slot)
		return []types.OperatorVotingPower{}, EvaluationMeta{Epoch: epoch, Slot: slot, MatchedValidator: 0, OperatorCount: 0}, nil
	}

	ids := make([]string, 0, len(pubkeyToOperator))
	for normalized := range pubkeyToOperator {
		ids = append(ids, "0x"+normalized)
	}
	sort.Strings(ids)

	validators, stateSlot, err := p.fetchValidatorsAtOrBeforeSlot(ctx, slot, ids)
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

type hoodiValidatorRecord struct {
	Index  uint64 `json:"index"`
	Pubkey string `json:"pubkey"`
}

func (p *Provider) applyMockMap(ops []types.OperatorWithKeys) ([]types.OperatorWithKeys, error) {
	pubkeys, err := p.loadMockMapPubkeys()
	if err != nil {
		return nil, fmt.Errorf("load mock map keys: %w", err)
	}

	operatorSet := make(map[common.Address]struct{})
	for _, op := range ops {
		operatorSet[op.Operator] = struct{}{}
	}
	if len(operatorSet) > len(pubkeys) {
		return nil, fmt.Errorf("%w: operators=%d keys=%d", ErrMockMapInsufficientKeys, len(operatorSet), len(pubkeys))
	}

	operators := make([]common.Address, 0, len(operatorSet))
	for op := range operatorSet {
		operators = append(operators, op)
	}
	sort.Slice(operators, func(i, j int) bool { return operators[i].Hex() < operators[j].Hex() })

	used := make([]bool, len(pubkeys))
	assignments := make(map[common.Address][]byte, len(operators))
	for _, op := range operators {
		hash := crypto.Keccak256Hash(op.Bytes())
		start := int(hash.Big().Uint64() % uint64(len(pubkeys)))
		idx := start
		for {
			if !used[idx] {
				used[idx] = true
				assignments[op] = pubkeys[idx]
				break
			}
			idx = (idx + 1) % len(pubkeys)
			if idx == start {
				return nil, fmt.Errorf("%w: exhausted key slots", ErrMockMapInsufficientKeys)
			}
		}
	}

	mapped := make([]types.OperatorWithKeys, 0, len(ops))
	for _, op := range ops {
		assigned := assignments[op.Operator]
		if len(op.Keys) == 0 {
			mapped = append(mapped, types.OperatorWithKeys{
				Operator: op.Operator,
				Keys: []types.Key{{
					Payload: append([]byte(nil), assigned...),
				}},
			})
			continue
		}
		keys := make([]types.Key, 0, len(op.Keys))
		for _, key := range op.Keys {
			keys = append(keys, types.Key{
				Tag:     key.Tag,
				Payload: append([]byte(nil), assigned...),
			})
		}
		mapped = append(mapped, types.OperatorWithKeys{Operator: op.Operator, Keys: keys})
	}
	return mapped, nil
}

func (p *Provider) loadMockMapPubkeys() ([][]byte, error) {
	p.mockMapOnce.Do(func() {
		raw, err := os.ReadFile(p.mockMapPath)
		if err != nil {
			p.mockMapErr = err
			return
		}

		var rows []hoodiValidatorRecord
		if err := json.Unmarshal(raw, &rows); err != nil {
			p.mockMapErr = err
			return
		}
		out := make([][]byte, 0, len(rows))
		for _, row := range rows {
			normalized, err := NormalizeBLSPubkeyHex(row.Pubkey)
			if err != nil {
				p.mockMapErr = err
				return
			}
			pk, err := hex.DecodeString(normalized)
			if err != nil {
				p.mockMapErr = err
				return
			}
			out = append(out, pk)
		}
		if len(out) == 0 {
			p.mockMapErr = errors.New("empty hoodi validator key set")
			return
		}
		p.mockMapPubkeys = out
	})

	return p.mockMapPubkeys, p.mockMapErr
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

func (p *Provider) fetchValidatorsForState(ctx context.Context, stateID string, ids []string) ([]types.BeaconValidator, error) {
	validators := make([]types.BeaconValidator, 0, len(ids))
	for _, chunk := range chunkStrings(ids, idChunkSize) {
		rows, err := p.beacon.GetValidatorsByState(ctx, stateID, nil, chunk)
		if err != nil {
			p.logger.WarnContext(ctx, "failed to fetch validators by state", "state_id", stateID, "chunk_size", len(chunk), "error", err)
			return nil, err
		}
		validators = append(validators, rows...)
	}
	return validators, nil
}

func (p *Provider) fetchValidatorsAtOrBeforeSlot(ctx context.Context, requestedSlot uint64, ids []string) ([]types.BeaconValidator, uint64, error) {
	for candidate := requestedSlot; ; candidate-- {
		stateID := strconv.FormatUint(candidate, 10)
		validators, err := p.fetchValidatorsForState(ctx, stateID, ids)
		if err == nil {
			return validators, candidate, nil
		}
		if !isNotFound(err) {
			return nil, 0, err
		}
		p.logger.WarnContext(ctx, "validator state missing, decrementing slot", "state_id", stateID, "requested_slot", requestedSlot)
		if candidate == 0 {
			return nil, 0, fmt.Errorf("%w: no validator state at or below slot=%d", ErrEpochNotFinalized, requestedSlot)
		}
	}
}

func isNotFound(err error) bool {
	if beaconapi.IsNotFoundError(err) {
		return true
	}
	var nf notFoundError
	return errors.As(err, &nf) && nf.NotFound()
}
