package provider

import (
	"encoding/hex"
	"fmt"
	"strings"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
)

const (
	bls12381PubkeyLen         = 48
	bls12381OnChainPubkeyLen  = 128
	bls12381G1UncompressedLen = 96
)

func NormalizeBLSPubkeyHex(s string) (string, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if len(s) != bls12381PubkeyLen*2 {
		return "", fmt.Errorf("invalid pubkey length: got=%d want=%d", len(s)/2, bls12381PubkeyLen)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("decode pubkey: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func NormalizeBLS12381KeyPayload(payload []byte) (string, error) {
	switch len(payload) {
	case bls12381PubkeyLen:
		return hex.EncodeToString(payload), nil
	case bls12381OnChainPubkeyLen:
		// The offchain-middleware BLS12-381 on-chain key is 128 bytes:
		// x and y are each left-padded from 48 to 64 bytes.
		g1Uncompressed := make([]byte, bls12381G1UncompressedLen)
		copy(g1Uncompressed[:48], payload[16:64])  // x
		copy(g1Uncompressed[48:], payload[80:128]) // y

		var g1 bls12381.G1Affine
		if _, err := g1.SetBytes(g1Uncompressed); err != nil {
			return "", fmt.Errorf("decode on-chain g1 key: %w", err)
		}
		compressed := g1.Bytes()
		return hex.EncodeToString(compressed[:]), nil
	default:
		return "", fmt.Errorf("unsupported bls12-381 payload length: %d", len(payload))
	}
}
