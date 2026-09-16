package portal

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestTokenLifetimeAndTampering(t *testing.T) {
	now := time.Unix(1800000000, 0)
	raw := Sign("test-only-secret", "t_abcdefghijkl", "eu_abcdefghijkl", now.Add(7*24*time.Hour))
	c, err := Verify("test-only-secret", raw, now)
	if err != nil || c.TenantID != "t_abcdefghijkl" || c.EndUserID != "eu_abcdefghijkl" || !c.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("claims: %v %+v", err, c)
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(raw)
	tampered := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(decoded), "t_abcdefghijkl", "t_anotherowner", 1)))
	for _, tc := range []struct {
		secret, raw string
		now         time.Time
	}{
		{"wrong", raw, now}, {"", raw, now}, {"test-only-secret", tampered, now}, {"test-only-secret", "bad!", now},
		{"test-only-secret", raw, now.Add(7 * 24 * time.Hour)}, {"test-only-secret", base64.RawURLEncoding.EncodeToString([]byte("a.b.c.d")), now},
	} {
		if _, err := Verify(tc.secret, tc.raw, tc.now); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
