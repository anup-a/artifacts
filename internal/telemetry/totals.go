package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/anup-a/byagent/internal/store"
)

// totalsEvery bounds how often the totals are counted: four COUNT(*)
// queries, at most once a minute however often the reader collects.
const totalsEvery = time.Minute

// totalsTimeout keeps a slow count from holding up an export.
const totalsTimeout = 5 * time.Second

var pageStateKey = attribute.Key("byagent.page.state") // live, deleted

// ObserveTotals reports the number of accounts, guest workspaces and pages
// as gauges. Every page ever created is in one state or the other, so their
// sum only grows and its change over a day is the pages created that day.
// It registers callbacks on the global MeterProvider, so call it after
// Start, and only when Enabled.
func ObserveTotals(count func(context.Context, time.Time) (store.Totals, error)) error {
	m := otel.Meter(scope)
	users, err := m.Int64ObservableGauge("byagent.users", metric.WithUnit("{user}"),
		metric.WithDescription("Signed-up accounts."))
	if err != nil {
		return err
	}
	guests, err := m.Int64ObservableGauge("byagent.guests", metric.WithUnit("{workspace}"),
		metric.WithDescription("Guest workspaces ever made by publishing without an account, claimed or not."))
	if err != nil {
		return err
	}
	pages, err := m.Int64ObservableGauge("byagent.pages", metric.WithUnit("{page}"),
		metric.WithDescription("Pages by state: live (being served) or deleted (deleted or expired)."))
	if err != nil {
		return err
	}
	s := &totalsSampler{count: count}
	_, err = m.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		t, ok := s.sample(ctx)
		if !ok {
			// Nothing observed rather than a zero, so a failed count leaves a
			// gap in the graph instead of a drop to nothing.
			return nil
		}
		o.ObserveInt64(users, t.Users)
		o.ObserveInt64(guests, t.Guests)
		o.ObserveInt64(pages, t.LivePages, metric.WithAttributes(pageStateKey.String("live")))
		o.ObserveInt64(pages, t.DeletedPages, metric.WithAttributes(pageStateKey.String("deleted")))
		return nil
	}, users, guests, pages)
	return err
}

// totalsSampler caches the last count for totalsEvery.
type totalsSampler struct {
	count func(context.Context, time.Time) (store.Totals, error)

	mu   sync.Mutex
	at   time.Time
	last store.Totals
	ok   bool
}

func (s *totalsSampler) sample(ctx context.Context) (store.Totals, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < totalsEvery {
		return s.last, s.ok
	}
	ctx, cancel := context.WithTimeout(ctx, totalsTimeout)
	defer cancel()
	now := time.Now()
	t, err := s.count(ctx, now.UTC())
	if err != nil {
		slog.Warn("telemetry: totals", "err", err)
	}
	s.at, s.last, s.ok = now, t, err == nil
	return t, s.ok
}
