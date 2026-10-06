package serve

import (
	"net/http"
	"strings"
)

// The cookies a page's visitors hold (an unlock today; password and email
// grants with page access control) are written and read only through these
// helpers, because where they live depends on where the page is served.
//
// On a page's own host a cookie is host-only and covers the whole host:
// Path=/, no Domain, so no other page host (and not the content domain's
// apex) ever receives it. Over https it also takes the __Host- prefix, which
// makes the browser refuse any version of it that is not Secure, Path=/ and
// host-only, so not even a sibling host could plant one. Plain http (local
// work on *.localhost) cannot use the prefix, so the bare name is used there.
//
// On the share origin, where pages share one host, and on a custom domain,
// where several pages may be mounted, the cookie is named per page and
// scoped to that page's two paths instead (cookiePaths).
//
// Every cookie is HttpOnly and SameSite=Lax. Lax does not separate one page
// host from another (they are same-site until the content domain is on the
// Public Suffix List); CrossPageWrite does that.

// pageCookieName is the cookie's name for this request. base is the kind,
// "artifact_unlock" or "artifact_access".
func pageCookieName(r *http.Request, base, id string) string {
	if OnPageHost(r) {
		if pageHostSecure(r) {
			return "__Host-" + base
		}
		return base
	}
	return base + "_" + id
}

// pageHostSecure reports whether the page host is reached over https: the
// content domain's scheme says so, or the request itself came over TLS.
func pageHostSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.HasPrefix(ownOrigin(r), "https://")
}

// cookiePaths are the places a page answers its visitor: the whole host on
// a page host; on the share origin the pages under /a/{id}/ and the viewer
// comment routes under /c/{id}/. Two narrow cookies there rather than one
// at "/" keep the credential off every unrelated request to the origin,
// including other pages', where the browser would otherwise offer a
// credential that has no business being sent. Path scoping is not a
// security boundary between pages on one origin, though; page hosts are.
func cookiePaths(r *http.Request, id string) []string {
	if OnPageHost(r) {
		return []string{"/"}
	}
	// On the share origin and on a custom domain (whose mount holds the
	// page at its base) the page and its comment routes.
	return []string{PageBase(r, id), "/c/" + id + "/"}
}

// setPageCookie writes one visitor cookie for page id.
func (h *Handler) setPageCookie(w http.ResponseWriter, r *http.Request, base, id, value string, maxAge int) {
	secure := r.TLS != nil || strings.HasPrefix(h.ShareOrigin, "https://")
	if OnPageHost(r) || OnMount(r) {
		secure = pageHostSecure(r)
	}
	for _, path := range cookiePaths(r, id) {
		http.SetCookie(w, &http.Cookie{
			Name:     pageCookieName(r, base, id),
			Value:    value,
			Path:     path,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   maxAge,
		})
	}
}

// clearPageCookie drops a visitor cookie that no longer opens anything.
func (h *Handler) clearPageCookie(w http.ResponseWriter, r *http.Request, base, id string) {
	secure := (OnPageHost(r) || OnMount(r)) && pageHostSecure(r)
	for _, path := range cookiePaths(r, id) {
		http.SetCookie(w, &http.Cookie{
			Name: pageCookieName(r, base, id), Value: "", Path: path,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		})
	}
}

// pageCookie reads a visitor cookie for page id, "" when there is none.
func pageCookie(r *http.Request, base, id string) string {
	c, err := r.Cookie(pageCookieName(r, base, id))
	if err != nil {
		return ""
	}
	return c.Value
}
