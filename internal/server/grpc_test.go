package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	votingpowerv1 "github.com/symbioticfi/beacon-chain-provider/api/gen/votingpower/v1"
	"github.com/symbioticfi/beacon-chain-provider/internal/provider"
	"github.com/symbioticfi/beacon-chain-provider/internal/testutil"
	"github.com/symbioticfi/beacon-chain-provider/internal/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type mockBeaconClient struct {
	genesis       uint64
	finalizedSlot uint64
	validators    []types.BeaconValidator
	err           error
}

func (m *mockBeaconClient) GetGenesis(context.Context) (uint64, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.genesis, nil
}

func (m *mockBeaconClient) GetFinalizedSlot(context.Context) (uint64, error) {
	if m.err != nil {
		return 0, m.err
	}
	return m.finalizedSlot, nil
}

func (m *mockBeaconClient) GetValidatorsByState(context.Context, string, []string, []string) ([]types.BeaconValidator, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.validators, nil
}

type mockKeyRegistryClient struct {
	ops []types.OperatorWithKeys
	err error
}

func (m *mockKeyRegistryClient) GetKeysAt(context.Context, uint64) ([]types.OperatorWithKeys, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.ops, nil
}

func TestGRPCServer_EndToEndBufconn(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()

	beacon := &mockBeaconClient{
		genesis:       1000,
		finalizedSlot: 10_000,
		validators: []types.BeaconValidator{
			{Pubkey: testutil.PubkeyHex(1), EffectiveBalance: 20},
			{Pubkey: testutil.PubkeyHex(2), EffectiveBalance: 10},
		},
	}
	keyRegistry := &mockKeyRegistryClient{
		ops: []types.OperatorWithKeys{
			{Operator: common.HexToAddress("0x0000000000000000000000000000000000000001"), Keys: []types.Key{{Payload: testutil.PubkeyBytes(1)}}},
			{Operator: common.HexToAddress("0x0000000000000000000000000000000000000002"), Keys: []types.Key{{Payload: testutil.PubkeyBytes(2)}}},
		},
	}
	votingpowerv1.RegisterVotingPowerProviderServiceServer(gs, NewGRPCServer(slog.Default(), provider.New(beacon, keyRegistry)))
	go func() {
		_ = gs.Serve(lis)
	}()
	t.Cleanup(gs.Stop)

	ctx := context.Background()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithInsecure(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := votingpowerv1.NewVotingPowerProviderServiceClient(conn)
	resp1, err := client.GetVotingPowersAt(ctx, &votingpowerv1.GetVotingPowersAtRequest{Timestamp: 1234})
	require.NoError(t, err)
	resp2, err := client.GetVotingPowersAt(ctx, &votingpowerv1.GetVotingPowersAtRequest{Timestamp: 1234})
	require.NoError(t, err)

	require.Equal(t, resp1, resp2)
	require.Len(t, resp1.GetVotingPowers(), 2)
	require.Equal(t, "0x0000000000000000000000000000000000000001", resp1.GetVotingPowers()[0].GetOperator())
	require.Equal(t, "20", resp1.GetVotingPowers()[0].GetVotingPower())
}

func TestMapProviderError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "invalid", err: provider.ErrTimestampBeforeGenesis, code: codes.InvalidArgument},
		{name: "precondition", err: provider.ErrEpochNotFinalized, code: codes.FailedPrecondition},
		{name: "deadline", err: errors.Join(provider.ErrUpstream, context.DeadlineExceeded), code: codes.DeadlineExceeded},
		{name: "unavailable", err: provider.ErrUpstream, code: codes.Unavailable},
		{name: "internal", err: errors.New("x"), code: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := mapProviderError(tc.err)
			require.Equal(t, tc.code, code)
		})
	}
}

func TestGRPCServer_NilRequest(t *testing.T) {
	s := NewGRPCServer(slog.Default(), provider.New(&mockBeaconClient{}, &mockKeyRegistryClient{}))
	_, err := s.GetVotingPowersAt(context.Background(), nil)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
