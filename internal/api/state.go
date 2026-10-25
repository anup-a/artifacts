package api

import (
	"net/http"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/serve"
)

// getState answers GET /api/artifacts/{id}/state: the page's whole state as
// its readers' script sees it, {revision, items}, plus its size against the
// per-page caps. It works with state switched off too, so an owner can read
// back what a page kept before the switch.
func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	st, err := s.Store.State(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	out := serve.StateJSON(st)
	out["id"], out["state_enabled"] = a.ID, a.StateEnabled
	out["keys"], out["bytes"] = st.Keys, st.Bytes
	out["max_keys"], out["max_bytes"] = serve.StatePageKeys, serve.StatePageBytes
	apierr.WriteJSON(w, 200, out)
}

// clearState answers DELETE /api/artifacts/{id}/state: every key goes, and
// the revision moves on so open pages pick the empty state up.
func (s *server) clearState(w http.ResponseWriter, r *http.Request) {
	a, e := s.ownedArtifact(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	rev, err := s.Store.ClearState(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"id": a.ID, "revision": rev, "cleared": true})
}
