package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Sender delivers magic links. EMAIL_PROVIDER picks the implementation.
type Sender interface {
	SendMagicLink(ctx context.Context, to, url string) error
}

// LogSender writes the link to the log instead of sending mail. It is the
// development and self-host default: the operator reads the link from the
// server log.
type LogSender struct{}

func (LogSender) SendMagicLink(_ context.Context, to, url string) error {
	slog.Info("magic link", "email", to, "magic_link_url", url)
	return nil
}

const resendURL = "https://api.resend.com/emails"

// ResendSender sends the link through resend.com.
type ResendSender struct {
	APIKey string
	From   string
	URL    string       // defaults to the Resend API
	HTTP   *http.Client // defaults to a client with a 10s timeout
}

func (s ResendSender) SendMagicLink(ctx context.Context, to, url string) error {
	return s.send(ctx, to, "Sign in to byagent", "Open this link to sign in. It works once and expires in 15 minutes.\n\n"+url+"\n\nIf you did not ask for this, ignore this email.\n")
}

func (s ResendSender) send(ctx context.Context, to, subject, text string) error {
	body, err := json.Marshal(map[string]any{
		"from":    s.From,
		"to":      []string{to},
		"subject": subject,
		"text":    text,
	})
	if err != nil {
		return err
	}
	endpoint := s.URL
	if endpoint == "" {
		endpoint = resendURL
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("resend: status %d: %s", res.StatusCode, msg)
	}
	return nil
}

// Mailer sends one plain-text email. It is what page access mails through:
// the sign-in codes for a restricted page's visitors and the owner's invites.
// Both senders implement it next to SendMagicLink.
type Mailer interface {
	SendMail(ctx context.Context, to, subject, text string) error
}

// MailerFor picks the Mailer for EMAIL_PROVIDER, or nil when the server sends
// no mail at all.
func MailerFor(provider, resendKey, from string) Mailer {
	switch provider {
	case "log":
		return LogSender{}
	case "resend":
		return ResendSender{APIKey: resendKey, From: from}
	}
	return nil
}

// SendMail logs the message; the operator reads codes from the server log.
func (LogSender) SendMail(_ context.Context, to, subject, text string) error {
	slog.Info("email", "email", to, "subject", subject, "text", text)
	return nil
}

func (s ResendSender) SendMail(ctx context.Context, to, subject, text string) error {
	return s.send(ctx, to, subject, text)
}
