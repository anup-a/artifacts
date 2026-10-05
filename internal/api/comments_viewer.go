package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

func (s *server) viewerLimit(w http.ResponseWriter, r *http.Request) bool {
	ok, retry := s.CommentLimiter.Allow("ip:" + clientIP(r, s.Cfg.TrustProxyHeaders))
	if !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
	}
	return ok
}

// viewerName is the author a viewer comment is signed with. On a restricted
// page that is the address the visitor proved, whatever name they typed, and
// the comment is marked verified so a typed name can never pass for one.
func (s *server) viewerName(r *http.Request, a store.Artifact, typed string) (string, bool, *apierr.Error) {
	if email := serve.VisitorEmail(r, a, s.Cfg.KeySecret); email != "" {
		return email, true, nil
	}
	name, e := s.validName(typed)
	return name, false, e
}

func (s *server) viewerCreateThread(w http.ResponseWriter, r *http.Request) {
	if !s.viewerLimit(w, r) {
		return
	}
	a, e := s.loadArtifactForViewer(r, r.PathValue("id"))
	if e == nil {
		e = guestThreadCheck(a)
	}
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var in struct {
		AuthorName string          `json:"author_name"`
		Body       string          `json:"body"`
		Anchor     json.RawMessage `json:"anchor"`
		Version    int             `json:"version"`
		Suggestion string          `json:"suggestion"`
	}
	if e := decodeJSON(w, r, int64(s.Cfg.Limits.AnchorBytes+2*s.Cfg.Limits.CommentBytes+1024), &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	name, verified, e := s.viewerName(r, a, in.AuthorName)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	body, e := s.validBody(in.Body)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	anchor, e := s.validAnchor(in.Anchor)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if ok, err := s.versionExists(r, a.ID, in.Version); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	} else if !ok {
		apierr.Write(w, r, apierr.Invalid("version does not exist"))
		return
	}
	idemKey := r.Header.Get("Idempotency-Key")
	if len(idemKey) > 128 {
		apierr.Write(w, r, apierr.Invalid("Idempotency-Key too long"))
		return
	}
	now := s.Now()
	if idemKey != "" {
		if tid, ok := s.idem.get(a.ID+"|"+idemKey, now); ok {
			v, e := s.threadView(r, tid)
			if e != nil {
				apierr.Write(w, r, e)
				return
			}
			apierr.WriteJSON(w, 201, v)
			return
		}
	}
	suggestion, e := s.validSuggestion(in.Suggestion, anchor)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	th := store.Thread{ID: newThreadID(), ArtifactID: a.ID, Version: in.Version, AnchorJSON: anchor, Status: "open", AnchorStatus: "unknown", CreatedAt: now, Suggestion: suggestion}
	c := store.Comment{ID: newCommentID(), ThreadID: th.ID, AuthorName: name, AuthorKind: "viewer", Body: body, CreatedAt: now, Verified: verified}
	if err := s.Store.CreateThread(r.Context(), th, c); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if idemKey != "" {
		s.idem.put(a.ID+"|"+idemKey, th.ID, now)
	}
	v, e := s.threadView(r, th.ID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 201, v)
}

// viewerThread loads a thread and checks it belongs to the artifact in the path.
func (s *server) viewerThread(r *http.Request) (store.Artifact, store.Thread, *apierr.Error) {
	a, e := s.loadArtifactForViewer(r, r.PathValue("id"))
	if e != nil {
		return a, store.Thread{}, e
	}
	t, err := s.Store.Thread(r.Context(), r.PathValue("thread"))
	if err != nil || t.ArtifactID != a.ID {
		return a, t, apierr.NotFound("thread")
	}
	return a, t, nil
}

func (s *server) viewerReply(w http.ResponseWriter, r *http.Request) {
	if !s.viewerLimit(w, r) {
		return
	}
	a, t, e := s.viewerThread(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var in struct {
		AuthorName string `json:"author_name"`
		Body       string `json:"body"`
	}
	if e := decodeJSON(w, r, int64(s.Cfg.Limits.CommentBytes+512), &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	name, verified, e := s.viewerName(r, a, in.AuthorName)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	body, e := s.validBody(in.Body)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	c := store.Comment{ID: newCommentID(), ThreadID: t.ID, AuthorName: name, AuthorKind: "viewer", Body: body, CreatedAt: s.Now(), Verified: verified}
	if err := s.Store.AddComment(r.Context(), c); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 201, commentView{c.ID, c.AuthorName, c.AuthorKind, c.Body, c.CreatedAt, c.Verified})
}

func (s *server) viewerSeen(w http.ResponseWriter, r *http.Request) {
	if !s.viewerLimit(w, r) {
		return
	}
	a, t, e := s.viewerThread(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var in struct {
		Version      int    `json:"version"`
		AnchorStatus string `json:"anchor_status"`
	}
	if e := decodeJSON(w, r, 512, &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	valid := map[string]bool{"attached": true, "drifted": true, "orphaned": true}
	if valid[in.AnchorStatus] && in.Version == a.CurrentVersion {
		if err := s.Store.SetAnchorStatus(r.Context(), t.ID, in.AnchorStatus); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	}
	w.WriteHeader(204)
}

func (s *server) viewerList(w http.ResponseWriter, r *http.Request) {
	a, e := s.loadArtifactForViewer(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	// Viewers may only ask for open threads or all of them (mirrors the owner
	// list, which additionally allows "resolved").
	status := "open"
	switch r.URL.Query().Get("status") {
	case "", "open":
	case "all":
		status = ""
	default:
		apierr.Write(w, r, apierr.Invalid("status must be open or all"))
		return
	}
	version := 0
	if v := r.URL.Query().Get("version"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			apierr.Write(w, r, apierr.Invalid("version must be a non-negative integer"))
			return
		}
		version = n
	}
	ts, err := s.Store.Threads(r.Context(), a.ID, status, time.Time{}, version)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	views, err := s.threadViews(r, ts)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	apierr.WriteJSON(w, 200, map[string]any{"threads": views})
}

// viewerMeta exposes the version list to anonymous viewers so the runtime can
// render a version switcher. The artifact id is the capability here, exactly as
// it is for the thread list; no publisher-only fields are included.
func (s *server) viewerMeta(w http.ResponseWriter, r *http.Request) {
	a, e := s.loadArtifactForViewer(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	vs, err := s.Store.Versions(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	ns := make([]int, 0, len(vs))
	for _, v := range vs {
		ns = append(ns, v.Version)
	}
	sort.Ints(ns)
	w.Header().Set("Cache-Control", "no-store")
	apierr.WriteJSON(w, 200, map[string]any{"current_version": a.CurrentVersion, "versions": ns})
}
