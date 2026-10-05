// Package config reads server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/anup-a/artifacts/internal/variables"
)

type Limits struct {
	VersionBytes       int64
	Files              int
	VersionsKept       int
	PublishPerMin      int
	ArtifactsPerTenant int
	CommentBytes       int
	CommentsPerMin     int
	AnchorBytes        int
	// AccessMailPerWorkspaceDay and AccessMailPerDay cap the sign-in codes a
	// restricted page's gate mails: per workspace, and for the whole server.
	AccessMailPerWorkspaceDay int
	AccessMailPerDay          int
	// Proxy route calls an hour, per client address across every page and
	// per page across every address.
	ProxyPerIPHour   int
	ProxyPerPageHour int
}

type Config struct {
	Addr               string
	DB                 string // sqlite | postgres
	DatabaseURL        string
	Storage            string // local | s3
	DataDir            string
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3UseSSL           bool
	AdminToken         string
	KeySecret          string
	ShareOrigin        string
	APIOrigin          string
	GitHubClientID     string
	GitHubClientSecret string
	EmailProvider      string
	ResendAPIKey       string
	EmailFrom          string
	// GoogleAnalyticsID is the GA4 Measurement ID for first-party app
	// surfaces (landing, login, /app/*). Empty disables the gtag snippet
	// and keeps CSP free of Google Analytics hosts. Prefer
	// GOOGLE_ANALYTICS_ID; GA_MEASUREMENT_ID is accepted as an alias.
	GoogleAnalyticsID string
	// TrustProxyHeaders makes rate limiting read CF-Connecting-IP /
	// X-Forwarded-For. Enable it only when the server is reachable exclusively
	// through a proxy that overwrites those headers; otherwise a client can
	// spoof them and escape per-IP limits.
	TrustProxyHeaders bool
	// GuestPublish turns on POST /api/guest: a 24-hour key for publishing
	// without an account, claimable from the dashboard. It needs web sign-in,
	// since claiming is how a guest keeps their pages. GUEST_PUBLISH=on.
	GuestPublish bool
	// ReadOnly answers every write with 503 and pauses GC, so a snapshot
	// taken while it is on is the last word: a server move loses nothing.
	// READ_ONLY=on.
	ReadOnly bool
	// ShowcaseSlug is the share slug of the collection /showcase draws its
	// pages from. Empty leaves /showcase with nothing to show yet.
	// SHOWCASE_COLLECTION=<slug>.
	ShowcaseSlug string
	// ShowcasePages lists the ids of the pages /showcase shows, in order.
	// Set, it wins over ShowcaseSlug, so pages can be picked from any
	// project without relabelling them. SHOWCASE_PAGES=id,id,...
	ShowcasePages []string
	// VariablesKey seals account variables (the secrets proxy routes send
	// upstream) at rest: comma-separated base64 32-byte AES keys, the first
	// for new writes, the rest still read during a rotation. Empty turns
	// variables and proxy routes off. VARIABLES=off turns them off with the
	// key still set. VARIABLES_KEY.
	VariablesKey string
	VariablesOff bool
	// ContentDomain gives every published page a browser origin of its own,
	// https://{slug}.{ContentDomain}/, the way GitHub Pages and here.now do,
	// so one page's script cannot read or act on another page. It is either
	// the share origin's own host (byagent.dev: pages at {slug}.byagent.dev,
	// ContentShared) or a registrable domain apart from the share and API
	// origins (byagent.page). Empty keeps every page under
	// PUBLIC_SHARE_ORIGIN/a/{id}/ as before.
	// CONTENT_DOMAIN=byagent.dev, or a whole origin such as
	// http://page.localhost:8080 to set the scheme and port for local work.
	// Host[:port], lower case, no scheme.
	ContentDomain string
	// ContentScheme is http or https for page hosts: the scheme
	// CONTENT_DOMAIN named, else the share origin's.
	ContentScheme string
	// ContentShared is set when ContentDomain is the share origin's own
	// host: page hosts are {slug}.byagent.dev beside byagent.dev and
	// app.byagent.dev. Only a name that is exactly one slug label is a page
	// host then; the apex and every other name keep their normal routes.
	ContentShared bool
	Limits        Limits
}

// VariablesEnabled reports whether account variables and proxy routes run.
func (c Config) VariablesEnabled() bool { return c.VariablesKey != "" && !c.VariablesOff }

// WebEnabled reports whether at least one sign-in provider is configured.
func (c Config) WebEnabled() bool { return c.GitHubClientID != "" || c.EmailProvider != "" }

