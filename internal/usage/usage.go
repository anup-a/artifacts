// Package usage counts artifact page views in memory and flushes them to the
// store in batches, and provides the month arithmetic the usage page needs
// and the ranges the analytics reports cover.
package usage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"log/slog"
	"sync"
	"time"

	"github.com/anup-a/artifacts/internal/store"
)

const hourFormat = "2006-01-02T15"

// Memory bounds. A facet group (one page, day and kind) buffers at most
// maxBufferedValues distinct values between flushes; the rest go to "(other)"
// before they reach the store, which caps them again at store.FacetCap.
// maxVisitors bounds the day's visitor set; past it every view counts as a
// new visitor, which overstates rather than grows without limit.
const (
	maxBufferedValues = 100
	maxVisitors       = 1 << 20
)

type bucket struct{ artifact, day string }
type hourBucket struct{ artifact, hour string }
type facetGroup struct{ artifact, day, kind string }

type tally struct{ views, visitors int64 }

// Hit is one page view as whatever served it saw it. Everything but
// ArtifactID is optional; Record fills it in from a request.
type Hit struct {
	ArtifactID string
	// Path is the page within the artifact, "/" for its index.
	Path string
	// Referrer is the host the visitor came from, Direct for none, or ""
	// to leave the view out of the referrer list (a link within the page).
	Referrer string
	// Country is a two-letter code from a trusted proxy, "" when unknown.
	Country string
	// Device is "desktop", "mobile" or "tablet", "" when unknown.
	Device string
	// Visitor is what tells visitors apart (client address and user agent).
	// It is hashed with the day's salt and never kept; "" counts no visitor.
	Visitor string
}

// Counter buffers views per (artifact, UTC day) and per hour, the day's
// unique visitors and the facet breakdowns, and flushes them in batches.
type Counter struct {
	store store.TenantStore
	// TrustProxy makes Record read the client address and country from
	// Cloudflare's headers. Set it only behind a proxy that overwrites them.
	TrustProxy bool
	// Now is the clock Record stamps views with. Nil means time.Now.
	Now func() time.Time

	mu     sync.Mutex
	buf    map[bucket]*tally
	hours  map[hourBucket]*tally
	facets map[facetGroup]map[string]int64

	// The visitor set: hashes of (salt, page, visitor) seen today. The salt
	// is random, lives only in memory and is replaced with the set at the
	// start of every UTC day, so no hash can be tied to a visitor afterwards.
	salt    []byte
	saltDay string
	seen    map[uint64]struct{}
}

func New(st store.TenantStore) *Counter {
	return &Counter{store: st, buf: map[bucket]*tally{}, hours: map[hourBucket]*tally{}, facets: map[facetGroup]map[string]int64{}}
}

// Hit records one page view of an artifact with nothing known about it.
func (c *Counter) Hit(artifactID string, now time.Time) {
	c.Add(Hit{ArtifactID: artifactID}, now)
}

// Add records one page view.
func (c *Counter) Add(h Hit, now time.Time) {
	now = now.UTC()
	day := now.Format(time.DateOnly)
	hour := now.Format(hourFormat)
	c.mu.Lock()
	defer c.mu.Unlock()
	newVisitor := h.Visitor != "" && c.firstVisit(day, h.ArtifactID, h.Visitor)
	add := func(t *tally) {
		t.views++
		if newVisitor {
			t.visitors++
		}
	}
	d := c.buf[bucket{h.ArtifactID, day}]
	if d == nil {
		d = &tally{}
		c.buf[bucket{h.ArtifactID, day}] = d
	}
	add(d)
	hb := c.hours[hourBucket{h.ArtifactID, hour}]
	if hb == nil {
		hb = &tally{}
		c.hours[hourBucket{h.ArtifactID, hour}] = hb
	}
	add(hb)
	for kind, value := range map[string]string{
		store.FacetReferrer: h.Referrer, store.FacetCountry: h.Country,
		store.FacetPath: h.Path, store.FacetDevice: h.Device,
	} {
		if value == "" {
			continue
		}
		g := facetGroup{h.ArtifactID, day, kind}
		m := c.facets[g]
		if m == nil {
			m = map[string]int64{}
			c.facets[g] = m
		}
		if _, ok := m[value]; !ok && len(m) >= maxBufferedValues {
			value = store.FacetOther
		}
		m[value]++
	}
}

