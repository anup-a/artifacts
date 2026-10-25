// Package pageurl builds the public address of a published page, and reads a
// request's Host back into the page it names.
//
// It is the one place that knows where pages live: under the share origin
// (https://byagent.dev/a/{id}/) when CONTENT_DOMAIN is unset, or on a host of
// their own (https://{slug}.byagent.dev/) when it is. API responses, the CLI
// and MCP output (which echo the API), the dashboard, Open Graph tags,
// gallery, showcase and collection pages and the agent docs all take their
// page links from here, so they agree.
package pageurl

import (
	"net"
	"strconv"
	"strings"

	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/ids"
)

// Builder makes page addresses for one deployment.
type Builder struct {
	// ShareOrigin is the public site, no trailing slash.
	ShareOrigin string
	// Scheme and Domain are the content domain (config.ContentScheme and
	// ContentDomain). Domain empty puts pages under ShareOrigin/a/{id}/.
	Scheme, Domain string
	// Shared is config.ContentShared: Domain is the share host itself, so
	// only exact slug hosts are pages and every other name is Elsewhere.
	Shared bool
}

// New reads the builder's settings from the server config.
func New(c config.Config) Builder {
	return Builder{ShareOrigin: strings.TrimRight(c.ShareOrigin, "/"), Scheme: c.ContentScheme, Domain: c.ContentDomain, Shared: c.ContentShared}
}

// PerPage reports whether every page has an origin of its own.
func (b Builder) PerPage() bool { return b.Domain != "" }

// Origin is a page's own origin, scheme://{slug}.{domain}, or "" when pages
// share the share origin (or the page has no slug, which only a row written
// before migration 0019 and not yet backfilled could).
func (b Builder) Origin(slug string) string {
	if !b.PerPage() || slug == "" {
		return ""
	}
	return b.Scheme + "://" + slug + "." + b.Domain
}

// Page is the address of a page's current version, ending in a slash.
func (b Builder) Page(id, slug string) string {
	if o := b.Origin(slug); o != "" {
		return o + "/"
	}
	return b.ShareOrigin + "/a/" + id + "/"
}

// Version is the pinned address of one version: Page + "v/{n}/".
func (b Builder) Version(id, slug string, n int) string {
	return b.Page(id, slug) + "v/" + strconv.Itoa(n) + "/"
}

// Cover is the page's cover image: Page + "_cover.svg".
func (b Builder) Cover(id, slug string) string { return b.Page(id, slug) + "_cover.svg" }

// Share is the one-click link of a page shared with a code: Page + "?k=".
// Only the digits of the code are kept, the way the gate reads it.
func (b Builder) Share(id, slug, code string) string {
	var digits strings.Builder
	for _, r := range code {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	return b.Page(id, slug) + "?k=" + digits.String()
}

// Form is how a page address looks, for prose and examples:
// "https://<name>.byagent.dev/" or "https://byagent.dev/a/<id>/".
func (b Builder) Form() string {
	if b.PerPage() {
		return b.Scheme + "://<name>." + b.Domain + "/"
	}
	return b.ShareOrigin + "/a/<id>/"
}

// Source is the CSP source that matches every page host
// (https://*.byagent.dev), or "" when pages share the share origin.
func (b Builder) Source() string {
	if !b.PerPage() {
		return ""
	}
	return b.Scheme + "://*." + b.Domain
}

// Kind is what a request's Host is, as far as page hosts go.
type Kind int

const (
	// Elsewhere is any host that is not a page host: byagent's own
	// origins, a custom domain, or a bare address from a health check. With
	// a shared content domain (byagent.dev) that includes the apex, www,
	// app, and every name under it that is not exactly a slug.
	Elsewhere Kind = iota
	// Apex is a separate content domain itself (or its www), which only
	// redirects.
	Apex
	// Page is {slug}.{domain}: one page's own host.
	Page
	// Stray is any other name under a separate content domain, such as a
	// label that is not a slug or a deeper subdomain. It serves nothing.
	Stray
)

// Classify reads a Host header (any case, with or without a port or a
// trailing dot). For a page host it returns the slug.
//
// A page host is exactly one label of slug shape (ids.ValidSlug) directly
// under the content domain. When the content domain is the share host
// itself, nothing else under it is touched: byagent.dev, app.byagent.dev,
// domains.byagent.dev and any other name stay Elsewhere, so they keep their
// own routes (and the apex is never redirected to itself).
func (b Builder) Classify(host string) (Kind, string) {
	if !b.PerPage() {
		return Elsewhere, ""
	}
	host = Hostname(host)
	domain := Hostname(b.Domain)
	if label, ok := strings.CutSuffix(host, "."+domain); ok && ids.ValidSlug(label) {
		return Page, label
	}
	switch {
	case b.Shared:
		return Elsewhere, ""
	case host == domain || host == "www."+domain:
		return Apex, ""
	case !strings.HasSuffix(host, "."+domain):
		return Elsewhere, ""
	}
	return Stray, ""
}

// IsPageOrigin reports whether an Origin header value names a page host.
func (b Builder) IsPageOrigin(origin string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	_, rest, ok := strings.Cut(origin, "://")
	if !ok {
		return false
	}
	k, _ := b.Classify(rest)
	return k == Page
}

// Hostname is a Host header or host[:port] as a bare, lower-case name.
func Hostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
