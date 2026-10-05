// Package domains connects hostnames people own to byagent pages: checking
// that they own them, asking the edge for a certificate, and deciding which
// page a request on such a hostname is for.
package domains

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// maxMountDepth and maxSegment bound a mount path, which is a URL prefix
// and nothing more: a handful of short readable segments.
const (
	maxMountDepth = 5
	maxSegment    = 64
)

// blockedTLDs never resolve on the public internet, so a TXT record there
// could only ever come from a resolver someone has tampered with.
var blockedTLDs = map[string]bool{"localhost": true, "local": true, "internal": true, "invalid": true, "test": true, "example": true, "onion": true, "home": true, "lan": true, "arpa": true}

// NormalizeHost turns what a person typed into the hostname to store:
// lower case, ASCII (punycode for an international name), no trailing dot.
// It accepts a bare URL and keeps its host, since agents are handed links,
// but refuses anything with a port or a path, an IP address, a wildcard, a
// single label and the names that never resolve publicly.
func NormalizeHost(input string) (string, error) {
	h := strings.TrimSpace(input)
	if strings.Contains(h, "://") {
		u, err := url.Parse(h)
		if err != nil || u.Host == "" {
			return "", errors.New("give a hostname like docs.example.com")
		}
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
			return "", errors.New("give a hostname like docs.example.com, without a path")
		}
		h = u.Host
	}
	if strings.ContainsAny(h, "/:@?# \t") {
		return "", errors.New("give a hostname like docs.example.com, without a scheme, port or path")
	}
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "" || len(h) > 253 {
		return "", errors.New("give a hostname like docs.example.com")
	}
	if strings.Contains(h, "*") {
		return "", errors.New("wildcard domains are not supported; connect each hostname")
	}
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", errors.New("that hostname is not valid")
	}
	h = ascii
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return "", errors.New("give a full hostname like docs.example.com")
	}
	for _, l := range labels {
		if !validLabel(l) {
			return "", errors.New("that hostname is not valid")
		}
	}
	tld := labels[len(labels)-1]
	if allDigits(tld) {
		return "", errors.New("IP addresses cannot be connected; use a hostname")
	}
	if blockedTLDs[tld] {
		return "", errors.New("that hostname does not resolve on the public internet")
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(h); err != nil {
		return "", errors.New("that is a public suffix, not a domain anyone can own")
	}
	return h, nil
}

func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// Reserved reports whether host is, or sits under, one of the hosts byagent
// itself answers on. A page must never be served as app.byagent.dev, and a
// subdomain of byagent.dev would put a customer's page on byagent's own
// cookie scope.
func Reserved(host string, own []string) bool {
	for _, o := range own {
		o = strings.TrimSuffix(strings.ToLower(o), ".")
		if o == "" {
			continue
		}
		if host == o || strings.HasSuffix(host, "."+o) {
			return true
		}
	}
	return false
}

// Registrable is the part of host a registrar sells: docs.example.co.uk
// gives example.co.uk.
func Registrable(host string) string {
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return d
}

// IsApex reports whether host is a registrable domain itself, which cannot
// hold a CNAME record.
func IsApex(host string) bool { return Registrable(host) == host }

// relativeName is how a DNS provider's form names a record at name, given
// the zone it belongs to: "@" for the zone itself, else the labels in front.
func relativeName(name, zone string) string {
	if name == zone {
		return "@"
	}
	return strings.TrimSuffix(name, "."+zone)
}

// NormalizePath turns a mount path into its stored form: "" for the root,
// else segments joined by "/" with no leading or trailing slash. Segments
// are URL-safe words; a leading underscore is reserved for byagent's own
// files (/_cover.svg, /_unlock, /_rt/).
func NormalizePath(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", nil
	}
	segs := strings.Split(p, "/")
	if len(segs) > maxMountDepth {
		return "", errors.New("a mount path has at most 5 segments")
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || len(s) > maxSegment {
			return "", errors.New("a mount path is segments like /docs or /docs/api")
		}
		if s[0] == '_' {
			return "", errors.New("mount path segments cannot start with _")
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '~') {
				return "", errors.New("mount path segments use letters, digits, '-', '.' and '~'")
			}
		}
	}
	return strings.Join(segs, "/"), nil
}

// DisplayPath is how a stored mount path is shown: "/" or "/docs".
func DisplayPath(p string) string { return "/" + p }

// Base is the URL path prefix a mount's files live under: "/" or "/docs/".
func Base(p string) string {
	if p == "" {
		return "/"
	}
	return "/" + p + "/"
}
