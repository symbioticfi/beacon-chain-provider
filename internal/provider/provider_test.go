package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"github.com/symbioticfi/beacon-chain-provider/internal/testutil"
	"github.com/symbioticfi/beacon-chain-provider/internal/types"
)

type mockBeacon struct {
	genesis        uint64
	finalizedSlot  uint64
	validatorsByID map[string]types.BeaconValidator
	errGenesis     error
	errFinalized   error
	errValidators  error
	errByState     map[string]error
	stateIDs       []string
	statuses       [][]string
	ids            [][]string
}

func (m *mockBeacon) GetGenesis(context.Context) (uint64, error) { return m.genesis, m.errGenesis }
func (m *mockBeacon) GetFinalizedSlot(context.Context) (uint64, error) {
	return m.finalizedSlot, m.errFinalized
}
func (m *mockBeacon) GetValidatorsByState(_ context.Context, stateID string, statuses []string, ids []string) ([]types.BeaconValidator, error) {
	m.stateIDs = append(m.stateIDs, stateID)
	m.statuses = append(m.statuses, append([]string(nil), statuses...))
	m.ids = append(m.ids, append([]string(nil), ids...))
	if m.errByState != nil {
		if err := m.errByState[stateID]; err != nil {
			return nil, err
		}
	}
	if m.errValidators != nil {
		return nil, m.errValidators
	}
	out := make([]types.BeaconValidator, 0, len(ids))
	for _, id := range ids {
		if v, ok := m.validatorsByID[id]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

type mockKeyRegistry struct {
	ops []types.OperatorWithKeys
	err error
}

func (m *mockKeyRegistry) GetKeysAt(context.Context, uint64) ([]types.OperatorWithKeys, error) {
	return m.ops, m.err
}

func TestGetVotingPowersAt_AggregatesAndSorts(t *testing.T) {
	opA := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	opB := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		validatorsByID: map[string]types.BeaconValidator{
			testutil.PubkeyHex(1): {Pubkey: testutil.PubkeyHex(1), EffectiveBalance: 32_000_000_000},
			testutil.PubkeyHex(2): {Pubkey: testutil.PubkeyHex(2), EffectiveBalance: 31_000_000_000},
			testutil.PubkeyHex(3): {Pubkey: testutil.PubkeyHex(3), EffectiveBalance: 30_000_000_000},
		},
	}
	keyReg := &mockKeyRegistry{ops: []types.OperatorWithKeys{
		{Operator: opB, Keys: []types.Key{{Tag: 0x20, Payload: testutil.PubkeyBytes(1)}, {Tag: 0x20, Payload: testutil.PubkeyBytes(2)}}},
		{Operator: opA, Keys: []types.Key{{Tag: 0x20, Payload: testutil.PubkeyBytes(3)}}},
	}}

	p := New(beacon, keyReg)
	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000+2*384)
	require.NoError(t, err)
	require.Equal(t, uint64(2), meta.Epoch)
	require.Equal(t, uint64(64), meta.Slot)
	require.Equal(t, 3, meta.MatchedValidator)
	require.Equal(t, 2, meta.OperatorCount)
	require.Equal(t, []string{"64", "64"}, beacon.stateIDs)
	require.Nil(t, beacon.statuses[0])

	require.Len(t, got, 2)
	require.Equal(t, opA, got[0].Operator)
	require.Equal(t, uint64(30_000_000_000), got[0].VotingPowerGwei)
	require.Equal(t, opB, got[1].Operator)
	require.Equal(t, uint64(63_000_000_000), got[1].VotingPowerGwei)
}

func TestGetVotingPowersAt_TimestampBeforeGenesis(t *testing.T) {
	p := New(&mockBeacon{genesis: 100}, &mockKeyRegistry{})
	_, _, err := p.GetVotingPowersAt(context.Background(), 99)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrTimestampBeforeGenesis)
}

func TestGetVotingPowersAt_EpochNotFinalized(t *testing.T) {
	p := New(&mockBeacon{genesis: 100, finalizedSlot: 31}, &mockKeyRegistry{})
	_, _, err := p.GetVotingPowersAt(context.Background(), 100+384)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrEpochNotFinalized)
}

