// Package ids generates artifact ids and API keys.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func base62(b []byte, width int) string {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		var m big.Int
		n.DivMod(n, base, &m)
		out[i] = alphabet[m.Int64()]
	}
	return string(out)
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return b
}

// NewArtifactID returns 12 base62 chars from 9 random bytes.
func NewArtifactID() string { return base62(random(9), 12) }

// NewKey returns the plaintext key (shown once) and its sha256 hex (stored).
func NewKey() (string, string) {
	plain := "ak_" + base62(random(32), 43)
	return plain, HashKey(plain)
}

func HashKey(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}
