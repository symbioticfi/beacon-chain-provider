package keyregistry

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
	relaygen "github.com/symbioticfi/relay/symbiotic/client/evm/gen"
)

type fakeEth struct {
	header  *gethtypes.Header
	err     error
	chainID *big.Int
}

func (f *fakeEth) HeaderByNumber(context.Context, *big.Int) (*gethtypes.Header, error) {
	return f.header, f.err
}

func (f *fakeEth) ChainID(context.Context) (*big.Int, error) {
	if f.chainID == nil {
		return big.NewInt(0), nil
	}
	return f.chainID, nil
}

type fakeCaller struct {
	rows       []relaygen.IKeyRegistryOperatorWithKeys
	err        error
	lastOpts   *bind.CallOpts
	lastTs     *big.Int
	calledOnce bool
}

func (f *fakeCaller) GetKeysAt(opts *bind.CallOpts, timestamp *big.Int) ([]relaygen.IKeyRegistryOperatorWithKeys, error) {
	f.calledOnce = true
	f.lastOpts = opts
	f.lastTs = timestamp
	return f.rows, f.err
}

func TestClient_GetKeysAt_FiltersByTagAndUsesFinalizedBlock(t *testing.T) {
	caller := &fakeCaller{rows: []relaygen.IKeyRegistryOperatorWithKeys{
		{
			Operator: common.HexToAddress("0x0000000000000000000000000000000000000001"),
			Keys: []relaygen.IKeyRegistryKey{
				{Tag: 0x20, Payload: []byte{1, 2}},
				{Tag: 0x10, Payload: []byte{9}},
			},
		},
		{
			Operator: common.HexToAddress("0x0000000000000000000000000000000000000002"),
			Keys:     []relaygen.IKeyRegistryKey{{Tag: 0x10, Payload: []byte{8}}},
		},
	}}
	c := &Client{eth: &fakeEth{header: &gethtypes.Header{Number: big.NewInt(123)}}, caller: caller, keyTag: 0x20, chainID: 1}

	got, err := c.GetKeysAt(context.Background(), 555)
	require.NoError(t, err)
	require.True(t, caller.calledOnce)
	require.Equal(t, int64(123), caller.lastOpts.BlockNumber.Int64())
	require.Equal(t, uint64(555), caller.lastTs.Uint64())
	require.Len(t, got, 1)
	require.Equal(t, "0x0000000000000000000000000000000000000001", got[0].Operator.Hex())
	require.Len(t, got[0].Keys, 1)
	require.Equal(t, uint8(0x20), got[0].Keys[0].Tag)
}

func TestClient_VerifyChainID(t *testing.T) {
	c := &Client{eth: &fakeEth{chainID: big.NewInt(17000)}, caller: &fakeCaller{}, keyTag: 0x20, chainID: 17000}
	require.NoError(t, c.verifyChainID(context.Background()))

	cMismatch := &Client{eth: &fakeEth{chainID: big.NewInt(1)}, caller: &fakeCaller{}, keyTag: 0x20, chainID: 17000}
	err := cMismatch.verifyChainID(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "chain id mismatch")
}