func TestGetVotingPowersAt_DuplicatePubkeyOwnership(t *testing.T) {
	pk := testutil.PubkeyBytes(7)
	p := New(
		&mockBeacon{genesis: 1, finalizedSlot: 1000},
		&mockKeyRegistry{ops: []types.OperatorWithKeys{
			{Operator: common.HexToAddress("0x0000000000000000000000000000000000000001"), Keys: []types.Key{{Payload: pk}}},
			{Operator: common.HexToAddress("0x0000000000000000000000000000000000000002"), Keys: []types.Key{{Payload: pk}}},
		}},
	)

	_, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrDuplicatePubkeyOwnership)
}

func TestGetVotingPowersAt_NoMatches(t *testing.T) {
	p := New(
		&mockBeacon{
			genesis:       1,
			finalizedSlot: 1000,
			validatorsByID: map[string]types.BeaconValidator{
				testutil.PubkeyHex(1): {Pubkey: testutil.PubkeyHex(1), EffectiveBalance: 100},
			},
		},
		&mockKeyRegistry{ops: []types.OperatorWithKeys{{Operator: common.HexToAddress("0x0000000000000000000000000000000000000001"), Keys: []types.Key{{Payload: testutil.PubkeyBytes(2)}}}}},
	)
	got, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestGetVotingPowersAt_ChunksBeaconRequestsByID(t *testing.T) {
	const total = 1001
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	keys := make([]types.Key, 0, total)
	validators := make(map[string]types.BeaconValidator, total)
	for i := 0; i < total; i++ {
		pk := testutil.PubkeyBytes(byte(i % 251))
		pk[0] = byte(i % 255)
		pk[1] = byte((i / 255) % 255)
		keys = append(keys, types.Key{Payload: pk})
		id := testutil.PubkeyHexFromBytes(pk)
		validators[id] = types.BeaconValidator{Pubkey: id, EffectiveBalance: 1}
	}

	beacon := &mockBeacon{genesis: 1, finalizedSlot: 10_000, validatorsByID: validators}
	p := New(beacon, &mockKeyRegistry{ops: []types.OperatorWithKeys{{Operator: op, Keys: keys}}})
	got, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(total), got[0].VotingPowerGwei)

	require.Len(t, beacon.ids, 3)
	require.Len(t, beacon.ids[0], 1)
	require.Len(t, beacon.ids[1], 1000)
	require.Len(t, beacon.ids[2], 1)
	require.Equal(t, []string{"0", "0", "0"}, beacon.stateIDs)
	require.Nil(t, beacon.statuses[0])
	require.Nil(t, beacon.statuses[1])
	require.Nil(t, beacon.statuses[2])
}

func TestGetVotingPowersAt_EmptyKeySetSkipsBeaconFetch(t *testing.T) {
	beacon := &mockBeacon{genesis: 1, finalizedSlot: 10_000}
	p := New(beacon, &mockKeyRegistry{ops: nil})
	got, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, beacon.ids)
}

func TestGetVotingPowersAt_DedupesPubkeysBeforeBeaconFetch(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := testutil.PubkeyBytes(9)
	id := testutil.PubkeyHexFromBytes(pk)

	beacon := &mockBeacon{
		genesis:       1,
		finalizedSlot: 10_000,
		validatorsByID: map[string]types.BeaconValidator{
			id: {Pubkey: id, EffectiveBalance: 123},
		},
	}
	p := New(beacon, &mockKeyRegistry{ops: []types.OperatorWithKeys{
		{Operator: op, Keys: []types.Key{{Payload: pk}, {Payload: pk}}},
	}})
	got, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(123), got[0].VotingPowerGwei)
	require.Len(t, beacon.ids, 2)
	require.Len(t, beacon.ids[0], 1)
	require.Equal(t, id, beacon.ids[0][0])
	require.Len(t, beacon.ids[1], 1)
	require.Equal(t, id, beacon.ids[1][0])
}

func TestGetVotingPowersAt_UpstreamErrorsWrapped(t *testing.T) {
	expected := errors.New("boom")
	p := New(&mockBeacon{errGenesis: expected}, &mockKeyRegistry{})
	_, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUpstream)
	require.ErrorContains(t, err, "boom")
}

