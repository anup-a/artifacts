// Package proxy runs a page's proxy routes: named endpoints, declared in the
// published .byagent/proxy.json, that forward a browser's request to an
// https API with the workspace's secrets added on the server. The browser
// never sees the secret; this package decides what may be sent where.
package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anup-a/artifacts/internal/variables"
)

// ManifestPath is where a published page declares its routes. Files under
// .byagent/ are never served to visitors.
const ManifestPath = ".byagent/proxy.json"

// HiddenDir holds files that configure a page rather than belong to it.
const HiddenDir = ".byagent/"

// PathPrefix is the first path segment under a page that reaches its routes:
// /a/{id}/_proxy/{route}. Published files may not use it.
const PathPrefix = "_proxy"

// Limits on a manifest.
const (
	MaxManifestBytes = 64 << 10
	MaxRoutes        = 20
	maxHeaders       = 20
	maxQuery         = 20
	maxPaths         = 50
	maxTemplateBytes = 8 << 10
	// MaxVarsPerRoute bounds the secrets one call carries, which also bounds
	// the work of redacting them from the answer.
	MaxVarsPerRoute = 4
	maxBodyRules    = 20
	// MaxLimitPerHour is the most an owner may write in limit_per_hour; the
	// server's own per-page limit still applies on top.
	MaxLimitPerHour = 1_000_000
)

// Methods are the HTTP methods a route may declare.
var Methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// Route is one validated entry of the manifest.
type Route struct {
	Name     string
	Upstream *url.URL
	Methods  []string
	// Headers and Query map a name to a template that may hold {{NAME}}
	// references to account variables. Header names are canonical.
	Headers map[string]string
	Query   map[string]string
	// Paths are the sub-paths a caller may append after the route name,
	// each exact ("/models") or a prefix ending in "/*" ("/v1/*"). Empty
	// means the route is the upstream URL and nothing below it.
	Paths []string
	// Body, when set, holds rules for a JSON request body: fields the route
	// fixes and numeric fields it caps. A body is then required to be a
	// JSON object.
	Body *BodyRules
}

// BodyRules constrain top-level fields of a JSON request body. Pin sets a
// field to a fixed value whatever the caller sent ("model": the one the owner
// pays for). Max caps a number, and sets it when the caller left it out
// ("max_tokens": 1024).
type BodyRules struct {
	Pin map[string]json.RawMessage `json:"pin"`
	Max map[string]float64         `json:"max"`
}

// Manifest is a parsed .byagent/proxy.json.
type Manifest struct {
	Routes map[string]Route
	// LimitPerHour, when set, is the owner's own cap on calls to this page's
	// routes per hour. The server's per-page limit applies when it is lower.
	LimitPerHour int
}

type rawRoute struct {
	Upstream string            `json:"upstream"`
	Method   string            `json:"method"`
	Methods  []string          `json:"methods"`
	Headers  map[string]string `json:"headers"`
	Query    map[string]string `json:"query"`
	Paths    []string          `json:"paths"`
	Body     *BodyRules        `json:"body"`
}

