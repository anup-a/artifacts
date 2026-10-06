package domains

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

const (
	// MaxMounts caps the pages one domain serves.
	MaxMounts = 50
	// pendingTTL is how long a domain may sit unverified before it is
	// removed, so nobody can park on a name they do not control.
	pendingTTL = 7 * 24 * time.Hour
	// actionAfter is when a domain still pending stops saying "keep
	// waiting" and starts saying "check your records".
	actionAfter = 24 * time.Hour
	// refreshEvery throttles the check a GET triggers.
	refreshEvery = 15 * time.Second
)

// Service is everything the API and the dashboard do with domains. Its
// methods return *apierr.Error so both surfaces show the same messages.
type Service struct {
	Store    store.Store
	Provider Provider
	Resolver Resolver
	Now      func() time.Time
	// Target is the hostname customers CNAME to (domains.byagent.dev).
	Target string
	// ApexIPs, when set, are the addresses an apex domain points A/AAAA
	// records at. Without them an apex is told to use ALIAS / CNAME
	// flattening onto Target.
	ApexIPs []string
	// Scheme is how custom domains are reached: https in production.
	Scheme string
	// Own lists the hosts byagent answers on itself. Neither they nor
	// anything under them can be connected.
	Own        []string
	MaxDomains int
	Index      *Index
}

func conflict(msg string) *apierr.Error {
	return &apierr.Error{Code: "CONFLICT", Status: http.StatusConflict, Message: msg}
}

func (s *Service) host(input string) (string, *apierr.Error) {
	h, err := NormalizeHost(input)
	if err != nil {
		return "", apierr.Invalid(err.Error())
	}
	if Reserved(h, s.Own) {
		return "", apierr.Invalid("that hostname belongs to byagent; connect a domain you own")
	}
	return h, nil
}

// Add connects a hostname to a workspace. Adding one the workspace already
// has returns it unchanged, so an agent can repeat the call safely. Nothing
// is sent to the edge yet: that waits until the TXT record proves the
// workspace controls the name.
func (s *Service) Add(ctx context.Context, tenantID, input string) (store.Domain, bool, *apierr.Error) {
	h, e := s.host(input)
	if e != nil {
		return store.Domain{}, false, e
	}
	d := store.Domain{Hostname: h, TenantID: tenantID, Token: ids.NewToken()[:32], Status: "pending", CreatedAt: s.Now()}
	got, err := s.Store.CreateDomain(ctx, d, s.MaxDomains)
	switch {
	case errors.Is(err, store.ErrDomainTaken):
		if got.TenantID == tenantID {
			return got, false, nil
		}
		return store.Domain{}, false, conflict("that domain is already connected to another byagent workspace")
	case errors.Is(err, store.ErrDomainQuota):
		return store.Domain{}, false, apierr.Quota("this workspace already has the most custom domains it can hold; remove one first")
	case err != nil:
		return store.Domain{}, false, apierr.Internal(err)
	}
	s.Index.Invalidate()
	return got, true, nil
}

// Owned loads one of a workspace's domains. Another workspace's domain is
// not found, exactly as a name nobody connected is.
func (s *Service) Owned(ctx context.Context, tenantID, input string) (store.Domain, *apierr.Error) {
	h, err := NormalizeHost(input)
	if err != nil {
		return store.Domain{}, apierr.NotFound("domain")
	}
	d, err := s.Store.Domain(ctx, h)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.TenantID != tenantID) {
		return store.Domain{}, apierr.NotFound("domain")
	}
	if err != nil {
		return store.Domain{}, apierr.Internal(err)
	}
	return d, nil
}

// Remove disconnects a domain: its mounts go, and the edge forgets it.
func (s *Service) Remove(ctx context.Context, tenantID, input string) *apierr.Error {
	d, e := s.Owned(ctx, tenantID, input)
	if e != nil {
		return e
	}
	if d.Provider != "" && d.Provider == s.Provider.Name() {
		if err := s.Provider.Remove(ctx, d.Hostname, d.ProviderRef); err != nil {
			// The row goes regardless: a hostname we no longer serve is
			// harmless at the edge, and ops can tidy it from the log.
			slog.Warn("domains: provider remove failed", "domain", d.Hostname, "err", err)
		}
	}
	if err := s.Store.DeleteDomain(ctx, d.Hostname); err != nil && !errors.Is(err, store.ErrNotFound) {
		return apierr.Internal(err)
	}
	s.Index.Invalidate()
	return nil
}

