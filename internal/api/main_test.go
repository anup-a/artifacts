package api

import (
	"os"
	"testing"

	"github.com/anup-a/byagent/internal/serve"
)

// TestMain hashes page passwords cheaply: argon2 at full cost takes seconds
// under the race detector.
func TestMain(m *testing.M) {
	serve.PasswordCost.Time, serve.PasswordCost.MemoryKiB = 1, 64
	os.Exit(m.Run())
}
