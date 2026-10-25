package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/anup-a/byagent/internal/apierr"
	"github.com/anup-a/byagent/internal/ratelimit"
	"github.com/anup-a/byagent/internal/store"
)

// Page state: a small JSON key-value store a page's own script keeps through
// window.byagent.state, so a checklist, a vote or a form survives a reload
// and is shared by everyone who opens the page.
//
// It lives at _state below the page (statePath): GET reads it, POST writes
// it. Whoever may open the page may read and write its state, as with viewer
// comments: the same access gate decides, and nothing else is asked. On a
// page host the route names no artifact; the host is the page, so one page's
// script can never reach another page's state. Writes there must carry the
// page's own Origin (CrossPageWrite runs before any route), and reads must
// too (stateRequestOK), so another page cannot even read it through a
// visitor's cookies.
//
// State belongs to the artifact, not to a version: every version, and a
// pinned v/{n}/ address, sees the same values. A write never makes a
// version, and deleting the artifact erases its state.
const statePath = "_state"

// Limits on page state.
const (
	// StateKeyMax is the longest key, in bytes of [A-Za-z0-9._:-].
	StateKeyMax = 128
	// StateValueMax is the largest value, as JSON text.
	StateValueMax = 64 << 10
	// StatePatchMax is how many keys one write may change.
	StatePatchMax = 256
	// StateBodyMax bounds a write's request body.
	StateBodyMax = 256 << 10
	// StatePageKeys and StatePageBytes cap one page's whole state (keys
	// plus values).
	StatePageKeys  = 1000
	StatePageBytes = 2 << 20
)

// Per client address, per minute. A page polls about four times a minute
// while it is open; writes follow clicks.
const (
	stateReadsPerMinute  = 120
	stateWritesPerMinute = 60
)

type stateGuards struct{ reads, writes *ratelimit.Limiter }

func newStateGuards() *stateGuards {
	return &stateGuards{reads: ratelimit.New(stateReadsPerMinute), writes: ratelimit.New(stateWritesPerMinute)}
}

// ValidStateKey reports whether k may name a state entry.
func ValidStateKey(k string) bool {
	if k == "" || len(k) > StateKeyMax {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

// StateJSON is the wire form of a page's state: {revision, items}, with
// each value as the JSON it was stored as.
func StateJSON(st store.PageState) map[string]any {
	items := make(map[string]json.RawMessage, len(st.Items))
	for k, v := range st.Items {
		items[k] = json.RawMessage(v)
	}
	return map[string]any{"revision": st.Revision, "items": items}
}

// stateRequestOK refuses a state request a page did not make itself. On a
// page host or custom domain that is the page's own origin; on the share
// origin, where every page shares one host anyway, it is the share origin.
// Reads are held to it as well as writes: state is for the page's script,
// and the owner reads it through the API.
func (h *Handler) stateRequestOK(r *http.Request) bool {
	if OnPageHost(r) || OnMount(r) {
		return SameOriginRequest(r)
	}
	return SameOrigin(r, h.ShareOrigin)
}

// stateArtifact loads the page a state request is for and checks that this
// visitor may open it, exactly as serving the page itself would.
func (h *Handler) stateArtifact(w http.ResponseWriter, r *http.Request, limiter *ratelimit.Limiter) (store.Artifact, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.stateRequestOK(r) {
		h.refuseCrossPage(w, r)
		return store.Artifact{}, false
	}
	if ok, retry := limiter.Allow("ip:" + ratelimit.ClientKey(h.clientIP(r))); !ok {
		apierr.Write(w, r, apierr.RateLimited(retry))
		return store.Artifact{}, false
	}
	a, err := h.Store.Artifact(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || err == nil && (foreignToMount(r, a) || !h.canView(r, a)) {
		apierr.Write(w, r, apierr.NotFound("artifact"))
		return a, false
	}
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return a, false
	}
	if a.DeletedAt != nil {
		apierr.Write(w, r, apierr.Gone())
		return a, false
	}
	if !a.StateEnabled {
		apierr.Write(w, r, apierr.NotFound("page state"))
		return a, false
	}
	return a, true
}

// readState answers GET _state: {revision, items}. With ?since=n and
// nothing written after revision n it answers {revision} alone, which is
// what a polling page asks for nearly every time.
func (h *Handler) readState(w http.ResponseWriter, r *http.Request) {
	a, ok := h.stateArtifact(w, r, h.state.reads)
	if !ok {
		return
	}
	if since := r.URL.Query().Get("since"); since != "" {
		n, err := strconv.ParseInt(since, 10, 64)
		if err != nil || n < 0 {
			apierr.Write(w, r, apierr.Invalid("since must be a non-negative integer"))
			return
		}
		rev, err := h.Store.StateRevision(r.Context(), a.ID)
		if err != nil {
			apierr.Write(w, r, apierr.Internal(err))
			return
		}
		if rev == n {
			apierr.WriteJSON(w, 200, map[string]any{"revision": rev})
			return
		}
	}
	st, err := h.Store.State(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, StateJSON(st))
}

// writeState answers POST _state with {"items": {key: value | null}}: each
// key is set to its value, or deleted when the value is null, as one write.
// It answers {revision}, the state's revision after the write.
func (h *Handler) writeState(w http.ResponseWriter, r *http.Request) {
	a, ok := h.stateArtifact(w, r, h.state.writes)
	if !ok {
		return
	}
	var in struct {
		Items map[string]json.RawMessage `json:"items"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, StateBodyMax))
	if err := dec.Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			apierr.Write(w, r, apierr.TooLarge(fmt.Sprintf("a state write is at most %d KiB", StateBodyMax>>10)))
			return
		}
		apierr.Write(w, r, apierr.Invalid("body must be JSON: {\"items\": {key: value or null}}"))
		return
	}
	if len(in.Items) == 0 {
		apierr.Write(w, r, apierr.Invalid("items must name at least one key"))
		return
	}
	if len(in.Items) > StatePatchMax {
		apierr.Write(w, r, apierr.Invalid(fmt.Sprintf("a state write changes at most %d keys", StatePatchMax)))
		return
	}
	set := map[string]string{}
	var del []string
	for k, raw := range in.Items {
		if !ValidStateKey(k) {
			apierr.Write(w, r, apierr.Invalid(fmt.Sprintf("key %q: keys are 1-%d characters of letters, digits, '.', '_', ':' and '-'", k, StateKeyMax)))
			return
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			del = append(del, k)
			continue
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			apierr.Write(w, r, apierr.Invalid(fmt.Sprintf("key %q: value is not JSON", k)))
			return
		}
		if buf.Len() > StateValueMax {
			apierr.Write(w, r, apierr.TooLarge(fmt.Sprintf("key %q: a value is at most %d KiB of JSON", k, StateValueMax>>10)))
			return
		}
		set[k] = buf.String()
	}
	sort.Strings(del)
	rev, err := h.Store.PatchState(r.Context(), a.ID, set, del, store.StateLimits{Keys: StatePageKeys, Bytes: StatePageBytes}, h.now())
	switch {
	case errors.Is(err, store.ErrStateFull):
		apierr.Write(w, r, apierr.TooLarge(fmt.Sprintf("page state is full: at most %d keys and %d MiB per page", StatePageKeys, StatePageBytes>>20)))
		return
	case errors.Is(err, store.ErrNotFound):
		apierr.Write(w, r, apierr.NotFound("artifact"))
		return
	case err != nil:
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	apierr.WriteJSON(w, 200, map[string]any{"revision": rev})
}
