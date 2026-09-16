package app

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anup-a/artifacts/internal/cover"
	"github.com/anup-a/artifacts/internal/store"
)

// Limits on the gallery labels, the same as the API's.
const (
	maxProjectChars = 64
	maxTags         = 10
	maxTagChars     = 32
)

// coverExts are the image types the cover endpoint streams.
var coverExts = map[string]bool{".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// metaForm is the About card's state: what the inputs show, taken from the
// artifact on GET and echoed back from the submission on a validation error.
type metaForm struct {
	Project    string
	Tags       string // comma separated
	CoverKind  string // "generated" or "file"
	CoverStyle string
	CoverFile  string
}

func metaFromArtifact(a store.Artifact) metaForm {
	m := metaForm{Project: deref(a.Project), Tags: strings.Join(a.Tags, ", "), CoverKind: "generated", CoverStyle: a.CoverStyle}
	if m.CoverStyle == "" || !cover.Valid(m.CoverStyle) {
		m.CoverStyle = cover.PickStyle(a.ID)
	}
	if a.CoverPath != nil {
		m.CoverKind, m.CoverFile = "file", *a.CoverPath
	}
	return m
}

func metaFromForm(r *http.Request) metaForm {
	f := r.PostForm
	return metaForm{Project: f.Get("project"), Tags: f.Get("tags"), CoverKind: f.Get("cover_kind"), CoverStyle: f.Get("cover_style"), CoverFile: f.Get("cover_file")}
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// cleanProject mirrors the API: trimmed, 1 to 64 characters, no control
// characters; empty means no project.
func cleanProject(raw string) (*string, string) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return nil, ""
	}
	if !utf8.ValidString(p) || hasControl(p) {
		return nil, "project must not contain control characters"
	}
	if utf8.RuneCountInString(p) > maxProjectChars {
		return nil, fmt.Sprintf("project must be 1 to %d characters", maxProjectChars)
	}
	return &p, ""
}

// cleanTags splits the comma-separated field, drops empty entries (a
// trailing comma is not a tag), then applies the API's rules: lowercased,
// trimmed, deduplicated, at most 10 of up to 32 characters.
func cleanTags(raw string) ([]string, string) {
	out := []string{}
	seen := map[string]bool{}
	for i, t := range strings.Split(raw, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagChars {
			return nil, fmt.Sprintf("tags[%d] must be 1 to %d characters", i, maxTagChars)
		}
		if !utf8.ValidString(t) || hasControl(t) {
			return nil, fmt.Sprintf("tags[%d] must not contain control characters", i)
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, fmt.Sprintf("tags: at most %d allowed", maxTags)
	}
	return out, ""
}

// coverFiles lists the image files of the current version, the choices for
// a publisher-file cover.
func coverFiles(files []store.File) []string {
	out := []string{}
	for _, f := range files {
		if coverExts[strings.ToLower(path.Ext(f.Path))] {
			out = append(out, f.Path)
		}
	}
	return out
}

// cleanCover validates the cover choice against the current version's files
// and the generator's style names.
func cleanCover(m metaForm, files []string) (coverPath *string, style string, msg string) {
	switch m.CoverKind {
	case "file":
		p := m.CoverFile
		if !coverExts[strings.ToLower(path.Ext(p))] {
			return nil, "", "cover must be an svg, png, jpg or webp file"
		}
		for _, f := range files {
			if f == p {
				return &p, m.CoverStyle, ""
			}
		}
		return nil, "", "cover must name a file in the current version: " + p
	case "generated", "":
		if m.CoverStyle != "" && !cover.Valid(m.CoverStyle) {
			return nil, "", "cover_style must be one of " + strings.Join(cover.Styles(), ", ")
		}
		return nil, m.CoverStyle, ""
	}
	return nil, "", "The form could not be read."
}

// setMetadata is the About card's save: POST /app/artifacts/{id}/meta with
// project, tags, cover_kind, cover_style and cover_file. Validation errors
// re-render the page (or the panel fragment) with the message beside the
// form and the typed values kept.
func (h *Handler) setMetadata(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	m := metaFromForm(r)
	fail := func(msg string) {
		h.renderArtifactWith(w, r, 400, v, a.ID, artifactOpts{MetaError: msg, Meta: &m})
	}
	project, msg := cleanProject(m.Project)
	if msg != "" {
		fail(msg)
		return
	}
	tags, msg := cleanTags(m.Tags)
	if msg != "" {
		fail(msg)
		return
	}
	files, err := h.Store.Files(r.Context(), a.ID, a.CurrentVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	coverPath, style, msg := cleanCover(m, coverFiles(files))
	if msg != "" {
		fail(msg)
		return
	}
	if err := h.Store.SetMetadata(r.Context(), a.ID, project, tags, coverPath, style); err != nil {
		h.fail(w, r, err)
		return
	}
	h.backToArtifact(w, r, v, a.ID, "#about")
}
