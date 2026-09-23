package store

import "sort"

// CourseOrder arranges a collection's artifacts the way a reader should meet
// them: the saved positions first, in that order, then everything not yet
// positioned by publish time, oldest first. A page published after the order
// was saved therefore joins at the end instead of disappearing. Ids in
// positions that no longer match an artifact are skipped. The input is not
// modified.
func CourseOrder(list []Artifact, positions []string) []Artifact {
	byID := make(map[string]Artifact, len(list))
	for _, a := range list {
		byID[a.ID] = a
	}
	out := make([]Artifact, 0, len(list))
	placed := map[string]bool{}
	for _, id := range positions {
		if a, ok := byID[id]; ok && !placed[id] {
			out = append(out, a)
			placed[id] = true
		}
	}
	rest := make([]Artifact, 0, len(list)-len(out))
	for _, a := range list {
		if !placed[a.ID] {
			rest = append(rest, a)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		if !rest[i].CreatedAt.Equal(rest[j].CreatedAt) {
			return rest[i].CreatedAt.Before(rest[j].CreatedAt)
		}
		return rest[i].ID < rest[j].ID
	})
	return append(out, rest...)
}