// Refresh checks a domain now when it is due: the TXT record until it is
// seen, then the edge. force skips the throttle. Errors from DNS or the
// provider become the domain's message rather than a failed request.
func (s *Service) Refresh(ctx context.Context, d store.Domain, force bool) store.Domain {
	now := s.Now()
	if !force && d.CheckedAt != nil && now.Sub(*d.CheckedAt) < refreshEvery {
		return d
	}
	before := d
	msgs := []string{}
	if d.VerifiedAt == nil {
		if ok, msg := s.verify(ctx, d); ok {
			t := now
			d.VerifiedAt = &t
		} else {
			msgs = append(msgs, msg)
		}
	}
	ready, failed := false, false
	if d.VerifiedAt != nil {
		var edge Edge
		var err error
		if d.Provider != s.Provider.Name() {
			edge, err = s.Provider.Add(ctx, d.Hostname)
		} else {
			edge, err = s.Provider.Check(ctx, d.Hostname, d.ProviderRef)
		}
		if err != nil {
			slog.Warn("domains: provider check failed", "domain", d.Hostname, "err", err)
			msgs = append(msgs, "the certificate provider could not be reached; byagent will try again")
			ready = d.Status == "active" && d.Provider == s.Provider.Name()
		} else {
			d.Provider, d.ProviderRef, d.SSLStatus = s.Provider.Name(), edge.Ref, edge.SSLStatus
			ready, failed = edge.Ready, edge.Failed
			if !edge.Ready && edge.Message != "" {
				msgs = append(msgs, edge.Message)
			} else if !edge.Ready {
				msgs = append(msgs, "waiting for the CNAME record to reach "+s.Target+" and the certificate to be issued")
			}
		}
	}
	switch {
	case d.VerifiedAt != nil && ready:
		d.Status = "active"
	case failed:
		d.Status = "error"
	default:
		d.Status = "pending"
	}
	d.Message = strings.Join(msgs, "; ")
	d.CheckedAt = &now
	if err := s.Store.UpdateDomainState(ctx, d); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("domains: save check", "domain", d.Hostname, "err", err)
		}
		return d
	}
	if before.Status != d.Status || (before.VerifiedAt == nil) != (d.VerifiedAt == nil) {
		s.Index.Invalidate()
	}
	return d
}

// verify looks for the TXT record. Only an exact value counts: another
// domain's token, or a near miss, proves nothing.
func (s *Service) verify(ctx context.Context, d store.Domain) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	name := TXTName(d.Hostname)
	want := TXTValue(d.Token)
	vals, err := s.Resolver.LookupTXT(ctx, name)
	var dnsErr *net.DNSError
	if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		return false, "the TXT record at " + name + " could not be looked up yet; byagent will try again"
	}
	for _, v := range vals {
		if strings.TrimSpace(v) == want {
			return true, ""
		}
	}
	if len(vals) > 0 {
		return false, "the TXT record at " + name + " does not hold this domain's value " + want
	}
	return false, "the TXT record at " + name + " has not been seen yet"
}

// Run checks due domains until ctx ends, and removes domains nobody
// verified within a week. paused, when it reports true, skips a round (the
// server is read-only during a move).
func (s *Service) Run(ctx context.Context, every time.Duration, paused func() bool) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if paused != nil && paused() {
				continue
			}
			s.CheckDue(ctx)
		}
	}
}

// CheckDue runs one round of background checks. A pending domain is looked
// at often while it is new and less often as it ages; an active one twice a
// day, which is enough to notice a certificate the edge lost.
func (s *Service) CheckDue(ctx context.Context) {
	ds, err := s.Store.Domains(ctx, "")
	if err != nil {
		slog.Error("domains: list", "err", err)
		return
	}
	now := s.Now()
	checked := 0
	for _, d := range ds {
		if ctx.Err() != nil || checked >= 25 {
			return
		}
		if d.VerifiedAt == nil && now.Sub(d.CreatedAt) > pendingTTL {
			if err := s.Store.DeleteDomain(ctx, d.Hostname); err == nil {
				slog.Info("domains: removed unverified domain", "domain", d.Hostname)
				s.Index.Invalidate()
			}
			continue
		}
		if d.CheckedAt != nil && now.Sub(*d.CheckedAt) < dueAfter(d, now) {
			continue
		}
		s.Refresh(ctx, d, true)
		checked++
	}
}

func dueAfter(d store.Domain, now time.Time) time.Duration {
	if d.Status == "active" {
		return 12 * time.Hour
	}
	switch age := now.Sub(d.CreatedAt); {
	case age < time.Hour:
		return time.Minute
	case age < actionAfter:
		return 10 * time.Minute
	default:
		return time.Hour
	}
}

