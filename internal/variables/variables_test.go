package variables

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func newKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestSealRoundTripAndBinding(t *testing.T) {
	s, err := NewSealer(newKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal("t_a", "OPENAI_KEY", "sk-secret-value-123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "sk-secret") {
		t.Fatal("sealed text carries the plaintext")
	}
	got, err := s.Open("t_a", "OPENAI_KEY", sealed)
	if err != nil || got != "sk-secret-value-123" {
		t.Fatalf("open: %q %v", got, err)
	}
	// A sealed value copied to another workspace or another name must not open.
	if _, err := s.Open("t_b", "OPENAI_KEY", sealed); err != ErrSealed {
		t.Fatalf("other tenant opened it: %v", err)
	}
	if _, err := s.Open("t_a", "OTHER_KEY", sealed); err != ErrSealed {
		t.Fatalf("other name opened it: %v", err)
	}
	// Tampering is detected.
	id, body, _ := strings.Cut(sealed, ":")
	raw, _ := base64.RawStdEncoding.DecodeString(body)
	raw[len(raw)-1] ^= 1
	if _, err := s.Open("t_a", "OPENAI_KEY", id+":"+base64.RawStdEncoding.EncodeToString(raw)); err != ErrSealed {
		t.Fatalf("tampered value opened: %v", err)
	}
	// Two seals of one value differ (fresh nonce).
	again, _ := s.Seal("t_a", "OPENAI_KEY", "sk-secret-value-123")
	if again == sealed {
		t.Fatal("nonce reused")
	}
}

func TestSealerRotation(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	old, _ := NewSealer(oldKey)
	sealed, _ := old.Seal("t", "K", "value-under-old-key")
	both, err := NewSealer(newK + "," + oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := both.Open("t", "K", sealed); err != nil || v != "value-under-old-key" {
		t.Fatalf("rotation lost the old value: %v", err)
	}
	fresh, _ := both.Seal("t", "K", "x-value-new")
	if _, err := old.Open("t", "K", fresh); err != ErrSealed {
		t.Fatal("new writes must use the first key")
	}
	onlyNew, _ := NewSealer(newK)
	if _, err := onlyNew.Open("t", "K", sealed); err != ErrSealed {
		t.Fatal("dropped key still opens")
	}
}

func TestNewSealerRejectsWeakKeys(t *testing.T) {
	for _, k := range []string{"", "short", base64.StdEncoding.EncodeToString(make([]byte, 16)), "not base64 at all!!", " , "} {
		if _, err := NewSealer(k); err == nil {
			t.Errorf("accepted %q", k)
		}
	}
	b := make([]byte, 32)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawURLEncoding} {
		if _, err := NewSealer(enc.EncodeToString(b)); err != nil {
			t.Errorf("rejected a valid key: %v", err)
		}
	}
}

func TestNamesAndValues(t *testing.T) {
	for _, n := range []string{"A", "OPENROUTER_API_KEY", "K2"} {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range []string{"", "lower", "1KEY", "_KEY", "KEY-1", "KEY NAME", strings.Repeat("A", 65), "KEY}}"} {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
	for _, v := range []string{"", "a\r\nX-Evil: 1", "tab\tin", "nul\x00", strings.Repeat("x", MaxValueBytes+1)} {
		if ValidValue(v) == nil {
			t.Errorf("value %q should be refused", v)
		}
	}
	if err := ValidValue("sk-or-v1-abc 123+/="); err != nil {
		t.Error(err)
	}
}

func TestNormalizeHost(t *testing.T) {
	ok := map[string]string{
		"api.openai.com":                      "api.openai.com",
		"API.OpenAI.com.":                     "api.openai.com",
		"https://openrouter.ai/api/v1":        "openrouter.ai",
		"https://xyz.supabase.co:443/rest/v1": "xyz.supabase.co",
	}
	for in, want := range ok {
		got, err := NormalizeHost(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "localhost", "intranet", "127.0.0.1", "169.254.169.254", "[::1]", "::1",
		"https://10.0.0.1/x", "https://api.example.com:8443", "*.example.com", "exa mple.com", "-bad.example.com",
		"http://user@", "example.com/path"} {
		if got, err := NormalizeHost(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	if _, err := NormalizeHosts(nil); err == nil {
		t.Error("an empty allowlist must be refused")
	}
	hosts, err := NormalizeHosts([]string{"a.example.com", "A.example.com", "https://b.example.com/"})
	if err != nil || len(hosts) != 2 {
		t.Errorf("dedupe: %v %v", hosts, err)
	}
}

func TestHostAllowed(t *testing.T) {
	allowed := []string{"api.openai.com", "xyz.supabase.co"}
	for _, h := range []string{"api.openai.com", "API.OPENAI.COM", "eu.api.openai.com", "xyz.supabase.co", "xyz.supabase.co."} {
		if !HostAllowed(h, allowed) {
			t.Errorf("%q should be allowed", h)
		}
	}
	for _, h := range []string{"evilapi.openai.com", "openai.com", "api.openai.com.evil.test", "abc.supabase.co", "xyz.supabase.co.evil.test", ""} {
		if HostAllowed(h, allowed) {
			t.Errorf("%q should be refused", h)
		}
	}
	// A shared suffix that slipped into a list admits nothing.
	for _, h := range []string{"attacker.supabase.co", "x.workers.dev", "bucket.s3.amazonaws.com"} {
		if HostAllowed(h, []string{"supabase.co", "workers.dev", "amazonaws.com"}) {
			t.Errorf("%q admitted through a public suffix", h)
		}
	}
}

func TestPublicSuffixesCannotBeAllowlisted(t *testing.T) {
	for _, h := range []string{"supabase.co", "workers.dev", "amazonaws.com", "s3.amazonaws.com", "co.uk", "github.io", "vercel.app", "com"} {
		if _, err := NormalizeHost(h); err == nil {
			t.Errorf("%q accepted", h)
		}
	}
	for _, h := range []string{"xyz.supabase.co", "my-worker.me.workers.dev", "api.openai.com", "openrouter.ai", "bbc.co.uk"} {
		if _, err := NormalizeHost(h); err != nil {
			t.Errorf("%q refused: %v", h, err)
		}
	}
}