var (
	routeNameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	headerNameRe = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,128}$")
	varRefRe     = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)
	// Sub-path segments are unreserved characters only: no percent
	// escapes, no ";" parameters, nothing outside ASCII.
	subPathRe = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)+$`)
)

// forbiddenHeaders may not be set by a manifest: they belong to the
// connection, identify the visitor, or would let a page smuggle a second
// request or a cookie upstream.
var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true,
	"Keep-Alive": true, "Te": true, "Trailer": true, "Upgrade": true, "Expect": true,
	"Cookie": true, "Cookie2": true, "Forwarded": true, "Via": true, "X-Real-Ip": true,
	"True-Client-Ip": true, "Accept-Encoding": true, "X-Byagent-Proxy": true,
}

func forbiddenHeader(name string) bool {
	c := http.CanonicalHeaderKey(name)
	return forbiddenHeaders[c] || strings.HasPrefix(c, "Proxy-") || strings.HasPrefix(c, "X-Forwarded-") ||
		strings.HasPrefix(c, "Cf-") || strings.HasPrefix(c, "Sec-")
}

// Parse validates a manifest. reserved lists host names routes may not point
// at (this server's own origins), so a route cannot call back into byagent.
func Parse(raw []byte, reserved ...string) (Manifest, error) {
	if len(raw) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("is larger than %d KB", MaxManifestBytes>>10)
	}
	var doc struct {
		Routes       map[string]json.RawMessage `json:"routes"`
		Proxies      json.RawMessage            `json:"proxies"`
		LimitPerHour *int                       `json:"limit_per_hour"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return Manifest{}, fmt.Errorf("is not valid: %s", jsonMessage(err))
	}
	if doc.Proxies != nil {
		return Manifest{}, errors.New(`uses "proxies"; byagent routes are named: {"routes": {"chat": {"upstream": "https://…", "methods": ["POST"]}}}`)
	}
	if len(doc.Routes) == 0 {
		return Manifest{}, errors.New(`declares no routes: expected {"routes": {"<name>": {...}}}`)
	}
	if len(doc.Routes) > MaxRoutes {
		return Manifest{}, fmt.Errorf("declares more than %d routes", MaxRoutes)
	}
	m := Manifest{Routes: map[string]Route{}}
	if doc.LimitPerHour != nil {
		if *doc.LimitPerHour < 1 || *doc.LimitPerHour > MaxLimitPerHour {
			return Manifest{}, fmt.Errorf("limit_per_hour must be between 1 and %d", MaxLimitPerHour)
		}
		m.LimitPerHour = *doc.LimitPerHour
	}
	for name, body := range doc.Routes {
		rt, err := parseRoute(name, body, reserved)
		if err != nil {
			return Manifest{}, fmt.Errorf("route %q: %w", name, err)
		}
		m.Routes[name] = rt
	}
	return m, nil
}

func jsonMessage(err error) string {
	return strings.TrimPrefix(err.Error(), "json: ")
}

func parseRoute(name string, body json.RawMessage, reserved []string) (Route, error) {
	if !routeNameRe.MatchString(name) {
		return Route{}, errors.New("route names are 1 to 64 lowercase letters, digits, - and _")
	}
	var rr rawRoute
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rr); err != nil {
		return Route{}, errors.New(jsonMessage(err))
	}
	rt := Route{Name: name, Headers: map[string]string{}, Query: map[string]string{}}
	u, err := parseUpstream(rr.Upstream, reserved)
	if err != nil {
		return Route{}, err
	}
	rt.Upstream = u
	if rt.Methods, err = parseMethods(rr.Method, rr.Methods); err != nil {
		return Route{}, err
	}
	if len(rr.Headers) > maxHeaders {
		return Route{}, fmt.Errorf("more than %d headers", maxHeaders)
	}
	for k, v := range rr.Headers {
		if !headerNameRe.MatchString(k) {
			return Route{}, fmt.Errorf("header %q is not a valid header name", k)
		}
		if forbiddenHeader(k) {
			return Route{}, fmt.Errorf("header %q cannot be set by a route", k)
		}
		if err := checkTemplate(v); err != nil {
			return Route{}, fmt.Errorf("header %q: %w", k, err)
		}
		rt.Headers[http.CanonicalHeaderKey(k)] = v
	}
	if len(rr.Query) > maxQuery {
		return Route{}, fmt.Errorf("more than %d query parameters", maxQuery)
	}
	for k, v := range rr.Query {
		if k == "" || len(k) > 128 {
			return Route{}, errors.New("query parameter names are 1 to 128 characters")
		}
		if err := checkTemplate(v); err != nil {
			return Route{}, fmt.Errorf("query %q: %w", k, err)
		}
		rt.Query[k] = v
	}
	if len(rr.Paths) > maxPaths {
		return Route{}, fmt.Errorf("more than %d paths", maxPaths)
	}
	for _, p := range rr.Paths {
		base := strings.TrimSuffix(p, "/*")
		if !subPathRe.MatchString(base) || hasDotSegment(base) {
			return Route{}, fmt.Errorf(`path %q must start with / and hold only letters, digits and "-._~" in each segment; end it in /* to allow anything below`, p)
		}
		rt.Paths = append(rt.Paths, p)
	}
	if n := len(rt.Vars()); n > MaxVarsPerRoute {
		return Route{}, fmt.Errorf("uses %d variables; a route may use at most %d", n, MaxVarsPerRoute)
	}
	if rr.Body != nil {
		if err := checkBodyRules(rr.Body); err != nil {
			return Route{}, fmt.Errorf("body: %w", err)
		}
		rt.Body = rr.Body
	}
	return rt, nil
}

