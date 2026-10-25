package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLinksStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"t_one", "t_two"} {
		if err := s.CreateTenant(ctx, Tenant{ID: id, Kind: "personal", Name: id, CreatedAt: now}, ""); err != nil {
			t.Fatal(err)
		}
	}
	proj := "site"
	first, created, err := s.PutLink(ctx, Link{ID: "ln_a", TenantID: "t_one", URL: "https://a.vercel.app/", Host: "a.vercel.app", Title: "Landing", Project: &proj, Agent: "claude-code", KeyName: "k1", CreatedAt: now, UpdatedAt: now}, 2)
	if err != nil || !created || first.ID != "ln_a" || first.Title != "Landing" {
		t.Fatalf("create: %+v %v %v", first, created, err)
	}
	// A bare re-register, as the hook makes on every deploy, refreshes the
	// time and keeps the labels and the id.
	later := now.Add(time.Hour)
	again, created, err := s.PutLink(ctx, Link{ID: "ln_ignored", TenantID: "t_one", URL: "https://a.vercel.app/", Host: "a.vercel.app", KeyName: "k2", CreatedAt: later, UpdatedAt: later}, 2)
	if err != nil || created || again.ID != "ln_a" || again.Title != "Landing" || again.Project == nil || *again.Project != "site" || again.Agent != "claude-code" || again.KeyName != "k2" || !again.UpdatedAt.Equal(later) || !again.CreatedAt.Equal(now) {
		t.Fatalf("refresh: %+v %v %v", again, created, err)
	}
	if _, _, err := s.PutLink(ctx, Link{ID: "ln_b", TenantID: "t_one", URL: "https://b.netlify.app/", Host: "b.netlify.app", Title: "Docs", CreatedAt: now, UpdatedAt: now}, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PutLink(ctx, Link{ID: "ln_c", TenantID: "t_one", URL: "https://c.netlify.app/", Host: "c.netlify.app", CreatedAt: now, UpdatedAt: now}, 2); !errors.Is(err, ErrLinkLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, _, err := s.PutLink(ctx, Link{ID: "ln_d", TenantID: "t_two", URL: "https://a.vercel.app/", Host: "a.vercel.app", CreatedAt: now, UpdatedAt: now}, 2); err != nil {
		t.Fatal("URLs are unique per workspace only:", err)
	}

	ls, err := s.ListLinks(ctx, "t_one", LinkFilter{})
	if err != nil || len(ls) != 2 || ls[0].ID != "ln_a" {
		t.Fatalf("list, newest first: %+v %v", ls, err)
	}
	if ls, _ := s.ListLinks(ctx, "t_one", LinkFilter{Query: "NETLIFY"}); len(ls) != 1 || ls[0].ID != "ln_b" {
		t.Fatalf("query matches the URL: %+v", ls)
	}
	if ls, _ := s.ListLinks(ctx, "t_one", LinkFilter{Project: "site"}); len(ls) != 1 || ls[0].ID != "ln_a" {
		t.Fatalf("project filter: %+v", ls)
	}
	if _, err := s.LinkByID(ctx, "t_two", "ln_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another workspace's link: %v", err)
	}
	if l, err := s.LinkByURL(ctx, "t_one", "https://b.netlify.app/"); err != nil || l.ID != "ln_b" {
		t.Fatalf("by url: %+v %v", l, err)
	}
	if err := s.DeleteLink(ctx, "t_two", "ln_a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete across workspaces: %v", err)
	}
	if err := s.DeleteLink(ctx, "t_one", "ln_a"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountLinks(ctx, "t_one"); n != 1 {
		t.Fatalf("count after delete: %d", n)
	}
}
