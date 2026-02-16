package keyregistry

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	relaygen "github.com/symbioticfi/relay/symbiotic/client/evm/gen"

	"github.com/symbioticfi/beacon-chain-provider/internal/types"
)

type ethAPI interface {
	HeaderByNumber(ctx context.Context, number *big.Int) (*gethtypes.Header, error)
	ChainID(ctx context.Context) (*big.Int, error)
}

type keyRegistryCaller interface {
	GetKeysAt(opts *bind.CallOpts, timestamp *big.Int) ([]relaygen.IKeyRegistryOperatorWithKeys, error)
}

type Client struct {
	eth     ethAPI
	caller  keyRegistryCaller
	keyTag  uint8
	chainID uint64
}

func NewClient(ctx context.Context, rpcURL string, contractAddress common.Address, keyTag uint8, chainID uint64) (*Client, error) {
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial eth rpc: %w", err)
	}
	caller, err := relaygen.NewKeyRegistryCaller(contractAddress, eth)
	if err != nil {
		return nil, fmt.Errorf("new key registry caller: %w", err)
	}
	c := &Client{eth: eth, caller: caller, keyTag: keyTag, chainID: chainID}
	if err := c.verifyChainID(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) GetKeysAt(ctx context.Context, timestamp uint64) ([]types.OperatorWithKeys, error) {
	finalizedHeader, err := c.eth.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
	if err != nil {
		return nil, fmt.Errorf("load finalized block: %w", err)
	}
	if finalizedHeader == nil || finalizedHeader.Number == nil {
		return nil, fmt.Errorf("finalized header not available")
	}
	rows, err := c.caller.GetKeysAt(&bind.CallOpts{Context: ctx, BlockNumber: finalizedHeader.Number}, new(big.Int).SetUint64(timestamp))
	if err != nil {
		return nil, fmt.Errorf("contract getKeysAt: %w", err)
	}

	out := make([]types.OperatorWithKeys, 0, len(rows))
	for _, row := range rows {
		filtered := make([]types.Key, 0, len(row.Keys))
		for _, k := range row.Keys {
			if k.Tag != c.keyTag {
				continue
			}
			filtered = append(filtered, types.Key{Tag: k.Tag, Payload: k.Payload})
		}
		if len(filtered) == 0 {
			continue
		}
		out = append(out, types.OperatorWithKeys{Operator: row.Operator, Keys: filtered})
	}
	return out, nil
}

func (c *Client) verifyChainID(ctx context.Context) error {
	actual, err := c.eth.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("get chain id: %w", err)
	}
	if !actual.IsUint64() {
		return fmt.Errorf("chain id is too large: %s", actual.String())
	}
	if actual.Uint64() != c.chainID {
		return fmt.Errorf("chain id mismatch: expected=%d actual=%d", c.chainID, actual.Uint64())
	}
	return nil
}