func checkBodyRules(b *BodyRules) error {
	if len(b.Pin)+len(b.Max) == 0 {
		return errors.New(`give "pin" and/or "max", for example {"pin": {"model": "x"}, "max": {"max_tokens": 1024}}`)
	}
	if len(b.Pin)+len(b.Max) > maxBodyRules {
		return fmt.Errorf("more than %d rules", maxBodyRules)
	}
	for k, v := range b.Pin {
		if k == "" || len(k) > 128 {
			return errors.New("field names are 1 to 128 characters")
		}
		if len(v) > maxTemplateBytes {
			return fmt.Errorf("pin %q is larger than %d bytes", k, maxTemplateBytes)
		}
		if bytes.Contains(v, []byte("{{")) {
			return errors.New("variables cannot be put in a body")
		}
		if _, both := b.Max[k]; both {
			return fmt.Errorf("%q is both pinned and capped", k)
		}
	}
	for k, v := range b.Max {
		if k == "" || len(k) > 128 {
			return errors.New("field names are 1 to 128 characters")
		}
		if v <= 0 {
			return fmt.Errorf("max %q must be a positive number", k)
		}
	}
	return nil
}

func parseUpstream(s string, reserved []string) (*url.URL, error) {
	if s == "" {
		return nil, errors.New("upstream is required, an https URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, errors.New("upstream is not a URL")
	}
	if u.Scheme != "https" {
		return nil, errors.New("upstream must be an https URL")
	}
	if u.User != nil {
		return nil, errors.New("upstream may not carry a user name or password; put credentials in a header from a variable")
	}
	if u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("upstream may not have a fragment")
	}
	if p := u.Port(); p != "" && p != "443" {
		return nil, errors.New("upstream must use the default https port")
	}
	host, err := variables.NormalizeHost(u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("upstream host: %w", err)
	}
	for _, r := range reserved {
		if r != "" && variables.HostAllowed(host, []string{strings.ToLower(r)}) {
			return nil, errors.New("upstream may not point back at byagent")
		}
	}
	if hasDotSegment(u.EscapedPath()) || strings.Contains(strings.ToLower(u.EscapedPath()), "%2e") {
		return nil, errors.New(`upstream path may not hold "." or ".." segments`)
	}
	for _, v := range u.Query() {
		for _, q := range v {
			if strings.Contains(q, "{{") {
				return nil, errors.New(`put {{NAME}} references in "query", not in the upstream URL`)
			}
		}
	}
	u.Host = host
	return u, nil
}

