package agents

import (
	"log/slog"
	"net/http"
	"strings"
)

// crawlers are the user agent tokens of AI crawlers and of the fetchers AI
// assistants use when a person asks them about a page, matched case
// insensitively anywhere in the User-Agent header. The order matters only
// where one token contains another: the more specific comes first.
var crawlers = []string{
	"OAI-SearchBot", "ChatGPT-User", "GPTBot",
	"Claude-SearchBot", "Claude-User", "ClaudeBot", "anthropic-ai",
	"Perplexity-User", "PerplexityBot",
	"Google-Extended", "Google-CloudVertexBot", "GoogleOther",
	"Applebot-Extended", "Applebot",
	"meta-externalagent", "meta-externalfetcher", "FacebookBot",
	"Amazonbot", "Bytespider", "CCBot", "cohere-ai", "DuckAssistBot",
	"MistralAI-User", "YouBot", "PhindBot", "Diffbot", "xAI-Bot", "Grok",
	"bingbot", "Googlebot",
}

// crawlerName returns the matching token from crawlers, or "".
func crawlerName(ua string) string {
	lower := strings.ToLower(ua)
	for _, c := range crawlers {
		if strings.Contains(lower, strings.ToLower(c)) {
			return c
		}
	}
	return ""
}

// LogCrawlers writes one log line per request from a known AI crawler or
// search bot: its name, the method, path and status. That is how we learn
// which pages answer engines read, since they run no analytics script.
// People's requests are not logged, and neither is any address.
func LogCrawlers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := crawlerName(r.UserAgent())
		if name == "" {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		slog.Info("crawler", "bot", name, "method", r.Method, "host", r.Host, "path", r.URL.Path, "status", sw.status)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
