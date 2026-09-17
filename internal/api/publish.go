package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/blob"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

func (s *server) artifactURL(id string) string { return s.Cfg.ShareOrigin + "/a/" + id + "/" }

func (s *server) artifactViewURL(a store.Artifact) string {
	if a.Visibility == "private" {
		return s.Cfg.APIOrigin + "/app/view/" + a.ID + "/"
	}
	return s.artifactURL(a.ID)
}

// ownedArtifact loads the artifact and checks ownership. Deleted → GONE.
func (s *server) ownedArtifact(r *http.Request, id string) (store.Artifact, *apierr.Error) {
	k, _ := KeyFrom(r.Context())
	a, err := s.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return a, apierr.NotFound("artifact")
	}
	if err != nil {
		return a, apierr.Internal(err)
	}
	if a.TenantID != k.TenantID {
		return a, apierr.NotFound("artifact") // do not reveal existence
	}
	if a.DeletedAt != nil {
		return a, apierr.Gone()
	}
	return a, nil
}

func (s *server) preflight(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		apierr.Write(w, r, apierr.TooLarge("manifest too large"))
		return
	}
	m, e := parseManifest(raw, s.Cfg.Limits)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	missing, e := s.missingBlobs(r.Context(), m)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"missing": missing})
}

// missingBlobErr formats the shared "missing blob" INVALID_ARGUMENT error used
// by both the create and update/rollback publish paths.
func missingBlobErr(missing []string) *apierr.Error {
	return apierr.Invalid(fmt.Sprintf("missing blob %s (and %d more): upload it in this request", missing[0], len(missing)-1))
}

func (s *server) missingBlobs(ctx context.Context, m manifest) ([]string, *apierr.Error) {
	shas := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		shas = append(shas, f.SHA256)
	}
	have, err := s.Store.HasBlobs(ctx, shas)
	if err != nil {
		return nil, apierr.Internal(err)
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, sha := range shas {
		if !have[sha] && !seen[sha] {
			missing = append(missing, sha)
			seen[sha] = true
		}
	}
	return missing, nil
}

// readPublish parses the multipart body: manifest first, then blob parts keyed by sha.
// Blob parts are written to blob storage as they stream, so the returned set is what was uploaded.
func (s *server) readPublish(w http.ResponseWriter, r *http.Request) (manifest, map[string]bool, *apierr.Error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.Limits.VersionBytes+1<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		return manifest{}, nil, apierr.Invalid("body must be multipart/form-data")
	}
	first, err := mr.NextPart()
	if err != nil || first.FormName() != "manifest" {
		return manifest{}, nil, apierr.Invalid("first part must be named manifest")
	}
	raw, err := io.ReadAll(io.LimitReader(first, 1<<20))
	if err != nil {
		return manifest{}, nil, apierr.Invalid("manifest unreadable")
	}
	m, e := parseManifest(raw, s.Cfg.Limits)
	if e != nil {
		return m, nil, e
	}
	sizes := map[string]int64{}
	for _, f := range m.Files {
		sizes[f.SHA256] = f.Size
	}
	uploaded := map[string]bool{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return m, nil, apierr.TooLarge("request body exceeds the version size limit")
			}
			return m, nil, apierr.Invalid("malformed multipart body")
		}
		if e := s.storePart(r.Context(), p, sizes, uploaded); e != nil {
			return m, nil, e
		}
	}
	return m, uploaded, nil
}

func (s *server) storePart(ctx context.Context, p *multipart.Part, sizes map[string]int64, uploaded map[string]bool) *apierr.Error {
	sha := p.FormName()
	size, ok := sizes[sha]
	if !ok {
		return apierr.Invalid("part " + sha + " is not in the manifest")
	}
	if uploaded[sha] {
		io.Copy(io.Discard, p)
		return nil
	}
	if err := s.Blobs.Put(ctx, sha, p, size); err != nil {
		if errors.Is(err, blob.ErrChecksum) {
			return apierr.Invalid("checksum mismatch for part " + sha)
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return apierr.TooLarge("request body exceeds the version size limit")
		}
		return apierr.Internal(err)
	}
	if err := s.Store.UpsertBlob(ctx, sha, size); err != nil {
		return apierr.Internal(err)
	}
	uploaded[sha] = true
	return nil
}

