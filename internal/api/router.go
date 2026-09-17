package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/auth"
	"github.com/anup-a/artifacts/internal/ids"
	"github.com/anup-a/artifacts/internal/serve"
	"github.com/anup-a/artifacts/internal/store"
)

func NewHandler(d Deps) (http.Handler, error) {
	s := &server{Deps: d}
	if d.Cfg.AdminToken != "" {
		k, err := ensureAdminKey(d)
		if err != nil {
			return nil, err
		}
		s.adminKey = &k
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/me", s.withAuth(s.me))
	mux.HandleFunc("POST /api/end-users/{external_id}/portal-link", s.withAuth(s.portalLink))
	mux.HandleFunc("GET /api/artifacts", s.withAuth(s.listArtifacts))
	mux.HandleFunc("GET /api/projects", s.withAuth(s.listProjects))
	mux.HandleFunc("POST /api/artifacts/preflight", s.withAuth(s.preflight))
	mux.HandleFunc("POST /api/artifacts", s.withAuth(s.createArtifact))
	mux.HandleFunc("PUT /api/artifacts/{id}", s.withAuth(s.updateArtifact))
	mux.HandleFunc("GET /api/artifacts/{id}", s.withAuth(s.getArtifact))
	mux.HandleFunc("DELETE /api/artifacts/{id}", s.withAuth(s.deleteArtifact))
	mux.HandleFunc("GET /api/artifacts/{id}/versions", s.withAuth(s.listVersions))
	mux.HandleFunc("POST /api/artifacts/{id}/rollback/{version}", s.withAuth(s.rollback))
	mux.HandleFunc("POST /api/artifacts/{id}/share-code", s.withAuth(s.setShareCode))
	mux.HandleFunc("POST /c/{id}/comments", noStore(s.viewerCreateThread))
	mux.HandleFunc("GET /c/{id}/comments", noStore(s.viewerList))
	mux.HandleFunc("GET /c/{id}/meta", noStore(s.viewerMeta))
	mux.HandleFunc("POST /c/{id}/comments/{thread}/reply", noStore(s.viewerReply))
	mux.HandleFunc("POST /c/{id}/comments/{thread}/seen", noStore(s.viewerSeen))
	mux.HandleFunc("GET /api/artifacts/{id}/comments", s.withAuth(s.ownerListComments))
	mux.HandleFunc("POST /api/artifacts/{id}/comments/{thread}/reply", s.withAuth(s.ownerReply))
	mux.HandleFunc("POST /api/artifacts/{id}/comments/{thread}/resolve", s.withAuth(s.ownerResolve))
	var sessions *auth.Sessions
	if d.Cfg.WebEnabled() {
		sessions = &auth.Sessions{Store: d.Store, Now: d.Now, Secure: auth.SecureOrigin(d.Cfg.APIOrigin)}
	}
	(&serve.Handler{
		Sessions: sessions, Deny: d.WebDeny, Gate: d.WebGate,
		Store: d.Store, Blobs: d.Blobs, Runtime: d.Runtime,
		ShareOrigin: d.Cfg.ShareOrigin, APIOrigin: d.Cfg.APIOrigin,
		UnlockSecret: d.Cfg.KeySecret, Now: d.Now, OnView: d.OnView,
	}).Register(mux)
	if d.Web != nil {
		d.Web(mux)
		// The bare host root is not an API route: send people to the dashboard,
		// on the API origin so byagent.dev/ lands on app.byagent.dev/app/.
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, d.Cfg.APIOrigin+"/app/", http.StatusFound)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apierr.Write(w, r, apierr.NotFound("route"))
	})
	return requestID(mux), nil
}

func ensureAdminKey(d Deps) (store.Key, error) {
	hash := ids.HashKey("admin:" + d.Cfg.AdminToken)
	k, err := d.Store.KeyByHash(context.Background(), hash)
	if errors.Is(err, store.ErrNotFound) {
		return d.Store.CreateKey(context.Background(), hash, "admin@local")
	}
	return k, err
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	if err := s.Blobs.Ping(r.Context()); err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	w.Write([]byte("ok"))
}