func FromEnv(getenv func(string) string) (Config, error) {
	get := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	gaID := getenv("GOOGLE_ANALYTICS_ID")
	if gaID == "" {
		gaID = getenv("GA_MEASUREMENT_ID")
	}
	c := Config{
		Addr:               get("ADDR", ":8080"),
		DB:                 get("DB", "sqlite"),
		DatabaseURL:        getenv("DATABASE_URL"),
		Storage:            get("STORAGE", "local"),
		DataDir:            get("DATA_DIR", "./data"),
		S3Endpoint:         getenv("S3_ENDPOINT"),
		S3Bucket:           getenv("S3_BUCKET"),
		S3AccessKey:        getenv("S3_ACCESS_KEY"),
		S3SecretKey:        getenv("S3_SECRET_KEY"),
		S3UseSSL:           get("S3_USE_SSL", "true") == "true",
		AdminToken:         getenv("ADMIN_TOKEN"),
		KeySecret:          getenv("KEY_SIGNING_SECRET"),
		ShareOrigin:        get("PUBLIC_SHARE_ORIGIN", "http://localhost:8080"),
		APIOrigin:          get("PUBLIC_API_ORIGIN", "http://localhost:8080"),
		GitHubClientID:     getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: getenv("GITHUB_CLIENT_SECRET"),
		EmailProvider:      getenv("EMAIL_PROVIDER"),
		ResendAPIKey:       getenv("RESEND_API_KEY"),
		EmailFrom:          getenv("EMAIL_FROM"),
		GoogleAnalyticsID:  gaID,

		TrustProxyHeaders: get("TRUST_PROXY_HEADERS", "false") == "true",
		GuestPublish:      getenv("GUEST_PUBLISH") == "on",
		ReadOnly:          getenv("READ_ONLY") == "on",
		ShowcaseSlug:      getenv("SHOWCASE_COLLECTION"),
		ShowcasePages:     splitList(getenv("SHOWCASE_PAGES")),
		VariablesKey:      getenv("VARIABLES_KEY"),
		VariablesOff:      getenv("VARIABLES") == "off",
	}
	var err error
	if c.ContentScheme, c.ContentDomain, c.ContentShared, err = contentDomain(getenv("CONTENT_DOMAIN"), c.ShareOrigin, c.APIOrigin); err != nil {
		return c, err
	}
	if c.Limits, err = limitsFromEnv(getenv); err != nil {
		return c, err
	}
	switch c.DB {
	case "sqlite":
	case "postgres":
		if c.DatabaseURL == "" {
			return c, errors.New("DB=postgres requires DATABASE_URL")
		}
	default:
		return c, fmt.Errorf("DB must be sqlite or postgres, got %q", c.DB)
	}
	switch c.Storage {
	case "local":
	case "s3":
		if c.S3Endpoint == "" || c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "" {
			return c, errors.New("STORAGE=s3 requires S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
		}
	default:
		return c, fmt.Errorf("STORAGE must be local or s3, got %q", c.Storage)
	}
	if (c.GitHubClientID == "") != (c.GitHubClientSecret == "") {
		return c, errors.New("GitHub sign-in requires GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET")
	}
	switch c.EmailProvider {
	case "", "log":
	case "resend":
		if c.ResendAPIKey == "" || c.EmailFrom == "" {
			return c, errors.New("EMAIL_PROVIDER=resend requires RESEND_API_KEY and EMAIL_FROM")
		}
	default:
		return c, errors.New("EMAIL_PROVIDER must be log or resend, or unset to disable email sign-in")
	}
	if c.WebEnabled() && c.KeySecret == "" {
		return c, errors.New("web sign-in requires KEY_SIGNING_SECRET, including when ADMIN_TOKEN is set")
	}
	if c.GuestPublish && !c.WebEnabled() {
		return c, errors.New("GUEST_PUBLISH=on requires web sign-in, so guests can claim their pages")
	}
	if c.VariablesKey != "" {
		if _, err := variables.NewSealer(c.VariablesKey); err != nil {
			return c, fmt.Errorf("VARIABLES_KEY: %w", err)
		}
	}
	if c.AdminToken == "" && c.KeySecret == "" {
		return c, errors.New("set ADMIN_TOKEN (self-host) or KEY_SIGNING_SECRET (managed)")
	}
	return c, nil
}

func limitsFromEnv(getenv func(string) string) (Limits, error) {
	l := Limits{VersionBytes: 26214400, Files: 500, VersionsKept: 20, PublishPerMin: 10,
		ArtifactsPerTenant: 200, CommentBytes: 2048, CommentsPerMin: 20, AnchorBytes: 4096,
		AccessMailPerWorkspaceDay: 200, AccessMailPerDay: 2000,
		ProxyPerIPHour: 100, ProxyPerPageHour: 1000}
	set := func(k string, dst *int) error {
		v := getenv(k)
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("%s must be a positive integer, got %q", k, v)
		}
		*dst = n
		return nil
	}
	if v := getenv("LIMIT_VERSION_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return l, fmt.Errorf("LIMIT_VERSION_BYTES must be a positive integer, got %q", v)
		}
		l.VersionBytes = n
	}
	for k, dst := range map[string]*int{
		"LIMIT_FILES": &l.Files, "LIMIT_VERSIONS_KEPT": &l.VersionsKept,
		"LIMIT_PUBLISH_PER_MIN": &l.PublishPerMin,
		"LIMIT_COMMENT_BYTES":   &l.CommentBytes, "LIMIT_COMMENTS_PER_MIN": &l.CommentsPerMin,
		"LIMIT_ANCHOR_BYTES":                  &l.AnchorBytes,
		"LIMIT_ACCESS_MAIL_PER_WORKSPACE_DAY": &l.AccessMailPerWorkspaceDay, "LIMIT_ACCESS_MAIL_PER_DAY": &l.AccessMailPerDay,
	} {
		if err := set(k, dst); err != nil {
			return l, err
		}
	}
	for k, dst := range map[string]*int{"LIMIT_PROXY_PER_IP_PER_HOUR": &l.ProxyPerIPHour, "LIMIT_PROXY_PER_PAGE_PER_HOUR": &l.ProxyPerPageHour} {
		if err := set(k, dst); err != nil {
			return l, err
		}
	}
	quotaEnv := "LIMIT_ARTIFACTS_PER_TENANT"
	if getenv(quotaEnv) == "" && getenv("LIMIT_ARTIFACTS_PER_KEY") != "" {
		quotaEnv = "LIMIT_ARTIFACTS_PER_KEY"
		slog.Warn("LIMIT_ARTIFACTS_PER_KEY is deprecated; use LIMIT_ARTIFACTS_PER_TENANT")
	}
	if err := set(quotaEnv, &l.ArtifactsPerTenant); err != nil {
		return l, err
	}
	return l, nil
}

