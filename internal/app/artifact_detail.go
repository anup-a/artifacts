package app

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/anup-a/artifacts/internal/cover"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// versionView is one row of the Versions section.
type versionView struct {
	N         int
	CreatedAt time.Time
	Bytes     int64
	FileCount int
	Current   bool
	Comments  int
	// By is what produced the version: agent and model when reported, else
	// the publishing key's name. Key is that name, shown on hover.
	By, Key string
}

func versionBy(v store.Version) string {
	parts := []string{}
	for _, p := range []string{v.Agent, v.Model} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return v.KeyName
	}
	return strings.Join(parts, " · ")
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
	CoverURL  string
	Versions  []versionView
	Open      []threadView
	Resolved  []threadView
	Error     string
	ErrThread string // thread the Error belongs to, so it renders inline
	// About card: the form state, the workspace's project names for the
	// datalist, the generator styles and the current version's image files.
	Meta       metaForm
	MetaError  string
	Projects   []string
	Styles     []string
	CoverFiles []string
	// ShareLink is the one-click form of the share code, code included. It is
	// shown only to the owner, on this page, and is never stored anywhere a
	// viewer can read.
	ShareLink string
	// SendURL is the address to give somebody else, which is not always the
	// address the owner opens. A private artifact opens for its owner on the
	// account origin, but that link is useless to anyone outside the
	// workspace, so once a share code exists the link worth copying is the
	// public one carrying the code. SendLabel says which of those it is, so
	// the field never silently hands over a link that will not work.
	SendURL, SendLabel string
}

// artifactOpts is what a re-render after a form post carries besides the
// artifact: the message and where it belongs, and the About form's typed
// values when that form failed validation.
type artifactOpts struct {
	ErrThread, Error string
	MetaError        string
	Meta             *metaForm
}

func (h *Handler) artifactDetail(w http.ResponseWriter, r *http.Request, v *viewer) {
	h.renderArtifact(w, r, 200, v, r.PathValue("id"), "", "")
}

func (h *Handler) renderArtifact(w http.ResponseWriter, r *http.Request, status int, v *viewer, id, errThread, msg string) {
	h.renderArtifactWith(w, r, status, v, id, artifactOpts{ErrThread: errThread, Error: msg})
}

