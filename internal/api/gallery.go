package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/cover"
	"github.com/anup-a/artifacts/internal/store"
)

// Limits on the gallery labels, from the design spec.
const (
	maxProjectChars = 64
	maxTags         = 10
	maxTagChars     = 32
)

// optString distinguishes an omitted JSON key (Set false) from an explicit
// null (Null true) from a value, so PUT can keep, clear or replace a label.
type optString struct {
	Set, Null bool
	Value     string
}

func (o *optString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// optStrings is the same for a string array. null and [] both clear.
type optStrings struct {
	Set    bool
	Values []string
}

func (o *optStrings) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		return nil
	}
	return json.Unmarshal(b, &o.Values)
}

// cleanProject trims and validates a project label. An empty result means
// "no project", the same as null.
func cleanProject(raw string) (*string, *apierr.Error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return nil, nil
	}
	if !utf8.ValidString(p) || hasControl(p, false) {
		return nil, apierr.Invalid("project must not contain control characters")
	}
	if utf8.RuneCountInString(p) > maxProjectChars {
		return nil, apierr.Invalid(fmt.Sprintf("project must be 1 to %d characters", maxProjectChars))
	}
	return &p, nil
}

// cleanTags lowercases, trims and deduplicates tags, keeping first-seen order.
func cleanTags(raw []string) ([]string, *apierr.Error) {
	out := []string{}
	seen := map[string]bool{}
	for i, t := range raw {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || utf8.RuneCountInString(t) > maxTagChars {
			return nil, apierr.Invalid(fmt.Sprintf("tags[%d] must be 1 to %d characters", i, maxTagChars))
		}
		if !utf8.ValidString(t) || hasControl(t, false) {
			return nil, apierr.Invalid(fmt.Sprintf("tags[%d] must not contain control characters", i))
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, apierr.Invalid(fmt.Sprintf("tags: at most %d allowed", maxTags))
	}
	return out, nil
}

// coverExts are the image types the cover endpoint will stream.
var coverExts = map[string]bool{".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// validateCover checks that a manifest cover names an image file in the same
// manifest.
func validateCover(p string, files []manifestFile) *apierr.Error {
	if !coverExts[strings.ToLower(path.Ext(p))] {
		return apierr.Invalid("cover must be an svg, png, jpg or webp file")
	}
	for _, f := range files {
		if f.Path == p {
			return nil
		}
	}
	return apierr.Invalid("cover must name a file in this manifest: " + p)
}

func validateCoverStyle(s string) *apierr.Error {
	if s == "" || cover.Valid(s) {
		return nil
	}
	return apierr.Invalid("cover_style must be one of " + strings.Join(cover.Styles(), ", "))
}

// applyMetadata folds the manifest's labels onto the artifact and persists
// the result when anything changed. Omitted keys keep the current value.
func (s *server) applyMetadata(ctx context.Context, a store.Artifact, m manifest) (store.Artifact, *apierr.Error) {
	project, tags, coverPath, style := a.Project, a.Tags, a.CoverPath, a.CoverStyle
	if m.Project.Set {
		project = nil
		if !m.Project.Null {
			project = m.project
		}
	}
	if m.Tags.Set {
		tags = m.tags
	}
	if m.Cover.Set {
		coverPath = nil
		if !m.Cover.Null && m.Cover.Value != "" {
			v := m.Cover.Value
			coverPath = &v
		}
	}
	if m.CoverStyle != "" {
		style = m.CoverStyle
	}
	if tags == nil {
		tags = []string{}
	}
	if ptrEq(project, a.Project) && strEq(tags, a.Tags) && ptrEq(coverPath, a.CoverPath) && style == a.CoverStyle {
		return a, nil
	}
	if err := s.Store.SetMetadata(ctx, a.ID, project, tags, coverPath, style); err != nil {
		return a, apierr.Internal(err)
	}
	a.Project, a.Tags, a.CoverPath, a.CoverStyle = project, tags, coverPath, style
	return a, nil
}

func ptrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func strEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// galleryFields are the label fields shared by every artifact JSON shape.
func (s *server) galleryFields(a store.Artifact) map[string]any {
	style := a.CoverStyle
	if !cover.Valid(style) {
		style = cover.PickStyle(a.ID)
	}
	tags := a.Tags
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{
		"project": a.Project, "tags": tags, "cover": a.CoverPath,
		"cover_url": s.artifactViewURL(a) + "_cover.svg", "cover_style": style, "visibility": a.Visibility,
	}
}

// listProjects serves GET /api/projects: the workspace's project labels with
// live artifact counts, for filter chips.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	rows, err := s.Store.Projects(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, p := range rows {
		out = append(out, map[string]any{"name": p.Name, "count": p.Count})
	}
	apierr.WriteJSON(w, 200, map[string]any{"projects": out})
}
