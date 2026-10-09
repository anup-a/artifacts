package auth

import (
	"context"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

// MagicLinkTTL is how long a sign-in link stays valid.
const MagicLinkTTL = 15 * time.Minute

const maxEmailLen = 254

var (
	// ErrBadLink means the token is unknown, used or expired.
	ErrBadLink = errors.New("invalid or expired sign-in link")
	// ErrBadEmail means the address cannot receive a link.
	ErrBadEmail = errors.New("enter a valid email address")
)

// MagicLinks issues single-use, short-lived email sign-in links.
type MagicLinks struct {
	Store  store.AuthStore
	Now    func() time.Time
	Send   Sender
	Origin string // PUBLIC_API_ORIGIN, where the verify route lives
}

// NormalizeEmail lowercases and trims an address, or returns ErrBadEmail.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxEmailLen || strings.ContainsAny(email, " <>\r\n") {
		return "", ErrBadEmail
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", ErrBadEmail
	}
	return email, nil
}

// Issue creates a link for email and hands it to the sender, to be written
// in lang (an i18n locale code; English when unknown). Only the token's hash
// is stored.
func (m *MagicLinks) Issue(ctx context.Context, rawEmail, lang string) error {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	token := ids.NewToken()
	now := m.Now()
	link := store.MagicLink{TokenSHA256: ids.HashKey(token), Email: email, CreatedAt: now, ExpiresAt: now.Add(MagicLinkTTL)}
	if err := m.Store.CreateMagicLink(ctx, link); err != nil {
		return err
	}
	return m.Send.SendMagicLink(ctx, email, m.Origin+"/app/login/email/verify?token="+url.QueryEscape(token), lang)
}

// Verify consumes a token and returns the address it was issued for.
func (m *MagicLinks) Verify(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrBadLink
	}
	link, err := m.Store.ConsumeMagicLink(ctx, ids.HashKey(token), m.Now())
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrBadLink
	}
	if err != nil {
		return "", err
	}
	return link.Email, nil
}
