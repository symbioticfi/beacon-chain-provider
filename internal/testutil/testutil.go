package testutil

import "encoding/hex"

// PubkeyBytes returns a 48-byte BLS pubkey filled with seed.
func PubkeyBytes(seed byte) []byte {
	out := make([]byte, 48)
	for i := range out {
		out[i] = seed
	}
	return out
}

// PubkeyHex returns a 0x-prefixed hex string for a 48-byte pubkey filled with seed.
func PubkeyHex(seed byte) string {
	return PubkeyHexFromBytes(PubkeyBytes(seed))
}

// PubkeyHexFromBytes returns a 0x-prefixed lowercase hex string for the given bytes.
func PubkeyHexFromBytes(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}
