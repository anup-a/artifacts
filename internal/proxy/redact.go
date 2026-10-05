package proxy

import (
	"bytes"
	"io"
	"strings"
)

// redactedMark replaces a secret an upstream echoed back.
const redactedMark = "[redacted]"

// minRedact is the shortest value worth hunting for: anything shorter would
// match ordinary text and mangle answers without protecting a real key.
const minRedact = 8

// redactor passes a response through, replacing any secret it carries. A
// secret can straddle two reads, so the tail of each chunk that could be the
// start of a secret is held back until the next one shows whether it was.
// Ordinary text (an SSE event ending in a blank line, say) is never a secret's
// prefix, so streaming is not delayed.
//
// Matching is Knuth-Morris-Pratt per secret, so the work per byte is bounded
// by the number of secrets (at most MaxVarsPerRoute) whatever the secrets and
// the body look like: a body of "AAAA…" against a secret of "AAA…B" costs no
// more than any other.
//
// It is a backstop: pinning a variable to the API it belongs to is what keeps
// it from being sent somewhere that echoes it.
type redactor struct {
	w       io.Writer
	secrets []kmp
	held    []byte
}

// kmp is one secret with its failure table.
type kmp struct {
	pat  []byte
	fail []int
}

func newKMP(p []byte) kmp {
	fail := make([]int, len(p))
	k := 0
	for i := 1; i < len(p); i++ {
		for k > 0 && p[i] != p[k] {
			k = fail[k-1]
		}
		if p[i] == p[k] {
			k++
		}
		fail[i] = k
	}
	return kmp{pat: p, fail: fail}
}

// scan runs the automaton over buf. It returns the end offsets of complete,
// non-overlapping matches and the length of the secret's prefix that the end
// of buf leaves matched.
func (m kmp) scan(buf []byte) (ends []int, partial int) {
	k := 0
	for i, c := range buf {
		for k > 0 && c != m.pat[k] {
			k = m.fail[k-1]
		}
		if c == m.pat[k] {
			k++
		}
		if k == len(m.pat) {
			ends = append(ends, i+1)
			k = 0
		}
	}
	return ends, k
}

func newRedactor(w io.Writer, secrets map[string]string) *redactor {
	r := &redactor{w: w}
	for _, v := range secrets {
		if len(v) >= minRedact {
			r.secrets = append(r.secrets, newKMP([]byte(v)))
		}
	}
	return r
}

func (r *redactor) contains(s string) bool {
	for _, sec := range r.secrets {
		if strings.Contains(s, string(sec.pat)) {
			return true
		}
	}
	return false
}

func (r *redactor) Write(p []byte) (int, error) {
	if len(r.secrets) == 0 {
		return r.w.Write(p)
	}
	buf := append(r.held, p...)
	for _, sec := range r.secrets {
		if ends, _ := sec.scan(buf); len(ends) > 0 {
			buf = replaceAt(buf, ends, len(sec.pat))
		}
	}
	// Hold back the longest tail that could still grow into a secret,
	// scanning only the last len(secret)-1 bytes for each.
	keep := 0
	for _, sec := range r.secrets {
		_, partial := sec.scan(buf[max(0, len(buf)-len(sec.pat)+1):])
		keep = max(keep, partial)
	}
	if _, err := r.w.Write(buf[:len(buf)-keep]); err != nil {
		return 0, err
	}
	r.held = append([]byte(nil), buf[len(buf)-keep:]...)
	return len(p), nil
}

// replaceAt swaps each match (ending at ends, n bytes long) for the mark.
func replaceAt(buf []byte, ends []int, n int) []byte {
	var out bytes.Buffer
	out.Grow(len(buf))
	prev := 0
	for _, e := range ends {
		out.Write(buf[prev : e-n])
		out.WriteString(redactedMark)
		prev = e
	}
	out.Write(buf[prev:])
	return out.Bytes()
}

// Flush writes whatever is held: at the end of the body it cannot be the
// start of a secret any more.
func (r *redactor) Flush() {
	if len(r.held) > 0 {
		r.w.Write(r.held)
		r.held = nil
	}
}
