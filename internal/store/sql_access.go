package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Access is the door on an artifact besides its owner's: a password, or an
// allowlist of addresses and "@domain" entries checked by email.
type Access struct {
	// Mode is "", "password" or "restricted".
	Mode string
	// PasswordHash is set with Mode "password" and empty otherwise.
	PasswordHash string
	// Allow is the restricted allowlist, empty otherwise.
	Allow []string
}

// AccessCode is a one-time code and link mailed to a restricted page's
// visitor. Both are stored as hashes: the link's token as SHA-256, the code
// as a keyed HMAC so a leaked row cannot be walked back to six digits.
type AccessCode struct {
	TokenSHA256   string
	ArtifactID    string
	Email         string
	CodeSHA256    string
	AccessVersion int
	Attempts      int
	CreatedAt     time.Time
	ExpiresAt     time.Time
	ConsumedAt    *time.Time
}

// AccessStore keeps password and allowlist access and the codes that prove a
// restricted page's visitor owns an address.
type AccessStore interface {
	// SetAccess replaces an artifact's door. Mode "password" or "restricted"
	// also makes it private and withdraws its share code, in the same
	// statement, so there is never a moment with two doors or an open one.
	// Mode "" clears the password and allowlist and leaves visibility alone.
	// access_version moves whenever the mode or the password hash changes,
	// which signs out every visitor holding a grant; an allowlist-only change
	// does not, since grants are checked against the live list anyway.
	SetAccess(ctx context.Context, id string, acc Access) error
	// CreateAccessCode stores a code, spending any earlier unused code for the
	// same artifact and address, and drops long-expired rows.
	CreateAccessCode(ctx context.Context, c AccessCode) error
	// AccessCodeByToken reads a code by its link token's hash.
	AccessCodeByToken(ctx context.Context, tokenSHA256 string) (AccessCode, error)
	// LiveAccessCode is the unexpired, unused code for an artifact and address.
	LiveAccessCode(ctx context.Context, artifactID, email string, now time.Time) (AccessCode, error)
	// SpendAccessAttempt counts one guess against a live code. ErrNotFound
	// means the code is gone or out of guesses and must not be checked.
	SpendAccessAttempt(ctx context.Context, tokenSHA256 string, now time.Time, max int) error
	// ConsumeAccessCode marks a live code used. ErrNotFound means it was
	// already used or has expired, so only one caller ever wins it.
	ConsumeAccessCode(ctx context.Context, tokenSHA256 string, now time.Time) error
}

func encodeAllow(allow []string) (string, error) {
	if len(allow) == 0 {
		return "", nil
	}
	b, err := json.Marshal(allow)
	return string(b), err
}

func decodeAllow(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func (s *sqlStore) SetAccess(ctx context.Context, id string, acc Access) error {
	switch acc.Mode {
	case "":
		acc.PasswordHash, acc.Allow = "", nil
	case "password":
		acc.Allow = nil
	case "restricted":
		acc.PasswordHash = ""
	default:
		return errInvalidAccess
	}
	allow, err := encodeAllow(acc.Allow)
	if err != nil {
		return err
	}
	// The private switch and the share code only move for a real door; mode
	// "" leaves both to the caller, who decides between public and private.
	door := 0
	if acc.Mode != "" {
		door = 1
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE artifacts SET
 access_version=access_version+(CASE WHEN access_mode<>? OR access_password<>? THEN 1 ELSE 0 END),
 access_mode=?, access_password=?, access_allow=?,
 visibility=(CASE WHEN ?=1 THEN 'private' ELSE visibility END),
 share_code=(CASE WHEN ?=1 THEN '' ELSE share_code END),
 share_code_expires_at=(CASE WHEN ?=1 THEN NULL ELSE share_code_expires_at END)
 WHERE id=? AND deleted_at IS NULL`),
		acc.Mode, acc.PasswordHash, acc.Mode, acc.PasswordHash, allow, door, door, door, id)
	return affected(res, err)
}

type accessError string

func (e accessError) Error() string { return string(e) }

const errInvalidAccess = accessError("access mode must be empty, password or restricted")

func scanAccessCode(row interface{ Scan(...any) error }) (AccessCode, error) {
	var c AccessCode
	err := row.Scan(&c.TokenSHA256, &c.ArtifactID, &c.Email, &c.CodeSHA256, &c.AccessVersion, &c.Attempts, &c.CreatedAt, &c.ExpiresAt, &c.ConsumedAt)
	return c, notFound(err)
}

const accessCodeCols = `token_sha256,artifact_id,email,code_sha256,access_version,attempts,created_at,expires_at,consumed_at`

func (s *sqlStore) CreateAccessCode(ctx context.Context, c AccessCode) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := c.CreatedAt.UTC()
	// A day past expiry nothing reads a row any more; keep the table small.
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM access_codes WHERE expires_at<?`), now.Add(-24*time.Hour)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE access_codes SET consumed_at=? WHERE artifact_id=? AND email=? AND consumed_at IS NULL`), now, c.ArtifactID, c.Email); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO access_codes(`+accessCodeCols+`) VALUES (?,?,?,?,?,0,?,?,NULL)`),
		c.TokenSHA256, c.ArtifactID, c.Email, c.CodeSHA256, c.AccessVersion, now, c.ExpiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) AccessCodeByToken(ctx context.Context, token string) (AccessCode, error) {
	return scanAccessCode(s.db.QueryRowContext(ctx, s.q(`SELECT `+accessCodeCols+` FROM access_codes WHERE token_sha256=?`), token))
}

func (s *sqlStore) LiveAccessCode(ctx context.Context, artifactID, email string, now time.Time) (AccessCode, error) {
	return scanAccessCode(s.db.QueryRowContext(ctx, s.q(`SELECT `+accessCodeCols+` FROM access_codes WHERE artifact_id=? AND email=? AND consumed_at IS NULL AND expires_at>? ORDER BY created_at DESC LIMIT 1`), artifactID, email, now.UTC()))
}

func (s *sqlStore) SpendAccessAttempt(ctx context.Context, token string, now time.Time, max int) error {
	return affected(s.db.ExecContext(ctx, s.q(`UPDATE access_codes SET attempts=attempts+1 WHERE token_sha256=? AND consumed_at IS NULL AND expires_at>? AND attempts<?`), token, now.UTC(), max))
}

func (s *sqlStore) ConsumeAccessCode(ctx context.Context, token string, now time.Time) error {
	return affected(s.db.ExecContext(ctx, s.q(`UPDATE access_codes SET consumed_at=? WHERE token_sha256=? AND consumed_at IS NULL AND expires_at>?`), now.UTC(), token, now.UTC()))
}
