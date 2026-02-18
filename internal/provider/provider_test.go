package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
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
			pubkeyHex(1): {Pubkey: pubkeyHex(1), EffectiveBalance: 32_000_000_000},
			pubkeyHex(2): {Pubkey: pubkeyHex(2), EffectiveBalance: 31_000_000_000},
			pubkeyHex(3): {Pubkey: pubkeyHex(3), EffectiveBalance: 30_000_000_000},
		},
	}
	keyReg := &mockKeyRegistry{ops: []types.OperatorWithKeys{
		{Operator: opB, Keys: []types.Key{{Tag: 0x20, Payload: pubkeyBytes(1)}, {Tag: 0x20, Payload: pubkeyBytes(2)}}},
		{Operator: opA, Keys: []types.Key{{Tag: 0x20, Payload: pubkeyBytes(3)}}},
	}}

	p := New(beacon, keyReg)
	got, meta, err := p.GetVotingPowersAt(context.Background(), 1000+2*384)
	require.NoError(t, err)
	require.Equal(t, uint64(2), meta.Epoch)
	require.Equal(t, uint64(64), meta.Slot)
	require.Equal(t, 3, meta.MatchedValidator)
	require.Equal(t, 2, meta.OperatorCount)
	require.Equal(t, []string{"64"}, beacon.stateIDs)
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
	pk := pubkeyBytes(7)
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
				pubkeyHex(1): {Pubkey: pubkeyHex(1), EffectiveBalance: 100},
			},
		},
		&mockKeyRegistry{ops: []types.OperatorWithKeys{{Operator: common.HexToAddress("0x0000000000000000000000000000000000000001"), Keys: []types.Key{{Payload: pubkeyBytes(2)}}}}},
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
		pk := pubkeyBytes(byte(i % 251))
		pk[0] = byte(i % 255)
		pk[1] = byte((i / 255) % 255)
		keys = append(keys, types.Key{Payload: pk})
		id := pubkeyHexFromBytes(pk)
		validators[id] = types.BeaconValidator{Pubkey: id, EffectiveBalance: 1}
	}

	beacon := &mockBeacon{genesis: 1, finalizedSlot: 10_000, validatorsByID: validators}
	p := New(beacon, &mockKeyRegistry{ops: []types.OperatorWithKeys{{Operator: op, Keys: keys}}})
	got, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, uint64(total), got[0].VotingPowerGwei)

	require.Len(t, beacon.ids, 2)
	require.Len(t, beacon.ids[0], 1000)
	require.Len(t, beacon.ids[1], 1)
	require.Equal(t, []string{"0", "0"}, beacon.stateIDs)
	require.Nil(t, beacon.statuses[0])
	require.Nil(t, beacon.statuses[1])
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
	pk := pubkeyBytes(9)
	id := pubkeyHexFromBytes(pk)

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
	require.Len(t, beacon.ids, 1)
	require.Len(t, beacon.ids[0], 1)
	require.Equal(t, id, beacon.ids[0][0])
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
	pk := pubkeyBytes(10)
	id := pubkeyHexFromBytes(pk)

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
	require.Equal(t, []string{"64", "63"}, beacon.stateIDs)
}

func TestGetVotingPowersAt_ReturnsPreconditionWhenNoStateExists(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := pubkeyBytes(11)

	beacon := &mockBeacon{
		genesis:       1000,
		finalizedSlot: 10_000,
		errByState:    map[string]error{"0": fakeNotFound{}},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{{Operator: op, Keys: []types.Key{{Payload: pk}}}},
	})

	_, _, err := p.GetVotingPowersAt(context.Background(), 1000)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrEpochNotFinalized)
}

type fakeNotFound struct{}

func (fakeNotFound) Error() string  { return "not found" }
func (fakeNotFound) NotFound() bool { return true }

func TestGetVotingPowersAt_FallsBackWhenValidatorsStateMissing(t *testing.T) {
	op := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	pk := pubkeyBytes(12)
	id := pubkeyHexFromBytes(pk)

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
	require.Equal(t, []string{"64", "63"}, beacon.stateIDs)
}

