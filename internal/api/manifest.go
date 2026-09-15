package api

import (
	"encoding/json"
	"fmt"
	"mime"
	"path"
	"regexp"
	"strings"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/config"
)

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifest struct {
	Title string         `json:"title"`
	Files []manifestFile `json:"files"`
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func parseManifest(raw []byte, lim config.Limits) (manifest, *apierr.Error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, apierr.Invalid("manifest is not valid JSON")
	}
	if len(m.Title) > 200 {
		return m, apierr.Invalid("title exceeds 200 characters")
	}
	if len(m.Files) == 0 {
		return m, apierr.Invalid("manifest has no files")
	}
	if len(m.Files) > lim.Files {
		return m, apierr.TooLarge(fmt.Sprintf("too many files: %d > %d", len(m.Files), lim.Files))
	}
	seen := map[string]bool{}
	var total int64
	hasIndex := false
	for i, f := range m.Files {
		if err := validPath(f.Path); err != "" {
			return m, apierr.Invalid(fmt.Sprintf("files[%d].path: %s", i, err))
		}
		if seen[f.Path] {
			return m, apierr.Invalid("duplicate path " + f.Path)
		}
		seen[f.Path] = true
		if !shaRe.MatchString(f.SHA256) {
			return m, apierr.Invalid(fmt.Sprintf("files[%d].sha256 must be 64 lowercase hex", i))
		}
		if f.Size < 0 {
			return m, apierr.Invalid(fmt.Sprintf("files[%d].size negative", i))
		}
		total += f.Size
		if f.Path == "index.html" {
			hasIndex = true
		}
	}
	if total > lim.VersionBytes {
		return m, apierr.TooLarge(fmt.Sprintf("version is %d bytes, limit %d", total, lim.VersionBytes))
	}
	if !hasIndex {
		return m, apierr.Invalid("index.html is required at the root")
	}
	return m, nil
}

func validPath(p string) string {
	if p == "" || len(p) > 512 {
		return "empty or too long"
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "must be relative with forward slashes"
	}
	if path.Clean(p) != p || strings.HasPrefix(p, "../") || p == ".." || strings.Contains(p, "/../") {
		return "must be a clean path without .."
	}
	return ""
}

func contentTypeFor(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".md", ".txt":
		return "text/plain; charset=utf-8"
	}
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
