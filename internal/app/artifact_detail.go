package app

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/store"
)

// versionView is one row of the Versions section.
type versionView struct {
	N         int
	CreatedAt time.Time
	Bytes     int64
	FileCount int
	Current   bool
	Comments  int
}

// threadView is one comment thread with its comments and the anchor's quote.
type threadView struct {
	store.Thread
	Quote    string
	Comments []store.Comment
}

type artifactData struct {
	Artifact  store.Artifact
	URL       string
	Versions  []versionView
	Open      []threadView
	Resolved  []threadView
	Error     string
	ErrThread string // thread the Error belongs to, so it renders inline
}

func (h *Handler) artifactDetail(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.renderArtifact(w, r, 200, v, r.PathValue("id"), "", "")
}

func (h *Handler) renderArtifact(w http.ResponseWriter, r *http.Request, status int, v *viewer, id, errThread, msg string) {
	ctx := r.Context()
	a, err := h.ownedArtifact(r, v, id)
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	vs, err := h.Store.Versions(ctx, a.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	counts, err := h.Store.CountThreadsByVersion(ctx, a.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := artifactData{Artifact: a, URL: h.shareURL(a.ID), Error: msg, ErrThread: errThread}
	for i := len(vs) - 1; i >= 0; i-- { // newest first
		vv := vs[i]
		d.Versions = append(d.Versions, versionView{N: vv.Version, CreatedAt: vv.CreatedAt, Bytes: vv.Bytes, FileCount: vv.FileCount,
			Current: vv.Version == a.CurrentVersion, Comments: counts[vv.Version]})
	}
	threads, err := h.Store.Threads(ctx, a.ID, "", time.Time{}, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	tids := make([]string, 0, len(threads))
	for _, t := range threads {
		tids = append(tids, t.ID)
	}
	comments, err := h.Store.Comments(ctx, tids)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for _, t := range threads {
		tv := threadView{Thread: t, Quote: anchorQuote(t.AnchorJSON), Comments: comments[t.ID]}
		if t.Status == "resolved" {
			d.Resolved = append(d.Resolved, tv)
		} else {
			d.Open = append(d.Open, tv)
		}
	}
	// Newest open thread first; resolved keep creation order under the fold.
	for i, j := 0, len(d.Open)-1; i < j; i, j = i+1, j-1 {
		d.Open[i], d.Open[j] = d.Open[j], d.Open[i]
	}
	title := a.Title
	if title == "" {
		title = a.ID
	}
	h.render(w, r, status, "artifact", title, v, d)
}

// anchorQuote pulls the quoted text out of a text anchor, or the selector of
// an element anchor, for display next to the thread.
func anchorQuote(raw string) string {
	var a struct {
		Kind  string `json:"kind"`
		Quote string `json:"quote"`
		CSS   string `json:"css"`
	}
	if json.Unmarshal([]byte(raw), &a) != nil {
		return ""
	}
	if a.Kind == "element" {
		return a.CSS
	}
	return a.Quote
}

// rollbackArtifact republishes an older version's files as a new version,
// the same way the API's rollback does, so viewers and the CLI see one
// linear history.
func (h *Handler) rollbackArtifact(w http.ResponseWriter, r *http.Request, v *viewer) {
	ctx := r.Context()
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	target, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || target < 1 {
		h.renderError(w, r, 404, "That version does not exist.")
		return
	}
	if target == a.CurrentVersion {
		http.Redirect(w, r, "/app/artifacts/"+a.ID, http.StatusFound)
		return
	}
	files, err := h.Store.Files(ctx, a.ID, target)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(files) == 0 {
		h.renderError(w, r, 404, "That version does not exist.")
		return
	}
	var total int64
	for _, f := range files {
		total += f.Size
	}
	nv := store.Version{ArtifactID: a.ID, Version: a.CurrentVersion + 1, CreatedAt: h.Now(), Bytes: total, FileCount: len(files)}
	if err := h.Store.AddVersion(ctx, nv, files); err != nil {
		h.fail(w, r, err)
		return
	}
	keep := h.Cfg.Limits.VersionsKept
	if keep <= 0 {
		keep = defaultVersionsKept
	}
	if _, err := h.Store.PruneVersions(ctx, a.ID, keep); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/artifacts/"+a.ID, http.StatusFound)
}

// ownedThread loads a thread that belongs to one of the viewer's artifacts.
func (h *Handler) ownedThread(r *http.Request, v *viewer) (store.Artifact, store.Thread, error) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		return a, store.Thread{}, err
	}
	t, err := h.Store.Thread(r.Context(), r.PathValue("thread"))
	if err != nil {
		return a, t, err
	}
	if t.ArtifactID != a.ID {
		return a, t, store.ErrNotFound
	}
	return a, t, nil
}

func (h *Handler) resolveThread(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, t, err := h.ownedThread(r, v)
	if err != nil {
		h.storeErr(w, r, err, "That comment thread does not exist.")
		return
	}
	if t.Status != "resolved" {
		if err := h.Store.ResolveThread(r.Context(), t.ID, h.Now()); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/app/artifacts/"+a.ID+"#"+t.ID, http.StatusFound)
}

func (h *Handler) replyThread(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, t, err := h.ownedThread(r, v)
	if err != nil {
		h.storeErr(w, r, err, "That comment thread does not exist.")
		return
	}
	max := h.Cfg.Limits.CommentBytes
	if max <= 0 {
		max = defaultCommentBytes
	}
	body, ok := cleanBody(r.PostForm.Get("body"), max)
	if !ok {
		h.renderArtifact(w, r, 400, v, a.ID, t.ID, "Write a reply of 1 to "+strconv.Itoa(max)+" characters.")
		return
	}
	author := strings.TrimSpace(v.User.Name)
	if author == "" {
		author = v.User.Email
	}
	c := store.Comment{ID: "cm_" + ids.NewArtifactID(), ThreadID: t.ID, AuthorName: author, AuthorKind: "owner", Body: body, CreatedAt: h.Now()}
	if err := h.Store.AddComment(r.Context(), c); err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/artifacts/"+a.ID+"#"+t.ID, http.StatusFound)
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// Defaults matching the API's when the config carries no limits (tests, or
// a handler built without config.Load).
const (
	defaultCommentBytes = 2048
	defaultVersionsKept = 20
)

// cleanBody applies the API's comment rules: no tags, no control characters
// other than newlines and tabs, trimmed, 1..max bytes.
func cleanBody(raw string, max int) (string, bool) {
	for _, c := range raw {
		if unicode.IsControl(c) && c != '\n' && c != '\t' && c != '\r' {
			return "", false
		}
	}
	body := strings.TrimSpace(tagRe.ReplaceAllString(raw, ""))
	if body == "" || len(body) > max {
		return "", false
	}
	return body, true
}
