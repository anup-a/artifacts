package agents

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrawlerName(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.2; +https://openai.com/gptbot)": "GPTBot",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; OAI-SearchBot/1.0)":                      "OAI-SearchBot",
		"Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)":                                      "ClaudeBot",
		"Mozilla/5.0 (compatible; Claude-User/1.0)":                                                              "Claude-User",
		"Mozilla/5.0 (compatible; PerplexityBot/1.0)":                                                            "PerplexityBot",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15":                   "",
		"byagent/0.6.0 node/22": "",
	} {
		if got := crawlerName(ua); got != want {
			t.Errorf("%q: got %q, want %q", ua, got, want)
		}
	}
}

func TestLogCrawlersLogsBotsOnly(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	h := LogCrawlers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("ok"))
	}))
	for _, c := range []struct{ ua, path string }{
		{"Mozilla/5.0 (Macintosh) Safari/605.1.15", "/"},
		{"Mozilla/5.0 (compatible; GPTBot/1.2)", "/llms.txt"},
		{"Mozilla/5.0 (compatible; ClaudeBot/1.0)", "/missing"},
	} {
		req := httptest.NewRequest("GET", "http://share.test"+c.path, nil)
		req.Header.Set("User-Agent", c.ua)
		req.RemoteAddr = "203.0.113.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	out := buf.String()
	if n := strings.Count(out, "msg=crawler"); n != 2 {
		t.Fatalf("got %d crawler lines, want 2:\n%s", n, out)
	}
	for _, want := range []string{"bot=GPTBot", "path=/llms.txt", "status=200", "bot=ClaudeBot", "path=/missing", "status=404"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "203.0.113.9") || strings.Contains(out, "Safari") {
		t.Errorf("logged an address or a person's request:\n%s", out)
	}
}
