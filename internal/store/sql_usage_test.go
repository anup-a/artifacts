package store

import (
	"context"
	"testing"
	"time"
)

func usageArtifact(t *testing.T, s Store, key Key, id string, at time.Time) {
	t.Helper()
	if err := s.CreateArtifact(context.Background(), Artifact{ID: id, KeyID: key.ID, TenantID: key.TenantID, CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestAddViewsAccumulatesAndRollsBack(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "usage-hash", "usage@example.test")
	if err != nil {
		t.Fatal(err)
	}
	usageArtifact(t, s, k, "first", time.Now().UTC())
	usageArtifact(t, s, k, "second", time.Now().UTC())
	for _, batch := range [][]ViewCount{
		nil,
		{{ArtifactID: "first", Day: "2026-09-15", Count: 2}, {ArtifactID: "second", Day: "2026-09-15", Count: 5}},
		{{ArtifactID: "first", Day: "2026-09-15", Count: 3}, {ArtifactID: "first", Day: "2026-09-16", Count: 7}},
	} {
		if err := s.AddViews(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	// A batch is atomic, so retrying after an error cannot count its first row twice.
	if err := s.AddViews(ctx, []ViewCount{{ArtifactID: "first", Day: "2026-09-15", Count: 100}, {ArtifactID: "missing", Day: "2026-09-15", Count: 1}}); err == nil {
		t.Fatal("expected foreign key error")
	}
	db := s.(*sqlStore)
	for _, want := range []ViewCount{{ArtifactID: "first", Day: "2026-09-15", Count: 5}, {ArtifactID: "first", Day: "2026-09-16", Count: 7}, {ArtifactID: "second", Day: "2026-09-15", Count: 5}} {
		var got int64
		if err := db.db.QueryRowContext(ctx, db.q(`SELECT count FROM artifact_views WHERE artifact_id=? AND day=?`), want.ArtifactID, want.Day).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want.Count {
			t.Errorf("%s/%s: got %d, want %d", want.ArtifactID, want.Day, got, want.Count)
		}
	}
}

func TestUsageMonthsAreTenantScoped(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	k, err := s.CreateKey(ctx, "usage-main", "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateKey(ctx, "usage-other", "other@example.test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previous := month.AddDate(0, -1, 0)
	older := previous.AddDate(0, -1, 0)
	next := month.AddDate(0, 1, 0)
	currentEvent := month.Add(now.Sub(month) / 2)
	usageArtifact(t, s, k, "active", older)
	usageArtifact(t, s, k, "deleted-current", previous)
	usageArtifact(t, s, k, "deleted-previous", older)
	usageArtifact(t, s, other, "other-tenant", older)
	versions := []Version{
		{ArtifactID: "active", Version: 1, CreatedAt: older, Bytes: 100},
		{ArtifactID: "active", Version: 2, CreatedAt: previous, Bytes: 200},
		{ArtifactID: "active", Version: 3, CreatedAt: currentEvent, Bytes: 300},
		{ArtifactID: "active", Version: 4, CreatedAt: next, Bytes: 10000},
		{ArtifactID: "deleted-current", Version: 1, CreatedAt: previous, Bytes: 400},
		{ArtifactID: "deleted-previous", Version: 1, CreatedAt: older, Bytes: 500},
		{ArtifactID: "other-tenant", Version: 1, CreatedAt: previous, Bytes: 666},
	}
	for _, v := range versions {
		if err := s.AddVersion(ctx, v, nil); err != nil {
			t.Fatal(err)
		}
	}
	for id, at := range map[string]time.Time{"deleted-current": currentEvent, "deleted-previous": previous.Add(time.Hour)} {
		if err := s.SoftDelete(ctx, id, at); err != nil {
			t.Fatal(err)
		}
	}
	threads := []struct {
		id, artifact string
		created      time.Time
		resolved     time.Time
	}{
		{"old-open", "active", older, time.Time{}},
		{"resolved-previous", "active", older, previous.Add(time.Hour)},
		{"resolved-current", "active", previous, currentEvent},
		{"new-current", "active", currentEvent, time.Time{}},
		{"deleted-comment", "deleted-current", previous, time.Time{}},
		{"future-comment", "active", next, time.Time{}},
		{"other-comment", "other-tenant", previous, time.Time{}},
	}
	for _, row := range threads {
		if err := s.CreateThread(ctx, Thread{ID: row.id, ArtifactID: row.artifact, Version: 1, AnchorJSON: "{}", Status: "open", AnchorStatus: "unknown", CreatedAt: row.created},
			Comment{ID: "comment-" + row.id, ThreadID: row.id, AuthorName: "Reader", AuthorKind: "viewer", Body: "Feedback", CreatedAt: row.created}); err != nil {
			t.Fatal(err)
		}
		if !row.resolved.IsZero() {
			if err := s.ResolveThread(ctx, row.id, row.resolved); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.AddViews(ctx, []ViewCount{
		{ArtifactID: "active", Day: older.Format(time.DateOnly), Count: 11},
		{ArtifactID: "active", Day: previous.Format(time.DateOnly), Count: 3},
		{ArtifactID: "active", Day: now.Format(time.DateOnly), Count: 7},
		{ArtifactID: "active", Day: next.Format(time.DateOnly), Count: 100},
		{ArtifactID: "deleted-current", Day: previous.Format(time.DateOnly), Count: 5},
		{ArtifactID: "deleted-current", Day: currentEvent.Format(time.DateOnly), Count: 13},
		{ArtifactID: "other-tenant", Day: previous.Format(time.DateOnly), Count: 1000},
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []MonthUsage{
		{Month: previous, Publishes: 2, StorageBytes: 700, PageViews: 8, OpenComments: 3},
		{Month: month, Publishes: 1, StorageBytes: 600, PageViews: 20, OpenComments: 2},
	} {
		got, err := s.Usage(ctx, k.TenantID, want.Month.Add(24*time.Hour).In(time.FixedZone("local", 4*60*60)))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
	if got, err := s.Usage(ctx, "absent-tenant", previous); err != nil || got != (MonthUsage{Month: previous}) {
		t.Errorf("empty tenant: %+v, %v", got, err)
	}
}

func TestUsageStoreErrors(t *testing.T) {
	s := newStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.AddViews(context.Background(), []ViewCount{{ArtifactID: "one", Day: "2026-09-16", Count: 1}}); err == nil {
		t.Fatal("expected closed database error from AddViews")
	}
	if _, err := s.Usage(context.Background(), "one", time.Now()); err == nil {
		t.Fatal("expected closed database error from Usage")
	}
}