// publishVersion verifies every manifest blob exists, then records the version
// and applies the manifest's title and comments switch. It returns the artifact
// as it stands afterwards.
func (s *server) publishVersion(ctx context.Context, a store.Artifact, m manifest) (store.Artifact, int, *apierr.Error) {
	missing, e := s.missingBlobs(ctx, m)
	if e != nil {
		return a, 0, e
	}
	if len(missing) > 0 {
		return a, 0, missingBlobErr(missing)
	}
	files := make([]store.File, 0, len(m.Files))
	var total int64
	for _, f := range m.Files {
		files = append(files, store.File{Path: f.Path, SHA256: f.SHA256, Size: f.Size, ContentType: contentTypeFor(f.Path)})
		total += f.Size
	}
	v := store.Version{ArtifactID: a.ID, Version: a.CurrentVersion + 1, CreatedAt: s.Now(), Bytes: total, FileCount: len(files)}
	// Restrict access before storing any new content. If publishing later fails,
	// the artifact stays private rather than exposing the attempted update.
	if m.Visibility != nil && *m.Visibility == "private" && a.Visibility != "private" {
		if err := s.Store.SetVisibility(ctx, a.ID, "private"); err != nil {
			return a, 0, apierr.Internal(err)
		}
		a.Visibility = "private"
	}
	if err := s.Store.AddVersion(ctx, v, files); err != nil {
		if errors.Is(err, store.ErrBlobMissing) {
			return a, 0, apierr.Invalid("missing blob")
		}
		return a, 0, apierr.Internal(err)
	}
	if m.Title != "" && m.Title != a.Title {
		if err := s.Store.UpdateTitle(ctx, a.ID, m.Title); err != nil {
			return a, 0, apierr.Internal(err)
		}
		a.Title = m.Title
	}
	if m.CommentsEnabled != nil && *m.CommentsEnabled != a.CommentsEnabled {
		if err := s.Store.SetCommentsEnabled(ctx, a.ID, *m.CommentsEnabled); err != nil {
			return a, 0, apierr.Internal(err)
		}
		a.CommentsEnabled = *m.CommentsEnabled
	}
	a, e = s.applyMetadata(ctx, a, m)
	if e != nil {
		return a, 0, e
	}
	if _, err := s.Store.PruneVersions(ctx, a.ID, s.Cfg.Limits.VersionsKept); err != nil {
		return a, 0, apierr.Internal(err)
	}
	// Making content public is the final step, only after all publish mutations succeed.
	if m.Visibility != nil && *m.Visibility == "public" && a.Visibility != "public" {
		if err := s.Store.SetVisibility(ctx, a.ID, "public"); err != nil {
			return a, 0, apierr.Internal(err)
		}
		a.Visibility = "public"
	}
	a.CurrentVersion = v.Version
	return a, v.Version, nil
}

func (s *server) createArtifact(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if ok, retry := s.PublishLimiter.Allow(fmt.Sprint("key:", k.ID)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	external, e := endUserFrom(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	n, err := s.Store.CountArtifacts(r.Context(), k.TenantID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if n >= s.Cfg.Limits.ArtifactsPerTenant {
		apierr.Write(w, r, apierr.Quota(fmt.Sprintf("artifact limit %d reached; delete one or upgrade", s.Cfg.Limits.ArtifactsPerTenant)))
		return
	}
	m, _, e := s.readPublish(w, r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	missing, e := s.missingBlobs(r.Context(), m)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if len(missing) > 0 {
		apierr.Write(w, r, missingBlobErr(missing))
		return
	}
	now := s.Now()
	// CommentsEnabled mirrors the column default; the manifest may switch it off below.
	a := store.Artifact{ID: ids.NewArtifactID(), KeyID: k.ID, TenantID: k.TenantID, Title: m.Title, Visibility: "public", CommentsEnabled: true, CreatedAt: now, UpdatedAt: now}
	if m.Visibility != nil {
		a.Visibility = *m.Visibility
	}
	a, e = s.attachEndUser(r, a, k.TenantID, external)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if err := s.Store.CreateArtifact(r.Context(), a); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	a, v, e := s.publishVersion(r.Context(), a, m)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 201, s.publishResponse(a, v))
}

func (s *server) publishResponse(a store.Artifact, version int) map[string]any {
	out := s.galleryFields(a)
	out["id"], out["url"], out["version"], out["end_user"], out["comments_enabled"] = a.ID, s.artifactViewURL(a), version, a.EndUser, a.CommentsEnabled
	return out
}

func (s *server) updateArtifact(w http.ResponseWriter, r *http.Request) {
	k, _ := KeyFrom(r.Context())
	if ok, retry := s.PublishLimiter.Allow(fmt.Sprint("key:", k.ID)); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return
	}
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	external, e := endUserFrom(r)
	if e == nil {
		e = checkEndUser(a, external)
	}
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	m, _, e := s.readPublish(w, r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	a, v, e := s.publishVersion(r.Context(), a, m)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, s.publishResponse(a, v))
}
