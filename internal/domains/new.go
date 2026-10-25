package domains

import (
	"net/url"
	"strings"
	"time"

	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/store"
)

// New builds the service from the server config, or returns nil when custom
// domains are off (DOMAINS_PROVIDER unset).
func New(cfg config.Config, st store.Store, now func() time.Time) *Service {
	var p Provider
	switch cfg.Domains.Provider {
	case "":
		return nil
	case "cloudflare":
		p = &Cloudflare{Token: cfg.Domains.CloudflareToken, ZoneID: cfg.Domains.CloudflareZoneID}
	case "fly":
		p = &Fly{Token: cfg.Domains.FlyToken, App: cfg.Domains.FlyApp}
	case "manual":
		p = Manual{}
	default:
		p = None{}
	}
	scheme := "https"
	if strings.HasPrefix(cfg.ShareOrigin, "http://") {
		scheme = "http"
	}
	own := []string{originHost(cfg.ShareOrigin), originHost(cfg.APIOrigin), cfg.Domains.Target}
	// Everything under the share origin's registrable domain is byagent's:
	// byagent.dev covers app.byagent.dev and any host added later.
	if h := originHost(cfg.ShareOrigin); h != "" {
		own = append(own, Registrable(h))
	}
	// So is the content domain, where pages have hosts of their own.
	if cfg.ContentDomain != "" {
		if h := originHost("https://" + cfg.ContentDomain); h != "" {
			own = append(own, h)
		}
	}
	return &Service{
		Store: st, Provider: p, Resolver: NewResolver(cfg.Domains.Resolver), Now: now,
		Target: cfg.Domains.Target, ApexIPs: cfg.Domains.ApexIPs, Scheme: scheme, Own: own,
		MaxDomains: cfg.Limits.DomainsPerTenant, Index: &Index{Store: st},
	}
}

func originHost(origin string) string {
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