func parseMethods(one string, many []string) ([]string, error) {
	if one != "" && len(many) > 0 {
		return nil, errors.New(`give "method" or "methods", not both`)
	}
	if one != "" {
		many = []string{one}
	}
	if len(many) == 0 {
		return nil, errors.New(`"methods" is required, for example ["POST"]`)
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range many {
		m = strings.ToUpper(strings.TrimSpace(m))
		ok := false
		for _, allowed := range Methods {
			ok = ok || m == allowed
		}
		if !ok {
			return nil, fmt.Errorf("method %q is not one of GET, POST, PUT, PATCH, DELETE", m)
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out, nil
}

// checkTemplate accepts text with {{NAME}} references to variables. Control
// characters are refused so a header cannot be split.
func checkTemplate(v string) error {
	if len(v) > maxTemplateBytes {
		return fmt.Errorf("is longer than %d bytes", maxTemplateBytes)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == 0x7f {
			return errors.New("contains a control character")
		}
	}
	if strings.Contains(v, "${") {
		return errors.New("write variables as {{NAME}}, not ${NAME}")
	}
	for _, m := range varRefRe.FindAllStringSubmatch(v, -1) {
		if !variables.ValidName(m[1]) {
			return fmt.Errorf("{{%s}} is not a variable name (uppercase letters, digits and _)", m[1])
		}
	}
	rest := varRefRe.ReplaceAllString(v, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return errors.New("has an unclosed {{")
	}
	return nil
}

func hasDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return strings.Contains(p, "//")
}

// Vars lists the variable names a route refers to, sorted.
func (rt Route) Vars() []string {
	seen := map[string]bool{}
	for _, set := range []map[string]string{rt.Headers, rt.Query} {
		for _, v := range set {
			for _, m := range varRefRe.FindAllStringSubmatch(v, -1) {
				seen[m[1]] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Allows reports whether the route declares method.
func (rt Route) Allows(method string) bool {
	for _, m := range rt.Methods {
		if m == method {
			return true
		}
	}
	return false
}

// MatchPath reports whether a caller may append sub ("" or "/x/y") to the
// upstream. sub arrives escaped and has already passed CleanSub.
func (rt Route) MatchPath(sub string) bool {
	if sub == "" {
		return true
	}
	for _, p := range rt.Paths {
		if base, ok := strings.CutSuffix(p, "/*"); ok {
			if strings.HasPrefix(sub, base+"/") && len(sub) > len(base)+1 {
				return true
			}
			continue
		}
		if sub == p {
			return true
		}
	}
	return false
}

// CleanSub checks the escaped sub-path a caller appended after the route
// name. Each segment may hold unreserved characters only (letters, digits
// and "-._~"): no percent escapes at all, so nothing can be decoded into a
// slash, a dot segment or a parameter on the way upstream, and no ";", "\\"
// or non-ASCII. Dot segments and empty segments are refused.
func CleanSub(escaped string) (string, bool) {
	if escaped == "" {
		return "", true
	}
	if !subPathRe.MatchString(escaped) || hasDotSegment(escaped) {
		return "", false
	}
	return escaped, true
}

// ApplyBody enforces a route's body rules on a request body. The body must be
// a JSON object; pinned fields are set, capped numbers are lowered (or set
// when absent). It returns the body to send.
func (b *BodyRules) ApplyBody(raw []byte) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errors.New("the request body must be a JSON object")
	}
	for k, v := range b.Pin {
		obj[k] = v
	}
	for k, max := range b.Max {
		v, ok := obj[k]
		if !ok || string(v) == "null" {
			obj[k] = json.RawMessage(strconv.FormatFloat(max, 'f', -1, 64))
			continue
		}
		var n float64
		if err := json.Unmarshal(v, &n); err != nil {
			return nil, fmt.Errorf("%s must be a number", k)
		}
		if n > max {
			obj[k] = json.RawMessage(strconv.FormatFloat(max, 'f', -1, 64))
		}
	}
	return json.Marshal(obj)
}

// Expand fills a template's {{NAME}} references from vals. Every name was
// checked by Parse and resolved by the caller, so a miss expands to "".
func Expand(tmpl string, vals map[string]string) string {
	return varRefRe.ReplaceAllStringFunc(tmpl, func(m string) string {
		return vals[varRefRe.FindStringSubmatch(m)[1]]
	})
}
