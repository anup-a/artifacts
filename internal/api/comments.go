package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

type commentView struct {
	ID         string    `json:"id"`
	AuthorName string    `json:"author_name"`
	AuthorKind string    `json:"author_kind"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type threadView struct {
	ID           string          `json:"id"`
	Version      int             `json:"version"`
	Status       string          `json:"status"`
	AnchorStatus string          `json:"anchor_status"`
	Anchor       json.RawMessage `json:"anchor"`
	CreatedAt    time.Time       `json:"created_at"`
	ResolvedAt   *time.Time      `json:"resolved_at"`
	Comments     []commentView   `json:"comments"`
}

func toThreadViews(ts []store.Thread, cs map[string][]store.Comment) []threadView {
	out := make([]threadView, 0, len(ts))
	for _, t := range ts {
		v := threadView{ID: t.ID, Version: t.Version, Status: t.Status, AnchorStatus: t.AnchorStatus, Anchor: json.RawMessage(t.AnchorJSON), CreatedAt: t.CreatedAt, ResolvedAt: t.ResolvedAt, Comments: []commentView{}}
		for _, c := range cs[t.ID] {
			v.Comments = append(v.Comments, commentView{c.ID, c.AuthorName, c.AuthorKind, c.Body, c.CreatedAt})
		}
		out = append(out, v)
	}
	return out
}

func (s *server) threadViews(r *http.Request, ts []store.Thread) ([]threadView, error) {
	idsList := make([]string, 0, len(ts))
	for _, t := range ts {
		idsList = append(idsList, t.ID)
	}
	cs, err := s.Store.Comments(r.Context(), idsList)
	if err != nil {
		return nil, err
	}
	return toThreadViews(ts, cs), nil
}

func (s *server) threadView(r *http.Request, id string) (threadView, *apierr.Error) {
	t, err := s.Store.Thread(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return threadView{}, apierr.NotFound("thread")
	}
	if err != nil {
		return threadView{}, apierr.Internal(err)
	}
	vs, err := s.threadViews(r, []store.Thread{t})
	if err != nil {
		return threadView{}, apierr.Internal(err)
	}
	return vs[0], nil
}

func (s *server) validName(n string) (string, *apierr.Error) {
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > 80 {
		return "", apierr.Invalid("author_name must be 1..80 characters")
	}
	return n, nil
}

func (s *server) validBody(b string) (string, *apierr.Error) {
	b = strings.TrimSpace(b)
	if b == "" || len(b) > s.Cfg.Limits.CommentBytes {
		return "", apierr.Invalid("body must be 1.." + itoa(s.Cfg.Limits.CommentBytes) + " bytes")
	}
	return b, nil
}

func (s *server) validAnchor(raw json.RawMessage) (string, *apierr.Error) {
	if len(raw) == 0 || len(raw) > s.Cfg.Limits.AnchorBytes {
		return "", apierr.Invalid("anchor must be a JSON object up to " + itoa(s.Cfg.Limits.AnchorBytes) + " bytes")
	}
	var a struct {
		Kind  string `json:"kind"`
		Quote string `json:"quote"`
		CSS   string `json:"css"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", apierr.Invalid("anchor must be a JSON object")
	}
	switch a.Kind {
	case "text":
		if a.Quote == "" {
			return "", apierr.Invalid("text anchor requires quote")
		}
	case "element":
		if a.CSS == "" {
			return "", apierr.Invalid("element anchor requires css")
		}
	default:
		return "", apierr.Invalid("anchor.kind must be text or element")
	}
	return string(raw), nil
}

// loadArtifactForViewer returns the artifact for /c/* routes (no ownership check).
func (s *server) loadArtifactForViewer(r *http.Request, id string) (store.Artifact, *apierr.Error) {
	a, err := s.Store.Artifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return a, apierr.NotFound("artifact")
	}
	if err != nil {
		return a, apierr.Internal(err)
	}
	if a.DeletedAt != nil {
		return a, apierr.Gone()
	}
	return a, nil
}

func (s *server) versionExists(r *http.Request, artifactID string, v int) (bool, error) {
	vs, err := s.Store.Versions(r.Context(), artifactID)
	if err != nil {
		return false, err
	}
	for _, x := range vs {
		if x.Version == v {
			return true, nil
		}
	}
	return false, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) *apierr.Error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(dst); err != nil {
		return apierr.Invalid("body must be valid JSON")
	}
	return nil
}

func newThreadID() string  { return "th_" + ids.NewArtifactID() }
func newCommentID() string { return "cm_" + ids.NewArtifactID() }

func itoa(n int) string { return strconvItoa(n) }

// idempotency remembers viewer thread creations for 10 minutes, per process.
type idempotency struct {
	mu    sync.Mutex
	items map[string]idemEntry
}

type idemEntry struct {
	threadID string
	at       time.Time
}

func (i *idempotency) get(key string, now time.Time) (string, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for k, e := range i.items {
		if now.Sub(e.at) > 10*time.Minute {
			delete(i.items, k)
		}
	}
	e, ok := i.items[key]
	return e.threadID, ok
}

func (i *idempotency) put(key, threadID string, now time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.items == nil {
		i.items = map[string]idemEntry{}
	}
	i.items[key] = idemEntry{threadID, now}
}
