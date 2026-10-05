package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestVariablesStore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"t_one", "t_two"} {
		if err := s.CreateTenant(ctx, Tenant{ID: id, Kind: "personal", Name: id, CreatedAt: now}, ""); err != nil {
			t.Fatal(err)
		}
	}
	put := func(tenant, name, sealed string, max int) (bool, error) {
		return s.PutVariable(ctx, Variable{TenantID: tenant, Name: name, Sealed: sealed, AllowedUpstreams: []string{"api.example.com"}, CreatedAt: now, UpdatedAt: now}, max)
	}
	if created, err := put("t_one", "B_KEY", "k1:aaa", 2); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if _, err := put("t_one", "A_KEY", "k1:bbb", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := put("t_one", "C_KEY", "k1:ccc", 2); !errors.Is(err, ErrVariableLimit) {
		t.Fatalf("limit: %v", err)
	}
	later := now.Add(time.Hour)
	created, err := s.PutVariable(ctx, Variable{TenantID: "t_one", Name: "B_KEY", Sealed: "k1:new", AllowedUpstreams: []string{"x.example.com"}, CreatedAt: later, UpdatedAt: later}, 2)
	if err != nil || created {
		t.Fatalf("replace at the limit: %v %v", created, err)
	}
	if _, err := put("t_two", "B_KEY", "k1:other", 2); err != nil {
		t.Fatal("the limit and the names are per workspace")
	}
	vs, err := s.Variables(ctx, "t_one")
	if err != nil || len(vs) != 2 || vs[0].Name != "A_KEY" || vs[1].Name != "B_KEY" {
		t.Fatalf("list: %v %+v", err, vs)
	}
	b := vs[1]
	if b.Sealed != "k1:new" || len(b.AllowedUpstreams) != 1 || b.AllowedUpstreams[0] != "x.example.com" ||
		!b.CreatedAt.Equal(now) || !b.UpdatedAt.Equal(later) {
		t.Fatalf("replace kept created_at, moved updated_at: %+v", b)
	}
	if err := s.DeleteVariable(ctx, "t_two", "A_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace delete: %v", err)
	}
	if err := s.DeleteVariable(ctx, "t_one", "A_KEY"); err != nil {
		t.Fatal(err)
	}
	if vs, _ := s.Variables(ctx, "t_two"); len(vs) != 1 || vs[0].Sealed != "k1:other" {
		t.Fatalf("other workspace touched: %+v", vs)
	}
}
