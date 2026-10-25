package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/ids"
	"github.com/anup-a/byagent/internal/links"
	"github.com/anup-a/byagent/internal/store"
)

// maxLinkTitle matches what the dashboard shows of a page title.
const maxLinkTitle = 200

// pageHosts are the host names whose pages byagent serves itself, so an
// agent that tries to register one is told to publish instead.
func (s *server) pageHosts() []string {
	out := s.reservedHosts()
	if s.Cfg.ContentDomain != "" {
		out = append(out, s.Cfg.ContentDomain)
	}
	return out
}

func linkJSON(l store.Link) map[string]any {
	var project any
	if l.Project != nil {
		project = *l.Project
	}
	return map[string]any{"id": l.ID, "url": l.URL, "host": l.Host, "provider": links.Provider(l.Host),
		"title": l.Title, "project": project, "agent": l.Agent, "model": l.Model, "key": l.KeyName,
		"hosted": false, "created_at": l.CreatedAt.UTC().Format(time.RFC3339), "updated_at": l.UpdatedAt.UTC().Format(time.RFC3339)}
}

// linksCaller refuses guest keys: a guest workspace expires, and a list of
// someone else's URLs is no reason to keep one.
func (s *server) linksCaller(r *http.Request) (store.Key, *apierr.Error) {
	k, _ := KeyFrom(r.Context())
	if _, ok := guestFrom(r.Context()); ok {
		return k, apierr.Forbidden("links need an account; sign in at " + s.Cfg.APIOrigin + "/app/connect")
	}
	return k, nil
}

// putLink answers POST /api/links with {url, title?, project?, agent?,
// model?}. It registers work an agent published somewhere else; nothing is
// fetched or stored but the labels. The same URL again refreshes the link
// (200) rather than making a second one (201).
func (s *server) putLink(w http.ResponseWriter, r *http.Request) {
	k, e := s.linksCaller(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var body struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Project string `json:"project"`
		Agent   string `json:"agent"`
		Model   string `json:"model"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		apierr.Write(w, r, apierr.TooLarge("body too large"))
		return
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		apierr.Write(w, r, apierr.Invalid(`body must be JSON: {"url": "https://…", "title": "…"}`))
		return
	}
	u, host, err := links.Normalize(body.URL, s.pageHosts())
	if err != nil {
		apierr.Write(w, r, apierr.Invalid(err.Error()))
		return
	}
	title := strings.TrimSpace(body.Title)
	if !utf8.ValidString(title) || hasControl(title, false) || utf8.RuneCountInString(title) > maxLinkTitle {
		apierr.Write(w, r, apierr.Invalid(fmt.Sprintf("title must be at most %d characters, without control characters", maxLinkTitle)))
		return
	}
	project, e := cleanProject(body.Project)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if body.Agent == "" {
		body.Agent = clientName(r)
	}
	for name, v := range map[string]string{"agent": body.Agent, "model": body.Model} {
		if len(v) > 64 || hasControl(v, false) {
			apierr.Write(w, r, apierr.Invalid(name+" must be at most 64 characters, without control characters"))
			return
		}
	}
	now := s.Now()
	l, created, err := s.Store.PutLink(r.Context(), store.Link{ID: "ln_" + ids.NewArtifactID(), TenantID: k.TenantID, URL: u, Host: host,
		Title: title, Project: project, Agent: body.Agent, Model: body.Model, KeyName: k.Name, CreatedAt: now, UpdatedAt: now}, links.MaxPerTenant)
	if errors.Is(err, store.ErrLinkLimit) {
		apierr.Write(w, r, apierr.Quota(fmt.Sprintf("this workspace has %d links; delete some with byagent unlink", links.MaxPerTenant)))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	status := 200
	if created {
		status = 201
	}
	out := linkJSON(l)
	out["created"] = created
	apierr.WriteJSON(w, status, out)
}

// listLinks answers GET /api/links[?project=&q=&limit=], newest first.
func (s *server) listLinks(w http.ResponseWriter, r *http.Request) {
	k, e := s.linksCaller(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	q := r.URL.Query()
	f := store.LinkFilter{Project: strings.TrimSpace(q.Get("project")), Query: strings.TrimSpace(q.Get("q")), Limit: 50}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			apierr.Write(w, r, apierr.Invalid("limit must be 1..200"))
			return
		}
		f.Limit = n
	}
	ls, err := s.Store.ListLinks(r.Context(), k.TenantID, f)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := make([]map[string]any, 0, len(ls))
	for _, l := range ls {
		out = append(out, linkJSON(l))
	}
	apierr.WriteJSON(w, 200, map[string]any{"links": out})
}

// deleteLink answers DELETE /api/links/{id}. The id may also be the URL
// itself (escaped), which is what a hook or an agent usually has at hand.
func (s *server) deleteLink(w http.ResponseWriter, r *http.Request) {
	k, e := s.linksCaller(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	l, err := s.findLink(r, k.TenantID, r.PathValue("id"))
	if err == nil {
		err = s.Store.DeleteLink(r.Context(), k.TenantID, l.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		apierr.Write(w, r, apierr.NotFound("link"))
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) findLink(r *http.Request, tenantID, ref string) (store.Link, error) {
	if strings.HasPrefix(ref, "ln_") {
		return s.Store.LinkByID(r.Context(), tenantID, ref)
	}
	if dec, err := url.PathUnescape(ref); err == nil {
		ref = dec
	}
	u, _, err := links.Normalize(ref, nil)
	if err != nil {
		return store.Link{}, store.ErrNotFound
	}
	return s.Store.LinkByURL(r.Context(), tenantID, u)
}
