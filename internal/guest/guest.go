// Package guest holds the rules for publishing without an account: the plan
// name, how long a guest workspace lives, what it may publish, and the signed
// link that moves its pages into a real workspace.
package guest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// Plan marks a tenant created by POST /api/guest. Claiming changes it to
// ClaimedPlan, which is also what makes an old claim link inert.
const (
	Plan        = "guest"
	ClaimedPlan = "claimed"
	// KeyName is the name of the one key a guest workspace gets. Claiming
	// revokes it: the claimer makes a real key for their agent.
	KeyName = "guest"
)

// TTL is how long a guest workspace, its key and its pages last. Every page
// expires at the workspace deadline, not 24h after its own publish, so one
// guest key cannot keep content alive by republishing.
const TTL = 24 * time.Hour

// Limits are tighter than a free workspace: enough to try the loop, too
// little to be worth abusing as anonymous hosting.
const (
	Artifacts    = 3
	VersionBytes = 5 << 20
	Files        = 100
	// OpenThreads caps unresolved comment threads on one guest page, since
	// anyone with the link may comment.
	OpenThreads = 25
)

var ErrInvalid = errors.New("invalid claim link")

// ClaimCode signs a guest tenant id. It holds no expiry of its own: the
// tenant's plan and age decide whether the claim still works.
func ClaimCode(secret, tenantID string) string {
	return tenantID + "." + sign(secret, tenantID)
}

// VerifyClaim returns the tenant id a claim code was signed for.
func VerifyClaim(secret, code string) (string, error) {
	if secret == "" || len(code) > 256 {
		return "", ErrInvalid
	}
	id, sig, ok := strings.Cut(code, ".")
	if !ok || id == "" || !hmac.Equal([]byte(sig), []byte(sign(secret, id))) {
		return "", ErrInvalid
	}
	return id, nil
}

func sign(secret, tenantID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("guest-claim:" + tenantID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ExpiresAt is when a guest workspace created at created stops working.
func ExpiresAt(created time.Time) time.Time { return created.Add(TTL).UTC() }
