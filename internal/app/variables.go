package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/internal/variables"
)

type variablesData struct {
	Enabled bool
	Rows    []store.Variable
	Limit   int
	Error   string
	// Name and Upstreams refill the form after a refusal. The value never
	// comes back: it is write-only, here as everywhere.
	Name, Upstreams string
	Saved           string
}

func (h *Handler) variablesPage(w http.ResponseWriter, r *http.Request, v *viewer) {
	d := variablesData{}
	if saved := r.URL.Query().Get("saved"); variables.ValidName(saved) {
		d.Saved = saved
	}
	h.renderVariables(w, r, 200, v, d)
}

func (h *Handler) renderVariables(w http.ResponseWriter, r *http.Request, status int, v *viewer, d variablesData) {
	d.Enabled, d.Limit = h.sealer != nil, variables.MaxPerTenant
	if d.Enabled {
		rows, err := h.Store.Variables(r.Context(), v.Tenant.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		d.Rows = rows
	}
	h.render(w, r, status, "variables", tr(r, "Variables"), v, d)
}

// setVariable adds or replaces a variable from the Settings form.
func (h *Handler) setVariable(w http.ResponseWriter, r *http.Request, v *viewer) {
	if h.sealer == nil {
		h.renderError(w, r, 503, tr(r, "Variables are off on this server."))
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("name"))
	ups := r.PostForm.Get("upstreams")
	again := variablesData{Name: name, Upstreams: ups}
	hosts, err := variables.Check(name, r.PostForm.Get("value"), strings.FieldsFunc(ups, func(c rune) bool {
		return c == ',' || c == ' ' || c == '\n' || c == '\r' || c == '\t'
	}))
	if err != nil {
		again.Error = capitalize(err.Error()) + "."
		h.renderVariables(w, r, 400, v, again)
		return
	}
	sealed, err := h.sealer.Seal(v.Tenant.ID, name, r.PostForm.Get("value"))
	if err != nil {
		h.fail(w, r, errors.New("sealing a variable failed"))
		return
	}
	now := h.Now()
	_, err = h.Store.PutVariable(r.Context(), store.Variable{TenantID: v.Tenant.ID, Name: name, Sealed: sealed,
		AllowedUpstreams: hosts, CreatedAt: now, UpdatedAt: now}, variables.MaxPerTenant)
	if errors.Is(err, store.ErrVariableLimit) {
		again.Error = tr(r, "This workspace already holds the most variables it can. Delete one first.")
		h.renderVariables(w, r, 400, v, again)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/app/settings/variables?saved="+name, http.StatusFound)
}

func (h *Handler) deleteVariable(w http.ResponseWriter, r *http.Request, v *viewer) {
	if h.sealer == nil {
		h.renderError(w, r, 503, tr(r, "Variables are off on this server."))
		return
	}
	if err := h.Store.DeleteVariable(r.Context(), v.Tenant.ID, r.PathValue("name")); err != nil {
		h.storeErr(w, r, err, tr(r, "That variable does not exist."))
		return
	}
	http.Redirect(w, r, "/app/settings/variables", http.StatusFound)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
