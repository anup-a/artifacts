package serve

import (
	"os"
	"testing"
)

var productionCost = PasswordCost

// TestMain hashes test passwords cheaply: argon2 at full cost takes seconds
// under the race detector, and the tests are about the gate, not the hash.
func TestMain(m *testing.M) {
	PasswordCost.Time, PasswordCost.MemoryKiB = 1, 64
	os.Exit(m.Run())
}
