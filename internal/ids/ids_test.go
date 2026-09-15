package ids

import (
	"regexp"
	"strings"
	"testing"
)

var b62 = regexp.MustCompile(`^[0-9A-Za-z]+$`)

func TestArtifactID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewArtifactID()
		if len(id) != 12 || !b62.MatchString(id) {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("dup %q", id)
		}
		seen[id] = true
	}
}

func TestKey(t *testing.T) {
	plain, hash := NewKey()
	if !strings.HasPrefix(plain, "ak_") || len(hash) != 64 {
		t.Fatalf("%q %q", plain, hash)
	}
	if HashKey(plain) != hash {
		t.Fatal("hash mismatch")
	}
	if HashKey("ak_other") == hash {
		t.Fatal("collision")
	}
}
