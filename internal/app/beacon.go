package app

import (
	"log/slog"
	"net/http"
)

// copyPlacements are the spots on the public pages that copy the prompt, as
// named by their data-ga-where attribute. Anything else is not counted.
var copyPlacements = map[string]bool{"bar": true, "hero": true, "try": true, "close": true, "showcase": true, "other": true}

// copyBeacon counts a copy of the prompt. ga.js sends it on every copy, before
// and regardless of the cookie choice, so it carries nothing that could tell
// one visitor from another: no cookie, and the log line has only the spot.
// That keeps it outside consent, and counts the visitors who decline GA too.
// It sits next to the funnel lines from the API (guest_key, first_publish,
// agent_connected), so copies and real use can be read off one log.
func (h *Handler) copyBeacon(w http.ResponseWriter, r *http.Request) {
	at := r.URL.Query().Get("at")
	// sendBeacon from our own pages is same-origin; this drops most junk.
	if copyPlacements[at] && r.Header.Get("Sec-Fetch-Site") != "cross-site" {
		slog.Info("funnel", "step", "copy_prompt", "placement", at)
	}
	w.WriteHeader(http.StatusNoContent)
}
