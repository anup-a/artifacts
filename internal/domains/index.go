package domains

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anup-a/byagent/internal/store"
)

// indexTTL is how stale the serving index may get when another process
// changed the tables. Writes through the Service invalidate it at once.
const indexTTL = 30 * time.Second

// Site is a connected hostname as the serving path needs it.
type Site struct {
	Domain store.Domain
	// Mounts are ordered longest path first, so the first match wins.
	Mounts []store.Mount
}

// Serving reports whether pages may be served on this hostname: only once
// its owner has proven control of it.
func (s *Site) Serving() bool { return s.Domain.VerifiedAt != nil }

// Match finds the mount for a request path. rest is the path below the
// mount's base, as the share origin's {path...} would carry it. slash means
// the path named the mount without its trailing slash and should redirect.
func (s *Site) Match(p string) (m store.Mount, rest string, slash, ok bool) {
	for _, m := range s.Mounts {
		if m.Path == "" {
			return m, strings.TrimPrefix(p, "/"), false, true
		}
		base := "/" + m.Path
		if p == base {
			return m, "", true, true
		}
		if strings.HasPrefix(p, base+"/") {
			return m, p[len(base)+1:], false, true
		}
	}
	return store.Mount{}, "", false, false
}

// Mounted reports whether artifactID is served anywhere on this hostname.
func (s *Site) Mounted(artifactID string) bool {
	for _, m := range s.Mounts {
		if m.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

// Index holds every connected hostname in memory, so a request's Host is
// matched without a query, and an unknown Host costs nothing at all.
type Index struct {
	Store store.Store
	mu    sync.Mutex
	at    time.Time
	sites map[string]*Site
	// primary maps an artifact to its primary mount, active domains only.
	primary map[string]store.Mount
}

// Invalidate makes the next lookup reload from the store.
func (ix *Index) Invalidate() {
	ix.mu.Lock()
	ix.at = time.Time{}
	ix.mu.Unlock()
}

func (ix *Index) snapshot(ctx context.Context) (map[string]*Site, map[string]store.Mount) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.sites != nil && time.Since(ix.at) < indexTTL {
		return ix.sites, ix.primary
	}
	ds, err := ix.Store.Domains(ctx, "")
	if err == nil {
		var ms []store.Mount
		ms, err = ix.Store.Mounts(ctx, "")
		if err == nil {
			sites := make(map[string]*Site, len(ds))
			for _, d := range ds {
				sites[d.Hostname] = &Site{Domain: d}
			}
			primary := map[string]store.Mount{}
			for _, m := range ms {
				s := sites[m.Hostname]
				if s == nil {
					continue
				}
				s.Mounts = append(s.Mounts, m)
				if m.Primary && s.Domain.Status == "active" && s.Domain.VerifiedAt != nil {
					primary[m.ArtifactID] = m
				}
			}
			for _, s := range sites {
				sort.SliceStable(s.Mounts, func(i, j int) bool { return len(s.Mounts[i].Path) > len(s.Mounts[j].Path) })
			}
			ix.sites, ix.primary, ix.at = sites, primary, time.Now()
			return sites, primary
		}
	}
	// A failed reload keeps serving the last good snapshot and tries again
	// on the next request rather than taking every custom domain down.
	slog.Error("domains index", "err", err)
	if ix.sites == nil {
		return map[string]*Site{}, map[string]store.Mount{}
	}
	return ix.sites, ix.primary
}

// Site returns the connected hostname matching host, whatever its state.
func (ix *Index) Site(ctx context.Context, host string) (*Site, bool) {
	sites, _ := ix.snapshot(ctx)
	s, ok := sites[host]
	return s, ok
}

// Primary returns the mount an artifact's share-origin address redirects to.
func (ix *Index) Primary(ctx context.Context, artifactID string) (store.Mount, bool) {
	_, primary := ix.snapshot(ctx)
	m, ok := primary[artifactID]
	return m, ok
}
