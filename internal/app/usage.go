package app

import (
	"net/http"
	"time"

	"github.com/anup-a/artifacts/internal/store"
	"github.com/anup-a/artifacts/internal/usage"
)

type monthView struct {
	Label string
	Month time.Time
	store.MonthUsage
}

func (h *Handler) usage(w http.ResponseWriter, r *http.Request, v *viewer) {
	now := h.Now()
	months := []monthView{{Label: "This month", Month: usage.MonthStart(now)}, {Label: "Last month", Month: usage.PreviousMonth(now)}}
	for i := range months {
		u, err := h.Store.Usage(r.Context(), v.Tenant.ID, months[i].Month)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		months[i].MonthUsage = u
	}
	pages, err := h.pageQuota(r.Context(), v.Tenant.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, r, 200, "usage", "Usage", v, struct {
		Pages  pageQuota
		Months []monthView
	}{pages, months})
}
