package api

import (
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/store"
)

func (s *server) ownerListComments(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "open":
		status = "open"
	case "resolved":
	case "all":
		status = ""
	default:
		apierr.Write(w, r, apierr.Invalid("status must be open, resolved or all"))
		return
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			apierr.Write(w, r, apierr.Invalid("since must be RFC3339"))
			return
		}
		since = ts
	}
	ts, err := s.Store.Threads(r.Context(), a.ID, status, since, 0)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	views, err := s.threadViews(r, ts)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"threads": views})
}

func (s *server) ownerThread(r *http.Request) (store.Thread, *apierr.Error) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		return store.Thread{}, e
	}
	t, err := s.Store.Thread(r.Context(), r.PathValue("thread"))
	if err != nil || t.ArtifactID != a.ID {
		return t, apierr.NotFound("thread")
	}
	return t, nil
}

func (s *server) ownerReply(w http.ResponseWriter, r *http.Request) {
	t, e := s.ownerThread(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if e := decodeJSON(w, r, int64(s.Cfg.Limits.CommentBytes+512), &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	body, e := s.validBody(in.Body)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	c := store.Comment{ID: newCommentID(), ThreadID: t.ID, AuthorName: "Author", AuthorKind: "owner", Body: body, CreatedAt: s.Now()}
	if err := s.Store.AddComment(r.Context(), c); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 201, commentView{c.ID, c.AuthorName, c.AuthorKind, c.Body, c.CreatedAt, c.Verified})
}

func (s *server) ownerResolve(w http.ResponseWriter, r *http.Request) {
	t, e := s.ownerThread(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if t.Status != "resolved" {
		if err := s.Store.ResolveThread(r.Context(), t.ID, s.Now()); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	}
	v, e := s.threadView(r, t.ID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, v)
}

// ownerProgress answers POST .../comments/{thread}/progress with
// {"progress": "seen"|"working"|"declined", "note": "..."}. The note, when
// given, is posted as an owner reply. Declining needs a note, since a reader
// is owed a reason, and also resolves the thread.
func (s *server) ownerProgress(w http.ResponseWriter, r *http.Request) {
	t, e := s.ownerThread(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var in struct {
		Progress string `json:"progress"`
		Note     string `json:"note"`
	}
	if e := decodeJSON(w, r, int64(s.Cfg.Limits.CommentBytes+512), &in); e != nil {
		apierr.Write(w, r, e)
		return
	}
	switch in.Progress {
	case "seen", "working", "declined":
	default:
		apierr.Write(w, r, apierr.Invalid("progress must be seen, working or declined"))
		return
	}
	var note string
	if in.Note != "" || in.Progress == "declined" {
		if note, e = s.validBody(in.Note); e != nil {
			if in.Progress == "declined" && in.Note == "" {
				e = apierr.Invalid("declining needs a note saying why")
			}
			apierr.Write(w, r, e)
			return
		}
	}
	ctx := r.Context()
	if err := s.Store.SetThreadProgress(ctx, t.ID, in.Progress); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if note != "" {
		c := store.Comment{ID: newCommentID(), ThreadID: t.ID, AuthorName: "Author", AuthorKind: "owner", Body: note, CreatedAt: s.Now()}
		if err := s.Store.AddComment(ctx, c); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	}
	if in.Progress == "declined" && t.Status != "resolved" {
		if err := s.Store.ResolveThread(ctx, t.ID, s.Now()); err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
	}
	v, e := s.threadView(r, t.ID)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	apierr.WriteJSON(w, 200, v)
}
