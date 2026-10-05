// Package variables holds the rules for account variables: the secrets a
// workspace stores so its pages' proxy routes can call other services
// without the secret ever reaching a browser. It seals values at rest,
// validates names, and decides which upstream hosts a value may be sent to.
package variables

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

// Limits on what a workspace may store.
const (
	MaxPerTenant  = 50
	MaxValueBytes = 4096
	MaxUpstreams  = 10
	MaxNameLen    = 64
)

var nameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidName reports whether name is an uppercase letter followed by
// uppercase letters, digits and underscores, at most MaxNameLen long.
func ValidName(name string) bool {
	return len(name) <= MaxNameLen && nameRe.MatchString(name)
}

// ValidValue checks a secret before it is stored. Values end up in HTTP
// header and query values, so control characters (a CR or LF above all,
// which would split a header) are refused outright.
func ValidValue(v string) error {
	if v == "" {
		return errors.New("value is empty")
	}
	if len(v) > MaxValueBytes {
		return fmt.Errorf("value is longer than %d bytes", MaxValueBytes)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == 0x7f {
			return errors.New("value contains a control character or line break")
		}
	}
	return nil
}

// Check validates a variable about to be saved and returns its allowed
// upstreams cleaned. Messages never include the value.
func Check(name, value string, upstreams []string) ([]string, error) {
	if !ValidName(name) {
		return nil, errors.New("name must be an uppercase letter, then uppercase letters, digits and _, at most 64 characters (for example OPENROUTER_API_KEY)")
	}
	if err := ValidValue(value); err != nil {
		return nil, err
	}
	return NormalizeHosts(upstreams)
}

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeHost turns an allowed-upstream entry into a bare lowercase host
// name. A full URL is reduced to its host. IP addresses, ports, wildcards
// and single-label names (localhost, intranet) are refused: a variable is
// pinned to a public service by name.
func NormalizeHost(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("%q is not a host name", s)
		}
		if u.Port() != "" && u.Port() != "443" {
			return "", fmt.Errorf("%q: only the default https port is allowed", s)
		}
		s = u.Hostname()
	}
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return "", errors.New("host name is empty or too long")
	}
	if _, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return "", fmt.Errorf("%q is an IP address; use the service's host name", s)
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%q is not a public host name", s)
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return "", fmt.Errorf("%q is not a host name", s)
		}
	}
	return s, nil
}

// NormalizeHosts cleans a list of allowed upstreams: at least one, at most
// MaxUpstreams, deduplicated, in the order given.
func NormalizeHosts(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errors.New("allowed_upstreams needs at least one host, such as api.openai.com")
	}
	if len(in) > MaxUpstreams {
		return nil, fmt.Errorf("allowed_upstreams has more than %d hosts", MaxUpstreams)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, h := range in {
		n, err := NormalizeHost(h)
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

// HostAllowed reports whether host is one of allowed or a subdomain of one.
// Matching is on whole labels: "api.openai.com" admits "eu.api.openai.com"
// but never "evilapi.openai.com" or "api.openai.com.evil.test".
func HostAllowed(host string, allowed []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, a := range allowed {
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

// Sealer encrypts values with AES-256-GCM. It holds one or more keys: the
// first seals, every one of them opens, so a key can be rotated by putting
// the new key first and re-saving the values before dropping the old one.
type Sealer struct {
	keys []sealKey
}

type sealKey struct {
	id   string
	aead cipher.AEAD
}

// ErrSealed is returned when a stored value cannot be opened: the key that
// sealed it is gone, or the row was altered.
var ErrSealed = errors.New("variable cannot be decrypted with the configured VARIABLES_KEY")

// NewSealer parses VARIABLES_KEY: comma-separated base64 (standard or URL,
// padded or not) encodings of 32 random bytes.
func NewSealer(spec string) (*Sealer, error) {
	s := &Sealer{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		raw, err := decodeKey(part)
		if err != nil || len(raw) != 32 {
			return nil, errors.New("each key must be 32 random bytes, base64 encoded (openssl rand -base64 32)")
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		s.keys = append(s.keys, sealKey{id: hex.EncodeToString(sum[:4]), aead: aead})
	}
	if len(s.keys) == 0 {
		return nil, errors.New("no key given")
	}
	return s, nil
}

func decodeKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}

// aad binds a sealed value to its workspace and name, so a value copied to
// another row (another workspace, another name) no longer opens.
func aad(tenantID, name string) []byte {
	return []byte("byagent-variable\x00" + tenantID + "\x00" + name)
}

// Seal encrypts value for (tenantID, name). The result is "<key id>:<base64
// nonce and ciphertext>", safe to store as text.
func (s *Sealer) Seal(tenantID, name, value string) (string, error) {
	k := s.keys[0]
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := k.aead.Seal(nonce, nonce, []byte(value), aad(tenantID, name))
	return k.id + ":" + base64.RawStdEncoding.EncodeToString(out), nil
}

// Open decrypts what Seal produced for the same (tenantID, name).
func (s *Sealer) Open(tenantID, name, sealed string) (string, error) {
	id, body, ok := strings.Cut(sealed, ":")
	if !ok {
		return "", ErrSealed
	}
	raw, err := base64.RawStdEncoding.DecodeString(body)
	if err != nil {
		return "", ErrSealed
	}
	for _, k := range s.keys {
		if k.id != id {
			continue
		}
		n := k.aead.NonceSize()
		if len(raw) < n {
			return "", ErrSealed
		}
		plain, err := k.aead.Open(nil, raw[:n], raw[n:], aad(tenantID, name))
		if err != nil {
			return "", ErrSealed
		}
		return string(plain), nil
	}
	return "", ErrSealed
}