// firstVisit reports whether visitor has not viewed the page yet today, and
// remembers that it now has. The caller holds c.mu.
func (c *Counter) firstVisit(day, artifactID, visitor string) bool {
	if day != c.saltDay || c.salt == nil {
		c.salt = make([]byte, 32)
		if _, err := rand.Read(c.salt); err != nil {
			return true
		}
		c.saltDay = day
		c.seen = map[uint64]struct{}{}
	}
	m := hmac.New(sha256.New, c.salt)
	m.Write([]byte(artifactID))
	m.Write([]byte{0})
	m.Write([]byte(visitor))
	key := binary.BigEndian.Uint64(m.Sum(nil))
	if _, ok := c.seen[key]; ok {
		return false
	}
	if len(c.seen) < maxVisitors {
		c.seen[key] = struct{}{}
	}
	return true
}

// Flush writes the buffered counts. On failure the counts stay buffered so
// the next flush retries them.
func (c *Counter) Flush(ctx context.Context) error {
	c.mu.Lock()
	pending, hours, facets := c.buf, c.hours, c.facets
	c.buf, c.hours, c.facets = map[bucket]*tally{}, map[hourBucket]*tally{}, map[facetGroup]map[string]int64{}
	c.mu.Unlock()
	var hourRows []store.ViewHour
	for b, t := range hours {
		hourRows = append(hourRows, store.ViewHour{ArtifactID: b.artifact, Hour: b.hour, Views: t.views, Visitors: t.visitors})
	}
	var facetRows []store.ViewFacet
	for g, m := range facets {
		for v, n := range m {
			facetRows = append(facetRows, store.ViewFacet{ArtifactID: g.artifact, Day: g.day, Kind: g.kind, Value: v, Views: n})
		}
	}
	var firstErr error
	if len(hourRows)+len(facetRows) > 0 {
		if err := c.store.AddViewDetails(ctx, hourRows, facetRows); err != nil {
			c.mu.Lock()
			for _, h := range hourRows {
				t := c.hourTally(hourBucket{h.ArtifactID, h.Hour})
				t.views += h.Views
				t.visitors += h.Visitors
			}
			for _, f := range facetRows {
				g := facetGroup{f.ArtifactID, f.Day, f.Kind}
				if c.facets[g] == nil {
					c.facets[g] = map[string]int64{}
				}
				c.facets[g][f.Value] += f.Views
			}
			c.mu.Unlock()
			firstErr = err
		}
	}
	if len(pending) == 0 {
		return firstErr
	}
	views := make([]store.ViewCount, 0, len(pending))
	for b, t := range pending {
		views = append(views, store.ViewCount{ArtifactID: b.artifact, Day: b.day, Count: t.views, Visitors: t.visitors})
	}
	if err := c.store.AddViews(ctx, views); err != nil {
		c.mu.Lock()
		for b, t := range pending {
			d := c.buf[b]
			if d == nil {
				d = &tally{}
				c.buf[b] = d
			}
			d.views += t.views
			d.visitors += t.visitors
		}
		c.mu.Unlock()
		return err
	}
	return firstErr
}

func (c *Counter) hourTally(b hourBucket) *tally {
	t := c.hours[b]
	if t == nil {
		t = &tally{}
		c.hours[b] = t
	}
	return t
}

// Run flushes every interval until ctx is done, then flushes once more so a
// shutdown loses nothing.
func (c *Counter) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Flush(flushCtx); err != nil {
				slog.Error("usage: final flush", "err", err)
			}
			return
		case <-t.C:
			if err := c.Flush(ctx); err != nil {
				slog.Error("usage: flush", "err", err)
			}
		}
	}
}

// MonthStart is the first instant of now's calendar month in UTC.
func MonthStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// PreviousMonth is the first instant of the month before now's, in UTC.
func PreviousMonth(now time.Time) time.Time {
	return MonthStart(now).AddDate(0, -1, 0)
}
