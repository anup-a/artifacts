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
// It is a backstop: pinning a variable to the API it belongs to is what keeps
// it from being sent somewhere that echoes it.
type redactor struct {
	w       io.Writer
	secrets [][]byte
	held    []byte
}

func newRedactor(w io.Writer, secrets map[string]string) *redactor {
	r := &redactor{w: w}
	for _, v := range secrets {
		if len(v) >= minRedact {
			r.secrets = append(r.secrets, []byte(v))
		}
	}
	return r
}

func (r *redactor) contains(s string) bool {
	for _, sec := range r.secrets {
		if strings.Contains(s, string(sec)) {
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
		buf = bytes.ReplaceAll(buf, sec, []byte(redactedMark))
	}
	keep := r.partial(buf)
	if _, err := r.w.Write(buf[:len(buf)-keep]); err != nil {
		return 0, err
	}
	r.held = append([]byte(nil), buf[len(buf)-keep:]...)
	return len(p), nil
}

// partial is the length of the longest tail of buf that is a proper prefix
// of some secret.
func (r *redactor) partial(buf []byte) int {
	best := 0
	for _, sec := range r.secrets {
		max := min(len(sec)-1, len(buf))
		for n := max; n > best; n-- {
			if bytes.Equal(buf[len(buf)-n:], sec[:n]) {
				best = n
				break
			}
		}
	}
	return best
}

// Flush writes whatever is held: at the end of the body it cannot be the
// start of a secret any more.
func (r *redactor) Flush() {
	if len(r.held) > 0 {
		r.w.Write(r.held)
		r.held = nil
	}
}
