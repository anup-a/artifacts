// Package ids generates artifact ids and API keys.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// maxUnbiased is the largest multiple of 62 that fits in a byte, so 248. A
// random byte below it maps to a character with modulo, every byte from 248 up
// is rejected. Without that rejection the first 8 characters of the alphabet
// would come up more often than the rest.
const maxUnbiased = 256 - 256%len(alphabet)

// base62 returns width characters drawn uniformly from the alphabet, using
// rejection sampling over bytes from crypto/rand.
func base62(width int) string {
	out := make([]byte, 0, width)
	for len(out) < width {
		// One byte per character still needed. Rejected bytes, about 3 percent
		// of them, come back around on the next pass.
		for _, b := range random(width - len(out)) {
			if int(b) < maxUnbiased {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
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

// NewArtifactID returns 12 random base62 characters.
func NewArtifactID() string { return base62(12) }

// NewKey returns the plaintext key (shown once) and its sha256 hex (stored).
func NewKey() (string, string) {
	plain := "ak_" + NewToken()
	return plain, HashKey(plain)
}

// NewToken returns 43 random base62 characters, about 256 bits of entropy.
func NewToken() string { return base62(43) }

// ShareCodeDigits is the length of a share code. Six digits is a million
// possibilities, about 20 bits: short enough to read down a phone line, and
// small enough that the server, not the code, is what stops a guesser. Every
// unlock attempt is counted against the artifact, so this length is only safe
// while that limit is in place — see serve.unlockGuard.
const ShareCodeDigits = 6

const digitMaxUnbiased = 256 - 256%10

// NewShareCode returns a uniformly random six-digit code, "000000" included.
// It is drawn from crypto/rand with the same rejection sampling as the ids: a
// code biased towards low digits would shrink an already small space.
func NewShareCode() string {
	out := make([]byte, 0, ShareCodeDigits)
	for len(out) < ShareCodeDigits {
		for _, b := range random(ShareCodeDigits - len(out)) {
			if int(b) < digitMaxUnbiased {
				out = append(out, '0'+b%10)
			}
		}
	}
	return string(out)
}

func HashKey(plain string) string {
	h := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(h[:])
}
