package types

import "github.com/ethereum/go-ethereum/common"

type BeaconValidator struct {
	Pubkey           string
	EffectiveBalance uint64
}

type Key struct {
	Tag     uint8
	Payload []byte
}

type OperatorWithKeys struct {
	Operator common.Address
	Keys     []Key
}

type OperatorVotingPower struct {
	Operator        common.Address
	VotingPowerGwei uint64
}
