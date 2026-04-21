package provider

import (
	"encoding/hex"
	"testing"

	bls12381 "github.com/consensys/gnark-crypto/ecc/bls12-381"
	"github.com/stretchr/testify/require"
)

func TestNormalizeBLS12381KeyPayload_Compressed48(t *testing.T) {
	in := make([]byte, 48)
	for i := range in {
		in[i] = byte(i)
	}
	got, err := NormalizeBLS12381KeyPayload(in)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(in), got)
}

func TestNormalizeBLS12381KeyPayload_OnChain128(t *testing.T) {
	_, _, g1, _ := bls12381.Generators()
	uncompressed := g1.Marshal() // 96 bytes (x||y)
	padded := make([]byte, 128)
	copy(padded[16:64], uncompressed[:48])
	copy(padded[80:128], uncompressed[48:96])

	got, err := NormalizeBLS12381KeyPayload(padded)
	require.NoError(t, err)

	compressed := g1.Bytes()
	require.Equal(t, hex.EncodeToString(compressed[:]), got)
}

func TestNormalizeBLS12381KeyPayload_UnsupportedLength(t *testing.T) {
	_, err := NormalizeBLS12381KeyPayload([]byte{1, 2, 3})
	require.Error(t, err)
	require.ErrorContains(t, err, "unsupported bls12-381 payload length")
}
