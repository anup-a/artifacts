package usage

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

// Direct is the referrer of a view that arrived with none: a typed or pasted
// link, a bookmark, or an app that does not send one.
const Direct = "(direct)"

// PreviewParam on a page address marks a request from the dashboard's own
// preview frame. Such views are the owner's, so they are not counted.
const PreviewParam = "byagent_preview"

// Record counts r as a view of the page at path within artifactID when it
// is one: a GET for an HTML document by a browser, not a bot, a prefetch or
// the dashboard preview. Call it only once the page has actually been served
// (200 or 304), after any gate, and only for HTML documents; serve does,
// and a handler serving an artifact on another host should do the same.
// The artifact id is what views are filed under, whatever the host.
func (c *Counter) Record(r *http.Request, artifactID, path string) {
	if !Countable(r) {
		return
	}
	ua := r.UserAgent()
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	c.Add(Hit{
		ArtifactID: artifactID,
		Path:       pagePath(path),
		Referrer:   referrer(r, artifactID),
		Country:    country(r, c.TrustProxy),
		Device:     device(ua),
		Visitor:    clientIP(r, c.TrustProxy) + "\n" + ua,
	}, now())
}

// Countable reports whether a request for a page is a person looking at it.
// A browser navigating to a page sends GET with text/html in Accept and a
// user agent starting with Mozilla/; scripts, link unfurlers and crawlers
// mostly do not, and the ones that do name themselves.
func Countable(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Query().Has(PreviewParam) {
		return false
	}
	for _, h := range []string{"Sec-Purpose", "Purpose", "X-Purpose", "X-Moz"} {
		if v := strings.ToLower(r.Header.Get(h)); strings.Contains(v, "prefetch") || strings.Contains(v, "preview") {
			return false
		}
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	return !IsBot(r.UserAgent())
}

// botTokens are lowercased fragments of the user agents of crawlers, link
// previewers, monitors, headless browsers and HTTP libraries.
var botTokens = []string{
	"bot", "crawl", "spider", "slurp", "archiver", "ia_archiver", "facebookexternalhit", "facebookcatalog",
	"meta-externalagent", "embedly", "quora link preview", "pinterest", "vkshare", "w3c_validator", "whatsapp",
	"skypeuripreview", "bingpreview", "google-inspectiontool", "googleother", "google-read-aloud", "feedfetcher",
	"mediapartners", "lighthouse", "pagespeed", "headless", "phantomjs", "puppeteer", "playwright", "selenium",
	"preview", "prerender", "curl", "wget", "httpie", "python", "go-http-client", "java/", "okhttp", "axios",
	"node-fetch", "undici", "libwww", "scrapy", "httpclient", "ruby", "php/", "perl", "postman", "insomnia",
	"uptime", "monitor", "pingdom", "statuscake", "datadog", "newrelic", "checkly", "gptbot", "chatgpt",
	"oai-searchbot", "claude", "anthropic", "perplexity", "bytespider", "ccbot", "amazonbot", "applebot",
	"yandex", "baidu", "duckduck", "petalbot", "ahrefs", "semrush", "mj12", "dotbot", "cohere", "diffbot",
}

// IsBot reports whether a user agent is not a person's browser.
func IsBot(ua string) bool {
	ua = strings.ToLower(ua)
	if !strings.HasPrefix(ua, "mozilla/") {
		return true
	}
	for _, t := range botTokens {
		if strings.Contains(ua, t) {
			return true
		}
	}
	return false
}

// device sorts a browser's user agent into a coarse class. Nothing finer is
// kept: no browser, version or operating system.
func device(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "ipad") || strings.Contains(ua, "tablet") || (strings.Contains(ua, "android") && !strings.Contains(ua, "mobile")):
		return "tablet"
	case strings.Contains(ua, "mobi") || strings.Contains(ua, "iphone") || strings.Contains(ua, "android"):
		return "mobile"
	}
	return "desktop"
}

// pagePath is how a file shows in the paths list: "/" for the index, the
// directory for a nested index, and the file path otherwise.
func pagePath(p string) string {
	p = strings.TrimSuffix(p, "index.html")
	if len(p) > 300 {
		p = p[:300]
	}
	return "/" + p
}

// referrer reduces the Referer header to a host, which is all a browser
// sends across sites by default anyway. It answers "" for a link from
// elsewhere in the same page, which is navigation rather than a source.
func referrer(r *http.Request, artifactID string) string {
	raw := r.Referer()
	if raw == "" {
		return Direct
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Direct
	}
	if strings.EqualFold(u.Host, r.Host) {
		// On the share host a page lives under /a/{id}/; on a host of its
		// own, everything on it is the page.
		if strings.HasPrefix(u.Path, "/a/"+artifactID+"/") || !strings.HasPrefix(r.URL.Path, "/a/") {
			return ""
		}
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if !validHost(host) {
		return store.FacetOther
	}
	return host
}

// validHost keeps referrers to plain DNS names and IPv4 addresses, so a
// made-up Referer cannot put markup or terminal escapes in a report.
func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, c := range h {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// country is the visitor's country code as Cloudflare reports it, only when
// the server sits behind a proxy that sets the header; anything else could
// be sent by the visitor. Unknown (XX) and Tor (T1) count as no country.
func country(r *http.Request, trustProxy bool) string {
	if !trustProxy {
		return ""
	}
	c := r.Header.Get("CF-IPCountry")
	if len(c) != 2 || c == "XX" || c == "T1" || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return ""
	}
	return c
}

// clientIP matches the API's rate-limit bucketing: proxy headers only when
// they are trusted, else the transport peer.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := r.Header.Get("CF-Connecting-IP"); v != "" {
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			return strings.TrimSpace(strings.Split(v, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
