// Package connect holds what the API and the dashboard share about connect
// codes: how a code looks, how long it lives, and how it is hashed.
//
// A connect code is the one thing that crosses from a signed-in person to an
// agent. It is short enough to read off a screen, works once and expires in
// ten minutes, so a copy left in a chat log is worth nothing afterwards. The
// API key it becomes goes straight from the server to the CLI.
package connect

import (
	"crypto/rand"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anup-a/byagent/internal/ids"
)

// TTL is how long a connect code can be redeemed or approved.
const TTL = 10 * time.Minute

// alphabet leaves out 0/O, 1/I/L and U, so a code read aloud or retyped
// cannot be mistaken for another. 30 characters, 8 of them: about 2^39.
const alphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

const length = 8

// maxUnbiased is the largest multiple of len(alphabet) below 256.
const maxUnbiased = 256 - 256%len(alphabet)

// NewCode returns a code formatted for people, e.g. "K7QD-M3XR".
func NewCode() string {
	out := make([]byte, 0, length)
	b := make([]byte, length)
	for len(out) < length {
		if _, err := rand.Read(b); err != nil {
			panic(err) // crypto/rand failure is unrecoverable
		}
		for _, c := range b {
			if int(c) < maxUnbiased && len(out) < length {
				out = append(out, alphabet[int(c)%len(alphabet)])
			}
		}
	}
	return string(out[:4]) + "-" + string(out[4:])
}

// Normalize turns whatever a person or agent typed into the canonical form:
// upper case, separators and spaces dropped. It returns "" for anything that
// cannot be a code.
func Normalize(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(raw) {
		switch {
		case r == '-' || r == ' ' || r == '_':
		case strings.ContainsRune(alphabet, r):
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != length {
		return ""
	}
	return b.String()
}

// Hash is what the store keeps for a code; the code itself is never stored.
// It takes the raw input, so "k7qd m3xr" and "K7QD-M3XR" hash alike.
func Hash(raw string) string {
	n := Normalize(raw)
	if n == "" {
		return ""
	}
	return ids.HashKey("connect:" + n)
}

// NewDeviceSecret returns the secret a CLI polls with, and its hash.
func NewDeviceSecret() (string, string) {
	plain := "dc_" + ids.NewToken()
	return plain, ids.HashKey(plain)
}

// HashDeviceSecret hashes a device secret the CLI sent back.
func HashDeviceSecret(plain string) string { return ids.HashKey(plain) }

// KeyName cleans the name an agent asks for its key: printable, single
// spaced, at most 64 characters. An empty or unusable name becomes "agent".
func KeyName(raw string) string {
	// Control characters count as spaces, and runs of space become one.
	name := strings.Join(strings.FieldsFunc(raw, func(r rune) bool { return r <= 0x20 || r == 0x7f }), " ")
	for utf8.RuneCountInString(name) > 64 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" {
		return "agent"
	}
	return name
}
