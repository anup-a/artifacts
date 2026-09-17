// Package store persists keys, artifacts, versions, files, blobs and comments.
package store

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

// ErrBlobMissing is returned by AddVersion when a manifest file references a
// sha256 with no blobs row, so the refcount increment would silently vanish
// and the version would point at bytes nothing keeps alive.
var ErrBlobMissing = errors.New("blob missing")

type Key struct {
	ID         int64
	TenantID   string
	Name       string
	Prefix     string
	LastUsedAt *time.Time
	Email      string
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

type Artifact struct {
	ID             string
	KeyID          int64
	TenantID       string
	EndUserID      *string
	EndUser        *string
	OpenComments   int
	Title          string
	CurrentVersion int
	// Visibility is public or private. CreateArtifact defaults an empty value to public.
	Visibility string
	// ShareCode, when set on a private artifact, lets anyone holding it read
	// the artifact without signing in. Empty means the artifact is reachable
	// only as its visibility allows. Never render it to a viewer: it is the
	// credential itself, not a label.
	ShareCode string
	// CommentsEnabled is whether viewers get the comment runtime and the
	// /c/* routes. New artifacts start enabled; CreateArtifact ignores this
	// field and SetCommentsEnabled changes it.
	CommentsEnabled bool
	// Project and Tags are publisher-supplied labels for the gallery. Tags is
	// never nil when read back: an artifact without tags has an empty slice.
	Project *string
	Tags    []string
	// CoverPath names a file inside the current version to serve as the cover;
	// nil means the server generates one. CoverStyle is the generator name the
	// publisher asked for, or "" to derive one from the id.
	CoverPath  *string
	CoverStyle string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// ProjectCount is one row of Projects: a project label and how many live
// artifacts carry it.
type ProjectCount struct {
	Name  string
	Count int
}

type Version struct {
	ArtifactID string
	Version    int
	CreatedAt  time.Time
	Bytes      int64
	FileCount  int
}

type File struct {
	Path        string
	SHA256      string
	Size        int64
	ContentType string
}

type Thread struct {
	ID           string
	ArtifactID   string
	Version      int
	AnchorJSON   string
	Status       string // open | resolved
	AnchorStatus string // unknown | attached | drifted | orphaned
	CreatedAt    time.Time
	ResolvedAt   *time.Time
}

type Comment struct {
	ID         string
	ThreadID   string
	AuthorName string
	AuthorKind string // viewer | owner
	Body       string
	CreatedAt  time.Time
}

type Store interface {
	TenantStore
	AuthStore
	Migrate(ctx context.Context) error
	CreateKey(ctx context.Context, hash, email string) (Key, error)
	KeyByHash(ctx context.Context, hash string) (Key, error)
	CreateArtifact(ctx context.Context, a Artifact) error
	Artifact(ctx context.Context, id string) (Artifact, error)
	ListArtifacts(ctx context.Context, tenantID string, filter ArtifactFilter) ([]Artifact, error)
	CountArtifacts(ctx context.Context, tenantID string) (int, error)
	UpdateTitle(ctx context.Context, id, title string) error
	SetCommentsEnabled(ctx context.Context, id string, on bool) error
	SetVisibility(ctx context.Context, id, visibility string) error
	SetShareCode(ctx context.Context, id, code string) error
	// SetMetadata replaces project, tags, cover path and cover style without
	// touching updated_at: labels are not content.
	SetMetadata(ctx context.Context, id string, project *string, tags []string, coverPath *string, coverStyle string) error
	// Projects lists the distinct project labels of a tenant's live artifacts
	// with their counts, most used first.
	Projects(ctx context.Context, tenantID string) ([]ProjectCount, error)
	SoftDelete(ctx context.Context, id string, at time.Time) error
	AddVersion(ctx context.Context, v Version, files []File) error
	Versions(ctx context.Context, artifactID string) ([]Version, error)
	Files(ctx context.Context, artifactID string, version int) ([]File, error)
	PruneVersions(ctx context.Context, artifactID string, keep int) ([]int, error)
	UpsertBlob(ctx context.Context, sha string, size int64) error
	HasBlobs(ctx context.Context, shas []string) (map[string]bool, error)
	ZeroRefBlobs(ctx context.Context, olderThan time.Time) ([]string, error)
	DeleteBlob(ctx context.Context, sha string) error
	CreateThread(ctx context.Context, t Thread, first Comment) error
	Thread(ctx context.Context, id string) (Thread, error)
	Threads(ctx context.Context, artifactID, status string, since time.Time, version int) ([]Thread, error)
	Comments(ctx context.Context, threadIDs []string) (map[string][]Comment, error)
	AddComment(ctx context.Context, c Comment) error
	ResolveThread(ctx context.Context, id string, at time.Time) error
	ResolveAllThreads(ctx context.Context, artifactID string, at time.Time) error
	SetAnchorStatus(ctx context.Context, threadID, status string) error
	CountOpenThreads(ctx context.Context, artifactID string) (int, error)
	CountThreadsByVersion(ctx context.Context, artifactID string) (map[int]int, error)
	Ping(ctx context.Context) error
	Close() error
}
