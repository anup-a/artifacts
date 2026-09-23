package store

import (
	"reflect"
	"testing"
	"time"
)

func TestCourseOrder(t *testing.T) {
	base := time.Date(2026, 9, 23, 9, 0, 0, 0, time.UTC)
	mk := func(id string, minute int) Artifact {
		return Artifact{ID: id, CreatedAt: base.Add(time.Duration(minute) * time.Minute)}
	}
	// Newest first, as ListArtifacts returns them.
	list := []Artifact{mk("d", 3), mk("c", 2), mk("b", 1), mk("a", 0)}
	ids := func(as []Artifact) []string {
		out := []string{}
		for _, a := range as {
			out = append(out, a.ID)
		}
		return out
	}
	if got := ids(CourseOrder(list, nil)); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("no positions: publish order, got %v", got)
	}
	if got := ids(CourseOrder(list, []string{"c", "gone", "a", "c"})); !reflect.DeepEqual(got, []string{"c", "a", "b", "d"}) {
		t.Fatalf("positions first, unknown and repeated ids skipped, rest by publish time: %v", got)
	}
	if list[0].ID != "d" {
		t.Fatal("input must not be reordered")
	}
}