func (h *Handler) renderArtifactWith(w http.ResponseWriter, r *http.Request, status int, v *viewer, id string, o artifactOpts) {
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
	files, err := h.Store.Files(ctx, a.ID, a.CurrentVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	projects, err := h.Store.Projects(ctx, v.Tenant.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := artifactData{Artifact: a, URL: h.artifactURL(a), CoverURL: h.artifactCoverURL(a), Error: o.Error, ErrThread: o.ErrThread,
		Meta: metaFromArtifact(a), MetaError: o.MetaError, Styles: cover.Styles(), CoverFiles: coverFiles(files)}
	d.SendURL, d.SendLabel = d.URL, "Share link"
	if a.Visibility == "private" {
		// Private and uncoded: the only people this link works for are signed-in
		// members of the workspace. Say so rather than calling it a share link.
		d.SendLabel = "Workspace link"
	}
	if a.ShareCode != "" {
		d.ShareLink = serve.ShareURL(h.Cfg.ShareOrigin, a.ID, a.ShareCode)
		d.SendURL, d.SendLabel = d.ShareLink, "Share link, code included"
	}
	if o.Meta != nil {
		d.Meta = *o.Meta
	}
	for _, p := range projects {
		d.Projects = append(d.Projects, p.Name)
	}
	// A cover file that the current version no longer has (after a rollback)
	// stays selectable so the form shows what is stored.
	if a.CoverPath != nil && !contains(d.CoverFiles, *a.CoverPath) {
		d.CoverFiles = append(d.CoverFiles, *a.CoverPath)
	}
	for i := len(vs) - 1; i >= 0; i-- { // newest first
		vv := vs[i]
		d.Versions = append(d.Versions, versionView{N: vv.Version, CreatedAt: vv.CreatedAt, Bytes: vv.Bytes, FileCount: vv.FileCount,
			Current: vv.Version == a.CurrentVersion, Comments: counts[vv.Version], By: versionBy(vv), Key: vv.KeyName})
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
	if wantsPanel(r) {
		h.renderRoot(w, r, status, "artifact", "panel", title, v, d)
		return
	}
	h.render(w, r, status, "artifact", title, v, d)
}

// wantsPanel is true when the request comes from the list page's side peek:
// GET ?panel=1 for the fragment, or a panel=1 field on the forms inside it.
// Such requests get the shell-less fragment back instead of a full page or
// a redirect, so the panel re-renders in place.
func wantsPanel(r *http.Request) bool {
	return r.URL.Query().Get("panel") == "1" || r.PostForm.Get("panel") == "1"
}

// backToArtifact ends a successful action: the fragment for the side peek,
// otherwise a redirect to the detail page at anchor.
func (h *Handler) backToArtifact(w http.ResponseWriter, r *http.Request, v *viewer, id, anchor string) {
	if wantsPanel(r) {
		h.renderArtifact(w, r, 200, v, id, "", "")
		return
	}
	http.Redirect(w, r, "/app/artifacts/"+id+anchor, http.StatusFound)
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
		h.backToArtifact(w, r, v, a.ID, "")
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
	h.backToArtifact(w, r, v, a.ID, "")
}

// setCommentsEnabled is the Comments switch on the detail page: enabled=1
// turns the viewer runtime on, enabled=0 off. Existing threads are untouched.
func (h *Handler) setCommentsEnabled(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	var on bool
	switch r.PostForm.Get("enabled") {
	case "1":
		on = true
	case "0":
		on = false
	default:
		h.renderError(w, r, 400, "The form could not be read.")
		return
	}
	if on != a.CommentsEnabled {
		if err := h.Store.SetCommentsEnabled(r.Context(), a.ID, on); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.backToArtifact(w, r, v, a.ID, "#comments")
}

// setVisibility changes who can read an artifact without changing its
// versions, comments, or saved viewer-comment preference.
func (h *Handler) setVisibility(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	visibility := r.PostForm.Get("visibility")
	if visibility != "public" && visibility != "private" {
		h.renderError(w, r, 400, "Choose Public or Private.")
		return
	}
	if visibility != a.Visibility {
		if err := h.Store.SetVisibility(r.Context(), a.ID, visibility); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	// A code only ever gates a private artifact. Going public makes it moot,
	// so clear it rather than leave a live code on an artifact anyone can read
	// — it would come back the moment the artifact went private again.
	if visibility == "public" && a.ShareCode != "" {
		if err := h.Store.SetShareCode(r.Context(), a.ID, "", nil); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.backToArtifact(w, r, v, a.ID, "#visibility")
}

// setShareCode issues, rotates, or withdraws an artifact's share code.
// Issuing and rotating are the same operation: a fresh code, which is what
// makes rotation a revocation — every link carrying the old one stops working,
// and only for this artifact.
func (h *Handler) setShareCode(w http.ResponseWriter, r *http.Request, v *viewer) {
	a, err := h.ownedArtifact(r, v, r.PathValue("id"))
	if err != nil {
		h.storeErr(w, r, err, "That artifact does not exist.")
		return
	}
	code := ""
	switch r.PostForm.Get("action") {
	case "issue", "rotate":
		if a.Visibility != "private" {
			h.renderError(w, r, 400, "Only a private artifact can be shared with a code.")
			return
		}
		code = ids.NewShareCode()
	case "revoke":
	default:
		h.renderError(w, r, 400, "Choose whether to share this artifact with a code.")
		return
	}
	if err := h.Store.SetShareCode(r.Context(), a.ID, code, nil); err != nil {
		h.fail(w, r, err)
		return
	}
	h.backToArtifact(w, r, v, a.ID, "#visibility")
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
	h.backToArtifact(w, r, v, a.ID, "#"+t.ID)
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
	h.backToArtifact(w, r, v, a.ID, "#"+t.ID)
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