func TestGetVotingPowersAt_DecrementsSlotUntilStateExists(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := testutil.PubkeyBytes(10)
	id := testutil.PubkeyHexFromBytes(pk)

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		errByState:    map[string]error{"64": fakeNotFound{}},
		validatorsByID: map[string]types.BeaconValidator{
			id: {Pubkey: id, EffectiveBalance: 32000000000},
		},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{{Operator: op, Keys: []types.Key{{Payload: pk}}}},
	})

	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000+2*384)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(32000000000), got[0].VotingPowerGwei)
	require.Equal(t, uint64(63), meta.Slot)
	require.Equal(t, []string{"64", "63", "63"}, beacon.stateIDs)
}

func TestGetVotingPowersAt_FallsBackToFinalizedStateAfterLookback(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := testutil.PubkeyBytes(13)
	id := testutil.PubkeyHexFromBytes(pk)

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		errByState: map[string]error{
			"64": fakeNotFound{},
			"63": fakeNotFound{},
			"62": fakeNotFound{},
			"61": fakeNotFound{},
			"60": fakeNotFound{},
		},
		validatorsByID: map[string]types.BeaconValidator{
			id: {Pubkey: id, EffectiveBalance: 32000000000},
		},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{{Operator: op, Keys: []types.Key{{Payload: pk}}}},
	})

	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000+2*384)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(32000000000), got[0].VotingPowerGwei)
	require.Equal(t, uint64(10_000), meta.Slot)
	require.Equal(t, []string{"64", "63", "62", "61", "60", "finalized", "finalized"}, beacon.stateIDs)
}

func TestGetVotingPowersAt_SlotZeroFallsBackToFinalized(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := testutil.PubkeyBytes(11)
	id := testutil.PubkeyHexFromBytes(pk)

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		errByState:    map[string]error{"0": fakeNotFound{}},
		validatorsByID: map[string]types.BeaconValidator{
			id: {Pubkey: id, EffectiveBalance: 111},
		},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{{Operator: op, Keys: []types.Key{{Payload: pk}}}},
	})

	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(111), got[0].VotingPowerGwei)
	require.Equal(t, uint64(10_000), meta.Slot)
	require.Equal(t, []string{"0", "finalized", "finalized"}, beacon.stateIDs)
}

type fakeNotFound struct{}

func (fakeNotFound) Error() string  { return "not found" }
func (fakeNotFound) NotFound() bool { return true }

func TestGetVotingPowersAt_FallsBackWhenValidatorsStateMissing(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := testutil.PubkeyBytes(12)
	id := testutil.PubkeyHexFromBytes(pk)

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		errByState:    map[string]error{"64": fakeNotFound{}},
		validatorsByID: map[string]types.BeaconValidator{
			id: {Pubkey: id, EffectiveBalance: 32000000000},
		},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{{Operator: op, Keys: []types.Key{{Payload: pk}}}},
	})

	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000+2*384)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(32000000000), got[0].VotingPowerGwei)
	require.Equal(t, uint64(63), meta.Slot)
	require.Equal(t, []string{"64", "63", "63"}, beacon.stateIDs)
}

func TestGetVotingPowersAt_MockModeMapsOperatorsToValidatorIndexes(t *testing.T) {
	opA := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	opB := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		validatorsByID: map[string]types.BeaconValidator{
			"0": {Pubkey: testutil.PubkeyHex(1), EffectiveBalance: 11},
			"1": {Pubkey: testutil.PubkeyHex(2), EffectiveBalance: 22},
		},
	}
	keyReg := &mockKeyRegistry{ops: []types.OperatorWithKeys{
		{Operator: opB, Keys: nil},
		{Operator: opA, Keys: nil},
	}}

	p := New(beacon, keyReg, WithMock(true))
	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000+2*384)
	require.NoError(t, err)
	require.Equal(t, uint64(2), meta.Epoch)
	require.Equal(t, uint64(64), meta.Slot)
	require.Equal(t, 2, meta.MatchedValidator)
	require.Equal(t, 2, meta.OperatorCount)

	require.Len(t, got, 2)
	require.Equal(t, opA, got[0].Operator)
	require.Equal(t, uint64(11), got[0].VotingPowerGwei)
	require.Equal(t, opB, got[1].Operator)
	require.Equal(t, uint64(22), got[1].VotingPowerGwei)
	require.Equal(t, []string{"64", "64", "64"}, beacon.stateIDs)
	require.Equal(t, []string{"0"}, beacon.ids[0])
	require.Equal(t, []string{"0"}, beacon.ids[1])
	require.Equal(t, []string{"1"}, beacon.ids[2])
}
