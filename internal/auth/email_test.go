package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogSenderLogsTheLink(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	err := LogSender{}.SendMagicLink(context.Background(), "a@x.io", "http://api.test/app/login/email/verify?token=abc", "en")
	if err != nil {
		t.Fatal(err)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	if line["magic_link_url"] != "http://api.test/app/login/email/verify?token=abc" || line["email"] != "a@x.io" || line["level"] != "INFO" {
		t.Fatalf("log line %s", buf.String())
	}
}

func TestResendSenderPostsEmail(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(200)
		w.Write([]byte(`{"id":"em_1"}`))
	}))
	defer srv.Close()
	s := ResendSender{APIKey: "re_test", From: "Artifacts <login@x.io>", URL: srv.URL}
	if err := s.SendMagicLink(context.Background(), "a@x.io", "http://api.test/v?token=abc", "en"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer re_test" || got["from"] != "Artifacts <login@x.io>" {
		t.Fatalf("auth=%q body=%v", auth, got)
	}
	to, _ := got["to"].([]any)
	if len(to) != 1 || to[0] != "a@x.io" || !strings.Contains(got["text"].(string), "http://api.test/v?token=abc") {
		t.Fatalf("body %v", got)
	}
}

func TestResendSenderReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte(`{"message":"bad from"}`))
	}))
	defer srv.Close()
	s := ResendSender{APIKey: "k", From: "x", URL: srv.URL}
	err := s.SendMagicLink(context.Background(), "a@x.io", "u", "en")
	if err == nil || !strings.Contains(err.Error(), "422") {
		t.Fatal(err)
	}
}
