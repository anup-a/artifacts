package connect

import (
	"regexp"
	"strings"
	"testing"
)

func TestNewCodeShapeAndSpread(t *testing.T) {
	shape := regexp.MustCompile(`^[` + alphabet + `]{4}-[` + alphabet + `]{4}$`)
	seen := map[string]bool{}
	for range 5000 {
		c := NewCode()
		if !shape.MatchString(c) {
			t.Fatalf("bad code %q", c)
		}
		if seen[c] {
			t.Fatalf("repeat %q", c)
		}
		seen[c] = true
	}
}

// Whatever a person retypes should land on the same hash, and anything that
// cannot be a code should land on none.
func TestNormalizeAndHash(t *testing.T) {
	for _, in := range []string{"K7QD-M3XR", "k7qd-m3xr", " K7QD M3XR ", "K7QDM3XR", "k7qd_m3xr"} {
		if Normalize(in) != "K7QDM3XR" || Hash(in) != Hash("K7QD-M3XR") {
			t.Fatalf("%q -> %q", in, Normalize(in))
		}
	}
	for _, in := range []string{"", "K7QD-M3X", "K7QD-M3XRR", "K0QD-M3XR", "K7QD/M3XR", "ak_K7QDM3XR"} {
		if Normalize(in) != "" || Hash(in) != "" {
			t.Fatalf("%q should not normalize", in)
		}
	}
	if Hash("K7QD-M3XR") == HashDeviceSecret("K7QDM3XR") {
		t.Fatal("code and device hashes share a space")
	}
}

func TestKeyName(t *testing.T) {
	cases := map[string]string{
		"":                      "agent",
		"   ":                   "agent",
		"claude-code on laptop": "claude-code on laptop",
		"  codex\n on\tbox ":    "codex on box",
		strings.Repeat("é", 70): strings.Repeat("é", 64),
		"\x00\x01":              "agent",
	}
	for in, want := range cases {
		if got := KeyName(in); got != want {
			t.Fatalf("KeyName(%q) = %q, want %q", in, got, want)
		}
	}
}
