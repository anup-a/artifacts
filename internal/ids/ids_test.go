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

// A short code has to be exactly as random as it is short: any bias or any
// repeat shrinks a space that is already only a million wide.
func TestShareCode(t *testing.T) {
	digits := regexp.MustCompile(`^[0-9]{6}$`)
	counts := map[rune]int{}
	seen := map[string]int{}
	const runs = 20000
	for range runs {
		c := NewShareCode()
		if !digits.MatchString(c) {
			t.Fatalf("bad share code %q", c)
		}
		seen[c]++
		for _, r := range c {
			counts[r]++
		}
	}
	// Repeats are not a defect here, they are arithmetic: 20k draws from a
	// million collide about runs^2/2N times, so roughly 200, and a code
	// appearing three times is expected about once per run. What would show a
	// broken generator is the count of repeats being far off that number —
	// near zero for a counter, far above it for a generator stuck in a corner
	// of the space. The bound below is eight standard deviations wide either
	// way, so it catches that and never a healthy run.
	repeats := runs - len(seen)
	expected := runs * runs / (2 * 1000000)
	if repeats < expected/2 || repeats > expected*2 {
		t.Errorf("%d repeated codes in %d draws, want near %d", repeats, runs, expected)
	}
	mean := float64(runs*ShareCodeDigits) / 10
	for d := '0'; d <= '9'; d++ {
		if got := float64(counts[d]); got < mean*0.9 || got > mean*1.1 {
			t.Errorf("digit %q occurred %.0f times, want near %.0f", d, got, mean)
		}
	}
}

// countChars tallies how often each alphabet character occurs in s.
func countChars(counts map[rune]int, s string) {
	for _, r := range s {
		counts[r]++
	}
}

// assertUniform fails when any alphabet character strays more than tol away
// from the mean. tol is a fraction, so 0.25 allows plus or minus 25 percent.
func assertUniform(t *testing.T, counts map[rune]int, total int, tol float64, what string) {
	t.Helper()
	mean := float64(total) / float64(len(alphabet))
	low, high := mean*(1-tol), mean*(1+tol)
	for _, r := range alphabet {
		got := float64(counts[r])
		if got < low || got > high {
			t.Errorf("%s: character %q occurred %.0f times, want between %.0f and %.0f (mean %.0f)", what, r, got, low, high, mean)
		}
	}
}

// TestTokenCharacterDistribution checks the overall character frequency across
// roughly 200k generated characters.
func TestTokenCharacterDistribution(t *testing.T) {
	counts := map[rune]int{}
	total := 0
	for range 5000 {
		token := NewToken()
		countChars(counts, token)
		total += len(token)
	}
	assertUniform(t, counts, total, 0.25, "token characters")
}

// TestArtifactIDFirstCharacterDistribution guards the position that modulo bias
// hits hardest. Big-int division leaves the most significant digit skewed
// whenever the random input does not line up with 62^width.
func TestArtifactIDFirstCharacterDistribution(t *testing.T) {
	counts := map[rune]int{}
	const n = 100000
	for range n {
		id := NewArtifactID()
		counts[rune(id[0])]++
	}
	assertUniform(t, counts, n, 0.25, "artifact id first character")
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

func TestToken(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		token := NewToken()
		if len(token) != 43 || !b62.MatchString(token) {
			t.Fatal("token must be 43 base62 characters")
		}
		if seen[token] {
			t.Fatal("duplicate token")
		}
		seen[token] = true
	}
}
