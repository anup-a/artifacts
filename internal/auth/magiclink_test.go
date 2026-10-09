package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

type captureSender struct{ to, url, lang string }

func (c *captureSender) SendMagicLink(_ context.Context, to, u, lang string) error {
	c.to, c.url, c.lang = to, u, lang
	return nil
}

func magicFor(t *testing.T) (*MagicLinks, *captureSender, *clock) {
	t.Helper()
	st := newStore(t)
	c := &clock{t: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	snd := &captureSender{}
	return &MagicLinks{Store: st, Now: c.now, Send: snd, Origin: "http://api.test"}, snd, c
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("token")
}

func TestMagicLinkRoundTrip(t *testing.T) {
	m, snd, _ := magicFor(t)
	if err := m.Issue(context.Background(), " Person@X.io ", "ja"); err != nil {
		t.Fatal(err)
	}
	if snd.to != "person@x.io" || !strings.HasPrefix(snd.url, "http://api.test/app/login/email/verify?token=") || snd.lang != "ja" {
		t.Fatalf("sent to=%q url=%q lang=%q", snd.to, snd.url, snd.lang)
	}
	tok := tokenOf(t, snd.url)
	if len(tok) != 43 {
		t.Fatalf("token %q", tok)
	}
	email, err := m.Verify(context.Background(), tok)
	if err != nil || email != "person@x.io" {
		t.Fatalf("%q %v", email, err)
	}
	// Single use.
	if _, err := m.Verify(context.Background(), tok); !errors.Is(err, ErrBadLink) {
		t.Fatalf("second use: %v", err)
	}
}

func TestMagicLinkExpiresAndRejectsGarbage(t *testing.T) {
	m, snd, c := magicFor(t)
	if err := m.Issue(context.Background(), "a@x.io", "en"); err != nil {
		t.Fatal(err)
	}
	tok := tokenOf(t, snd.url)
	c.t = c.t.Add(MagicLinkTTL + time.Second)
	if _, err := m.Verify(context.Background(), tok); !errors.Is(err, ErrBadLink) {
		t.Fatal(err)
	}
	if _, err := m.Verify(context.Background(), ""); !errors.Is(err, ErrBadLink) {
		t.Fatal(err)
	}
	if _, err := m.Verify(context.Background(), "not-a-token"); !errors.Is(err, ErrBadLink) {
		t.Fatal(err)
	}
}

func TestMagicLinkRejectsBadAddress(t *testing.T) {
	m, snd, _ := magicFor(t)
	for _, bad := range []string{"", "nope", "a@", "@x.io", "a b@x.io", strings.Repeat("a", 250) + "@x.io"} {
		if err := m.Issue(context.Background(), bad, "en"); !errors.Is(err, ErrBadEmail) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if snd.url != "" {
		t.Fatal("sent a link for a bad address")
	}
}