func TestGetVotingPowersAt_MockMap_DeterministicMapping(t *testing.T) {
	opA := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	opB := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	keysPath := writeHoodiKeysFile(t, []string{pubkeyHex(1), pubkeyHex(2), pubkeyHex(3)})

	beacon := &mockBeacon{
		genesis:       1,
		finalizedSlot: 10_000,
		validatorsByID: map[string]types.BeaconValidator{
			pubkeyHex(1): {Pubkey: pubkeyHex(1), EffectiveBalance: 101},
			pubkeyHex(2): {Pubkey: pubkeyHex(2), EffectiveBalance: 202},
			pubkeyHex(3): {Pubkey: pubkeyHex(3), EffectiveBalance: 303},
		},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{
			{Operator: opA, Keys: []types.Key{{Payload: pubkeyBytes(11)}}},
			{Operator: opB, Keys: []types.Key{{Payload: pubkeyBytes(22)}}},
		},
	}, WithMockMap(keysPath))

	got1, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	got2, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, got1, got2)
	require.Len(t, got1, 2)

	sums := map[common.Address]uint64{
		got1[0].Operator: got1[0].VotingPowerGwei,
		got1[1].Operator: got1[1].VotingPowerGwei,
	}
	require.Contains(t, []uint64{101, 202, 303}, sums[opA])
	require.Contains(t, []uint64{101, 202, 303}, sums[opB])
	require.NotEqual(t, uint64(0), sums[opA])
	require.NotEqual(t, uint64(0), sums[opB])
}

func TestGetVotingPowersAt_MockMap_FailsWhenNotEnoughHoodiKeys(t *testing.T) {
	keysPath := writeHoodiKeysFile(t, []string{pubkeyHex(1)})

	p := New(&mockBeacon{genesis: 1, finalizedSlot: 10_000}, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{
			{Operator: common.HexToAddress("0x0000000000000000000000000000000000000001"), Keys: []types.Key{{Payload: pubkeyBytes(11)}}},
			{Operator: common.HexToAddress("0x0000000000000000000000000000000000000002"), Keys: []types.Key{{Payload: pubkeyBytes(22)}}},
		},
	}, WithMockMap(keysPath))

	_, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMockMapInsufficientKeys)
}

func TestGetVotingPowersAt_MockMap_AssignsKeysForOperatorsWithoutKeys(t *testing.T) {
	opA := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	opB := common.HexToAddress("0x00000000000000000000000000000000000000bb")

	keysPath := writeHoodiKeysFile(t, []string{pubkeyHex(1), pubkeyHex(2), pubkeyHex(3)})

	beacon := &mockBeacon{
		genesis:       1,
		finalizedSlot: 10_000,
		validatorsByID: map[string]types.BeaconValidator{
			pubkeyHex(1): {Pubkey: pubkeyHex(1), EffectiveBalance: 111},
			pubkeyHex(2): {Pubkey: pubkeyHex(2), EffectiveBalance: 222},
			pubkeyHex(3): {Pubkey: pubkeyHex(3), EffectiveBalance: 333},
		},
	}

	p := New(beacon, &mockKeyRegistry{
		ops: []types.OperatorWithKeys{
			{Operator: opA, Keys: nil},
			{Operator: opB, Keys: nil},
		},
	}, WithMockMap(keysPath))

	got, _, err := p.GetVotingPowersAt(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.NotEqual(t, uint64(0), got[0].VotingPowerGwei)
	require.NotEqual(t, uint64(0), got[1].VotingPowerGwei)
}

func pubkeyBytes(seed byte) []byte {
	out := make([]byte, 48)
	for i := range out {
		out[i] = seed
	}
	return out
}

func pubkeyHex(seed byte) string {
	b := pubkeyBytes(seed)
	return pubkeyHexFromBytes(b)
}

func pubkeyHexFromBytes(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, 2+len(b)*2)
	out[0] = '0'
	out[1] = 'x'
	for i, v := range b {
		out[2+i*2] = hexChars[v>>4]
		out[2+i*2+1] = hexChars[v&0x0f]
	}
	return string(out)
}

func writeHoodiKeysFile(t *testing.T, keys []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hoodi.json")

	payload := "[\n"
	for i, key := range keys {
		if i > 0 {
			payload += ",\n"
		}
		payload += `  {"index": ` + strconv.Itoa(i) + `, "pubkey": "` + key + `"}`
	}
	payload += "\n]\n"

	var decoded []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(payload), &decoded))
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o644))
	return path
}
