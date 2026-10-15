package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPageStatePatchReadAndRevision(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "state0000001", "b1")
	versioned(t, s, "state0000002", "c1")
	now := time.Now().UTC()

	st, err := s.State(ctx, "state0000001")
	if err != nil || st.Revision != 0 || st.Items == nil || len(st.Items) != 0 {
		t.Fatalf("fresh state = %+v %v", st, err)
	}
	rev, err := s.PatchState(ctx, "state0000001", map[string]string{"a": `1`, "b": `{"x":"é"}`}, nil, StateLimits{}, now)
	if err != nil || rev != 1 {
		t.Fatalf("patch = %d %v", rev, err)
	}
	rev, err = s.PatchState(ctx, "state0000001", map[string]string{"a": `2`}, []string{"b", "never-set"}, StateLimits{}, now)
	if err != nil || rev != 2 {
		t.Fatalf("second patch = %d %v", rev, err)
	}
	st, err = s.State(ctx, "state0000001")
	if err != nil || st.Revision != 2 || len(st.Items) != 1 || st.Items["a"] != "2" || st.Keys != 1 || st.Bytes != 2 {
		t.Fatalf("state = %+v %v", st, err)
	}
	if r, _ := s.StateRevision(ctx, "state0000001"); r != 2 {
		t.Fatalf("revision = %d", r)
	}
	// State is per artifact: the other page sees none of it.
	if other, _ := s.State(ctx, "state0000002"); len(other.Items) != 0 || other.Revision != 0 {
		t.Fatalf("state leaked to another artifact: %+v", other)
	}
	// Writes never make versions or touch the content timestamp.
	vs, _ := s.Versions(ctx, "state0000001")
	a, _ := s.Artifact(ctx, "state0000001")
	if len(vs) != 1 || a.CurrentVersion != 1 {
		t.Fatalf("a state write made a version: %d versions, current %d", len(vs), a.CurrentVersion)
	}
	if _, err := s.PatchState(ctx, "missing00001", map[string]string{"a": "1"}, nil, StateLimits{}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown artifact: %v", err)
	}
	if _, err := s.State(ctx, "missing00001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown artifact read: %v", err)
	}
}

func TestPageStateLimitsAreAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "statefull001", "b1")
	now := time.Now().UTC()
	lim := StateLimits{Keys: 2, Bytes: 20}
	if _, err := s.PatchState(ctx, "statefull001", map[string]string{"a": "1", "b": "2"}, nil, lim, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchState(ctx, "statefull001", map[string]string{"c": "3"}, []string{"a"}, StateLimits{Keys: 1}, now); !errors.Is(err, ErrStateFull) {
		t.Fatalf("over the key cap: %v", err)
	}
	// Bytes count key plus value, in bytes: "é" is two.
	if _, err := s.PatchState(ctx, "statefull001", map[string]string{"a": `"` + strings.Repeat("é", 8) + `"`}, nil, lim, now); !errors.Is(err, ErrStateFull) {
		t.Fatalf("over the byte cap: %v", err)
	}
	st, _ := s.State(ctx, "statefull001")
	if st.Revision != 1 || len(st.Items) != 2 || st.Items["a"] != "1" {
		t.Fatalf("a refused patch changed something: %+v", st)
	}
	// Replacing a value counts the new size, not old plus new.
	if _, err := s.PatchState(ctx, "statefull001", map[string]string{"a": `"` + strings.Repeat("x", 15) + `"`}, nil, lim, now); err != nil {
		t.Fatalf("a write within the cap after replacement: %v", err)
	}
}

func TestPageStateClearAndErase(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "stateclear01", "b1")
	now := time.Now().UTC()
	if _, err := s.PatchState(ctx, "stateclear01", map[string]string{"a": "1"}, nil, StateLimits{}, now); err != nil {
		t.Fatal(err)
	}
	rev, err := s.ClearState(ctx, "stateclear01")
	if err != nil || rev != 2 {
		t.Fatalf("clear = %d %v", rev, err)
	}
	if st, _ := s.State(ctx, "stateclear01"); len(st.Items) != 0 {
		t.Fatalf("clear left %+v", st.Items)
	}
	if _, err := s.PatchState(ctx, "stateclear01", map[string]string{"a": "1"}, nil, StateLimits{}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDelete(ctx, "stateclear01", now); err != nil {
		t.Fatal(err)
	}
	left, _ := s.Unerased(ctx)
	if len(left) != 1 {
		t.Fatalf("unerased = %v", left)
	}
	if err := s.Erase(ctx, "stateclear01"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.State(ctx, "stateclear01"); len(st.Items) != 0 {
		t.Fatalf("erase left state behind: %+v", st.Items)
	}
}

func TestStateEnabledDefaultsOnAndToggles(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "stateflag001", "b1")
	if a, _ := s.Artifact(ctx, "stateflag001"); !a.StateEnabled {
		t.Fatal("new artifact must have state on")
	}
	if err := s.SetStateEnabled(ctx, "stateflag001", false); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Artifact(ctx, "stateflag001"); a.StateEnabled {
		t.Fatal("still on after SetStateEnabled(false)")
	}
	if err := s.SetStateEnabled(ctx, "missing00001", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestVersionSummaryRoundTrips(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	versioned(t, s, "summary00001", "b1")
	now := time.Now().UTC()
	if err := s.AddVersion(ctx, Version{ArtifactID: "summary00001", Version: 2, CreatedAt: now, Summary: "line one\nline two"}, nil); err != nil {
		t.Fatal(err)
	}
	vs, err := s.Versions(ctx, "summary00001")
	if err != nil || len(vs) != 2 || vs[0].Summary != "" || vs[1].Summary != "line one\nline two" {
		t.Fatalf("versions = %+v %v", vs, err)
	}
}