// Mount serves one of the workspace's pages at path on one of its domains,
// or points an existing mount at another page.
func (s *Service) Mount(ctx context.Context, tenantID, domain, path, artifactID string) (store.Mount, *apierr.Error) {
	d, e := s.Owned(ctx, tenantID, domain)
	if e != nil {
		return store.Mount{}, e
	}
	p, err := NormalizePath(path)
	if err != nil {
		return store.Mount{}, apierr.Invalid(err.Error())
	}
	a, err := s.Store.Artifact(ctx, artifactID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (a.TenantID != tenantID || a.DeletedAt != nil)) {
		return store.Mount{}, apierr.NotFound("artifact")
	}
	if err != nil {
		return store.Mount{}, apierr.Internal(err)
	}
	ms, err := s.Store.Mounts(ctx, d.Hostname)
	if err != nil {
		return store.Mount{}, apierr.Internal(err)
	}
	exists := false
	for _, m := range ms {
		if m.Path == p {
			exists = true
		}
	}
	if !exists && len(ms) >= MaxMounts {
		return store.Mount{}, apierr.Quota("a domain serves at most 50 pages")
	}
	m := store.Mount{Hostname: d.Hostname, Path: p, ArtifactID: a.ID, CreatedAt: s.Now()}
	if err := s.Store.PutMount(ctx, m); err != nil {
		return store.Mount{}, apierr.Internal(err)
	}
	s.Index.Invalidate()
	ms, _ = s.Store.Mounts(ctx, d.Hostname)
	for _, x := range ms {
		if x.Path == p {
			return x, nil
		}
	}
	return m, nil
}

// Unmount stops serving whatever is at path on a domain.
func (s *Service) Unmount(ctx context.Context, tenantID, domain, path string) *apierr.Error {
	d, e := s.Owned(ctx, tenantID, domain)
	if e != nil {
		return e
	}
	p, err := NormalizePath(path)
	if err != nil {
		return apierr.Invalid(err.Error())
	}
	if err := s.Store.DeleteMount(ctx, d.Hostname, p); errors.Is(err, store.ErrNotFound) {
		return apierr.NotFound("mount")
	} else if err != nil {
		return apierr.Internal(err)
	}
	s.Index.Invalidate()
	return nil
}

// SetPrimary makes one of an artifact's mounts the address its byagent.dev
// links redirect to, or clears it when domain is "". The domain must be
// active: redirecting readers to a name without a certificate would break
// every link at once. path may be omitted when the artifact has exactly one
// mount on the domain.
func (s *Service) SetPrimary(ctx context.Context, tenantID, artifactID, domain string, path *string) *apierr.Error {
	if domain == "" {
		if err := s.Store.SetPrimaryMount(ctx, artifactID, "", ""); err != nil {
			return apierr.Internal(err)
		}
		s.Index.Invalidate()
		return nil
	}
	d, e := s.Owned(ctx, tenantID, domain)
	if e != nil {
		return e
	}
	if d.Status != "active" {
		return conflict("only an active domain can be primary; check its status with GET /api/domains/" + d.Hostname)
	}
	ms, err := s.Store.ArtifactMounts(ctx, artifactID)
	if err != nil {
		return apierr.Internal(err)
	}
	var on []store.Mount
	for _, m := range ms {
		if m.Hostname == d.Hostname {
			on = append(on, m)
		}
	}
	var pick *store.Mount
	if path != nil {
		p, err := NormalizePath(*path)
		if err != nil {
			return apierr.Invalid(err.Error())
		}
		for i := range on {
			if on[i].Path == p {
				pick = &on[i]
			}
		}
	} else if len(on) == 1 {
		pick = &on[0]
	} else if len(on) > 1 {
		return apierr.Invalid("this page is mounted at more than one path on " + d.Hostname + "; give the path")
	}
	if pick == nil {
		return apierr.Invalid("mount this page on " + d.Hostname + " first")
	}
	if err := s.Store.SetPrimaryMount(ctx, artifactID, pick.Hostname, pick.Path); err != nil {
		return apierr.Internal(err)
	}
	s.Index.Invalidate()
	return nil
}

// URL is the address a mount serves at.
func (s *Service) URL(hostname, path string) string {
	return s.Scheme + "://" + hostname + Base(path)
}

// PrimaryURL is the base an artifact's share-origin address redirects to,
// or "" to serve it there. A private page without a share code is never
// redirected: the share origin answers it with a plain 404, and a redirect
// would tell a stranger the page exists and where it lives.
func (s *Service) PrimaryURL(ctx context.Context, artifactID string) string {
	m, ok := s.Index.Primary(ctx, artifactID)
	if !ok {
		return ""
	}
	site, ok := s.Index.Site(ctx, m.Hostname)
	if !ok {
		return ""
	}
	a, err := s.Store.Artifact(ctx, artifactID)
	if err != nil || a.DeletedAt != nil || a.TenantID != site.Domain.TenantID {
		return ""
	}
	if a.Visibility == "private" && a.ShareCode == "" {
		return ""
	}
	return s.URL(m.Hostname, m.Path)
}