// splitList reads a comma-separated setting, dropping blanks.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// contentDomain reads CONTENT_DOMAIN: a bare domain (byagent.dev,
// byagent.page), which takes the share origin's scheme, or an origin
// (http://page.localhost:8080).
//
// Two layouts are accepted. The content domain may be the share origin's own
// host (shared): page hosts are then {slug}.byagent.dev, and only a name that
// is exactly one slug label is ever served as a page, so byagent.dev,
// app.byagent.dev and any other name keep their routes; byagent's own
// cookies are host-only (__Host-) so no page host can plant or read them.
// Or it is a domain apart from byagent's own hosts, overlapping them in
// neither direction: a share or API host under it would be served as a
// page, and a content domain under them would let a page set cookies the
// dashboard reads.
func contentDomain(raw, shareOrigin, apiOrigin string) (scheme, domain string, shared bool, err error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", "", false, nil
	}
	scheme = "https"
	if strings.HasPrefix(shareOrigin, "http://") {
		scheme = "http"
	}
	if !strings.Contains(raw, "://") {
		raw = scheme + "://" + raw
	}
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return "", "", false, fmt.Errorf("CONTENT_DOMAIN must be a domain such as byagent.dev, got %q", raw)
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return "", "", false, fmt.Errorf("CONTENT_DOMAIN must be a DNS name, got %q", host)
		}
	}
	domain = host
	if p := u.Port(); p != "" {
		domain += ":" + p
	}
	if so, err := url.Parse(shareOrigin); err == nil && strings.EqualFold(strings.TrimSuffix(so.Hostname(), "."), host) {
		return u.Scheme, domain, true, nil
	}
	for _, origin := range []string{shareOrigin, apiOrigin} {
		o, err := url.Parse(origin)
		if err != nil {
			continue
		}
		own := strings.ToLower(o.Hostname())
		// localhost is the one parent allowed: page.localhost next to
		// localhost:8080 is how this runs on a laptop, where browsers
		// resolve every *.localhost name to the machine itself.
		under := strings.HasSuffix(host, "."+own) && own != "localhost"
		if own == host || strings.HasSuffix(own, "."+host) || under {
			return "", "", false, fmt.Errorf("CONTENT_DOMAIN %q overlaps %s; use the share host itself (%s) or a domain of its own", host, origin, so(shareOrigin))
		}
		if lastLabels(own, 2) == lastLabels(host, 2) {
			slog.Warn("CONTENT_DOMAIN shares a parent domain with byagent's own hosts, so pages can set cookies for them", "content_domain", host, "origin", origin)
		}
	}
	return u.Scheme, domain, false, nil
}

// so is an origin's host name, for messages.
func so(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return origin
	}
	return u.Hostname()
}

// lastLabels is the last n labels of a host name: a rough registrable domain
// (there is no public suffix list here), good enough to warn about.
func lastLabels(host string, n int) string {
	parts := strings.Split(host, ".")
	if len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	return strings.Join(parts, ".")
}
