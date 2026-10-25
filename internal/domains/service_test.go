package domains

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anup-a/byagent/internal/config"
	"github.com/anup-a/byagent/internal/store"
)

type mapResolver struct {
	mu  sync.Mutex
	txt map[string][]string
}

func (m *mapResolver) set(name string, vals ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txt[name] = vals
}

func (m *mapResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.txt[name]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// slowProvider is ready only once told to be, so a test can watch a domain
// move from verified-but-pending to active.
type slowProvider struct {
	mu    sync.Mutex
	ready bool
	added []string
}

func (p *slowProvider) Name() string { return "slow" }
func (p *slowProvider) Add(_ context.Context, h string) (Edge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.added = append(p.added, h)
	return Edge{Ref: "ref-" + h, Ready: p.ready, SSLStatus: "pending_validation"}, nil
}
func (p *slowProvider) Check(_ context.Context, h, ref string) (Edge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ready {
		return Edge{Ref: ref, Ready: true, SSLStatus: "active"}, nil
	}
	return Edge{Ref: ref, SSLStatus: "pending_validation", Message: "custom hostname does not CNAME to this zone."}, nil
}
func (p *slowProvider) Remove(context.Context, string, string) error { return nil }

type fixture struct {
	svc   *Service
	st    store.Store
	dns   *mapResolver
	prov  *slowProvider
	now   time.Time
	clock *time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, config.Config{DB: "sqlite", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	clock := now
	for _, id := range []string{"t_one", "t_two"} {
		if err := st.CreateTenant(ctx, store.Tenant{ID: id, Kind: "personal", Name: id, CreatedAt: now}, ""); err != nil {
			t.Fatal(err)
		}
		k, err := st.CreateTenantKey(ctx, id, "hash-"+id, "a@b.c", "test", "ak_x", now)
		if err != nil {
			t.Fatal(err)
		}
		a := store.Artifact{ID: "page_" + id, KeyID: k.ID, TenantID: id, CreatedAt: now, UpdatedAt: now}
		if err := st.CreateArtifact(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	f := &fixture{st: st, dns: &mapResolver{txt: map[string][]string{}}, prov: &slowProvider{}, now: now, clock: &clock}
	f.svc = &Service{Store: st, Provider: f.prov, Resolver: f.dns, Now: func() time.Time { return *f.clock },
		Target: "domains.byagent.dev", Scheme: "https", Own: []string{"byagent.dev", "app.byagent.dev", "domains.byagent.dev"},
		MaxDomains: 2, Index: &Index{Store: st}}
	return f
}

func TestServiceVerifyThenActivate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	d, created, e := f.svc.Add(ctx, "t_one", "Docs.Example.com")
	if e != nil || !created || d.Hostname != "docs.example.com" || d.Status != "pending" || len(d.Token) != 32 {
		t.Fatalf("add: %v %+v", e, d)
	}
	again, created, e := f.svc.Add(ctx, "t_one", "docs.example.com")
	if e != nil || created || again.Token != d.Token {
		t.Fatalf("re-add must return the same domain: %v %+v", e, again)
	}
	if _, _, e := f.svc.Add(ctx, "t_two", "docs.example.com"); e == nil || e.Status != 409 {
		t.Fatalf("another workspace: %v", e)
	}

	// No record yet: nothing reaches the provider and nothing is served.
	d = f.svc.Refresh(ctx, d, true)
	if d.VerifiedAt != nil || d.Status != "pending" || !strings.Contains(d.Message, "has not been seen") || len(f.prov.added) != 0 {
		t.Fatalf("unverified: %+v added=%v", d, f.prov.added)
	}
	if s, ok := f.svc.Index.Site(ctx, "docs.example.com"); !ok || s.Serving() {
		t.Fatalf("an unverified domain must be known but not served: %v %+v", ok, s)
	}
	// Somebody else's token, or a near miss, proves nothing.
	f.dns.set(TXTName("docs.example.com"), "byagent-verify=someone-elses-token", TXTValue(d.Token)+"x")
	d = f.svc.Refresh(ctx, d, true)
	if d.VerifiedAt != nil || !strings.Contains(d.Message, "does not hold") {
		t.Fatalf("wrong token verified: %+v", d)
	}
	f.dns.set(TXTName("docs.example.com"), "v=spf1 -all", " "+TXTValue(d.Token)+" ")
	d = f.svc.Refresh(ctx, d, true)
	if d.VerifiedAt == nil || d.Status != "pending" || d.ProviderRef != "ref-docs.example.com" || d.Provider != "slow" {
		t.Fatalf("verified, waiting on the edge: %+v", d)
	}
	if s, _ := f.svc.Index.Site(ctx, "docs.example.com"); !s.Serving() {
		t.Fatal("a verified domain serves")
	}
	// The throttle holds a second refresh within the window.
	f.prov.ready = true
	if d2 := f.svc.Refresh(ctx, d, false); d2.Status != "pending" {
		t.Fatalf("throttle: %+v", d2)
	}
	d = f.svc.Refresh(ctx, d, true)
	if d.Status != "active" || d.SSLStatus != "active" || d.Message != "" {
		t.Fatalf("active: %+v", d)
	}
	// Removing the record later does not unverify: the proof was made.
	f.dns.set(TXTName("docs.example.com"))
	if d = f.svc.Refresh(ctx, d, true); d.Status != "active" {
		t.Fatalf("after txt removal: %+v", d)
	}
}

func TestServiceRejectsOwnHostsAndQuota(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, h := range []string{"byagent.dev", "app.byagent.dev", "evil.byagent.dev", "127.0.0.1", "localhost"} {
		if _, _, e := f.svc.Add(ctx, "t_one", h); e == nil || e.Status != 400 {
			t.Errorf("%s: %v", h, e)
		}
	}
	for _, h := range []string{"a.example.com", "b.example.com"} {
		if _, _, e := f.svc.Add(ctx, "t_one", h); e != nil {
			t.Fatal(e)
		}
	}
	if _, _, e := f.svc.Add(ctx, "t_one", "c.example.com"); e == nil || e.Code != "QUOTA_EXCEEDED" {
		t.Fatalf("quota: %v", e)
	}
	if _, e := f.svc.Owned(ctx, "t_two", "a.example.com"); e == nil || e.Status != 404 {
		t.Fatalf("another workspace's domain must read as missing: %v", e)
	}
	if e := f.svc.Remove(ctx, "t_two", "a.example.com"); e == nil || e.Status != 404 {
		t.Fatalf("remove someone else's: %v", e)
	}
}

func activeDomain(t *testing.T, f *fixture, tenant, host string) {
	t.Helper()
	ctx := context.Background()
	d, _, e := f.svc.Add(ctx, tenant, host)
	if e != nil {
		t.Fatal(e)
	}
	f.prov.ready = true
	f.dns.set(TXTName(host), TXTValue(d.Token))
	if d = f.svc.Refresh(ctx, d, true); d.Status != "active" {
		t.Fatalf("not active: %+v", d)
	}
}

func TestServiceMountsAndPrimary(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	activeDomain(t, f, "t_one", "docs.example.com")
	if _, e := f.svc.Mount(ctx, "t_one", "docs.example.com", "/", "page_t_two"); e == nil || e.Status != 404 {
		t.Fatalf("mounting another workspace's page: %v", e)
	}
	if _, e := f.svc.Mount(ctx, "t_two", "docs.example.com", "/", "page_t_two"); e == nil || e.Status != 404 {
		t.Fatalf("mounting on another workspace's domain: %v", e)
	}
	if _, e := f.svc.Mount(ctx, "t_one", "docs.example.com", "/_rt", "page_t_one"); e == nil || e.Status != 400 {
		t.Fatalf("reserved path: %v", e)
	}
	m, e := f.svc.Mount(ctx, "t_one", "docs.example.com", "/guide/", "page_t_one")
	if e != nil || m.Path != "guide" {
		t.Fatalf("mount: %v %+v", e, m)
	}
	if got := f.svc.PrimaryURL(ctx, "page_t_one"); got != "" {
		t.Fatalf("no primary yet: %q", got)
	}
	if e := f.svc.SetPrimary(ctx, "t_one", "page_t_one", "docs.example.com", nil); e != nil {
		t.Fatal(e)
	}
	if got := f.svc.PrimaryURL(ctx, "page_t_one"); got != "https://docs.example.com/guide/" {
		t.Fatalf("primary: %q", got)
	}
	// A second mount on the same domain makes the path necessary.
	if _, e := f.svc.Mount(ctx, "t_one", "docs.example.com", "", "page_t_one"); e != nil {
		t.Fatal(e)
	}
	if e := f.svc.SetPrimary(ctx, "t_one", "page_t_one", "docs.example.com", nil); e == nil || e.Status != 400 {
		t.Fatalf("ambiguous primary: %v", e)
	}
	root := "/"
	if e := f.svc.SetPrimary(ctx, "t_one", "page_t_one", "docs.example.com", &root); e != nil {
		t.Fatal(e)
	}
	if got := f.svc.PrimaryURL(ctx, "page_t_one"); got != "https://docs.example.com/" {
		t.Fatalf("primary root: %q", got)
	}
	// A private page without a code is never redirected.
	if err := f.st.SetVisibility(ctx, "page_t_one", "private"); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.PrimaryURL(ctx, "page_t_one"); got != "" {
		t.Fatalf("private page redirected: %q", got)
	}
	if err := f.st.SetShareCode(ctx, "page_t_one", "123456", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.svc.PrimaryURL(ctx, "page_t_one"); got == "" {
		t.Fatal("a page shared by code redirects to its gate on the domain")
	}
	if e := f.svc.SetPrimary(ctx, "t_one", "page_t_one", "", nil); e != nil {
		t.Fatal(e)
	}
	if got := f.svc.PrimaryURL(ctx, "page_t_one"); got != "" {
		t.Fatalf("cleared: %q", got)
	}

	site, _ := f.svc.Index.Site(ctx, "docs.example.com")
	for p, want := range map[string]string{"/": "", "/guide": "guide", "/guide/": "guide", "/guide/a.css": "guide", "/guidex": "", "/other/x": ""} {
		m, _, _, ok := site.Match(p)
		if !ok || m.Path != want {
			t.Errorf("match %s: %+v %v", p, m, ok)
		}
	}
	if _, rest, slash, _ := site.Match("/guide"); !slash || rest != "" {
		t.Error("/guide should redirect to /guide/")
	}
	if _, rest, _, _ := site.Match("/guide/sub/a.css"); rest != "sub/a.css" {
		t.Errorf("rest %q", rest)
	}

	if e := f.svc.Unmount(ctx, "t_one", "docs.example.com", "/guide"); e != nil {
		t.Fatal(e)
	}
	if e := f.svc.Unmount(ctx, "t_one", "docs.example.com", "/guide"); e == nil || e.Status != 404 {
		t.Fatalf("unmount twice: %v", e)
	}
}

func TestPrimaryNeedsActiveDomain(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, _, e := f.svc.Add(ctx, "t_one", "docs.example.com"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.svc.Mount(ctx, "t_one", "docs.example.com", "", "page_t_one"); e != nil {
		t.Fatal(e)
	}
	if e := f.svc.SetPrimary(ctx, "t_one", "page_t_one", "docs.example.com", nil); e == nil || e.Status != 409 {
		t.Fatalf("pending domain made primary: %v", e)
	}
}

func TestCheckDueRemovesStalePending(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	if _, _, e := f.svc.Add(ctx, "t_one", "stale.example.com"); e != nil {
		t.Fatal(e)
	}
	activeDomain(t, f, "t_one", "live.example.com")
	*f.clock = f.now.Add(8 * 24 * time.Hour)
	f.svc.CheckDue(ctx)
	if _, err := f.st.Domain(ctx, "stale.example.com"); err != store.ErrNotFound {
		t.Fatalf("a week-old unverified domain should be gone: %v", err)
	}
	if d, err := f.st.Domain(ctx, "live.example.com"); err != nil || d.Status != "active" {
		t.Fatalf("an active domain stays: %v %+v", err, d)
	}
}

func TestRecords(t *testing.T) {
	f := newFixture(t)
	sub := f.svc.Records(store.Domain{Hostname: "docs.example.co.uk", Token: "tok"})
	if sub[0].Type != "CNAME" || sub[0].Host != "docs" || sub[0].Value != "domains.byagent.dev" {
		t.Fatalf("cname: %+v", sub[0])
	}
	if sub[1].Type != "TXT" || sub[1].Host != "_byagent.docs" || sub[1].Name != "_byagent.docs.example.co.uk" || sub[1].Value != "byagent-verify=tok" {
		t.Fatalf("txt: %+v", sub[1])
	}
	apex := f.svc.Records(store.Domain{Hostname: "example.com", Token: "tok"})
	if apex[0].Type != "ALIAS" || apex[0].Host != "@" || apex[1].Host != "_byagent" {
		t.Fatalf("apex: %+v", apex)
	}
	f.svc.ApexIPs = []string{"66.241.125.1", "2a09:8280:1::1"}
	ips := f.svc.Records(store.Domain{Hostname: "example.com", Token: "tok"})
	if ips[0].Type != "A" || ips[1].Type != "AAAA" || ips[2].Type != "TXT" {
		t.Fatalf("apex ips: %+v", ips)
	}
	v := f.svc.View(store.Domain{Hostname: "docs.example.com", Status: "pending", CreatedAt: f.now.Add(-48 * time.Hour)}, nil, f.now)
	if v.Verification == nil || v.Verification.State != "action_required" || v.Mounts == nil {
		t.Fatalf("view: %+v", v)
	}
}
