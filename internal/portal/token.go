// Package portal signs stateless, read-only end-user portal capabilities.
package portal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrInvalid = errors.New("invalid or expired portal link")

type Claims struct {
	TenantID, EndUserID string
	ExpiresAt           time.Time
}

func Sign(secret, tenantID, endUserID string, expires time.Time) string {
	payload := tenantID + "." + endUserID + "." + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "." + hex.EncodeToString(mac.Sum(nil))))
}

// Verify authenticates the complete payload before returning any database ids.
func Verify(secret, token string, now time.Time) (Claims, error) {
	if secret == "" || len(token) > 512 {
		return Claims{}, ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 4 {
		return Claims{}, ErrInvalid
	}
	sig, err := hex.DecodeString(parts[3])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.Join(parts[:3], ".")))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, ErrInvalid
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() >= exp || parts[0] == "" || parts[1] == "" {
		return Claims{}, ErrInvalid
	}
	return Claims{parts[0], parts[1], time.Unix(exp, 0).UTC()}, nil
}
