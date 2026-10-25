// Package links checks the URLs agents register as links and names the
// service a link lives on, for the dashboard's badge.
package links

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// MaxPerTenant caps a workspace's links. A hook registers a deploy URL on
// every deploy, so this is set well above what a person ever files by hand.
const MaxPerTenant = 2000

// MaxURL is the longest URL kept; longer ones are almost always signed or
// tracking URLs that stop working anyway.
const MaxURL = 2048

// Normalize checks raw as a link and returns the form it is stored under,
// with its host. Only http and https URLs with a public-looking host are
// links; credentials and fragments are dropped, the host lower-cased.
// ownHosts are the server's own host names: a byagent page is published,
// not linked.
func Normalize(raw string, ownHosts []string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("url is required")
	}
	if len(raw) > MaxURL {
		return "", "", errors.New("url is longer than 2048 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", errors.New("url must be an absolute http or https URL")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return "", "", errors.New("url must be on a public host, not " + host)
	}
	for _, own := range ownHosts {
		own = strings.ToLower(own)
		if own != "" && (host == own || strings.HasSuffix(host, "."+own)) {
			return "", "", errors.New("that is already a byagent page; links are for work hosted somewhere else")
		}
	}
	u.User = nil
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	if port := u.Port(); port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), host, nil
}

// providers maps a host suffix to the service's name. Order matters only
// where one suffix ends another, and none here do.
var providers = []struct{ suffix, name string }{
	{"vercel.app", "Vercel"},
	{"netlify.app", "Netlify"},
	{"pages.dev", "Cloudflare Pages"},
	{"workers.dev", "Cloudflare Workers"},
	{"github.io", "GitHub Pages"},
	{"gitlab.io", "GitLab Pages"},
	{"here.now", "here.now"},
	{"fly.dev", "Fly"},
	{"onrender.com", "Render"},
	{"up.railway.app", "Railway"},
	{"surge.sh", "Surge"},
	{"web.app", "Firebase"},
	{"firebaseapp.com", "Firebase"},
	{"deno.dev", "Deno Deploy"},
	{"val.run", "Val Town"},
	{"replit.app", "Replit"},
	{"lovable.app", "Lovable"},
	{"claude.site", "Claude"},
	{"claude.ai", "Claude"},
	{"chatgpt.com", "ChatGPT"},
	{"grok.com", "Grok"},
	{"x.ai", "Grok"},
	{"gist.github.com", "GitHub Gist"},
	{"codepen.io", "CodePen"},
	{"stackblitz.io", "StackBlitz"},
}

// Provider names the service a host belongs to, or returns the host itself
// (without a leading www.) when it is not one byagent knows.
func Provider(host string) string {
	host = strings.ToLower(host)
	for _, p := range providers {
		if host == p.suffix || strings.HasSuffix(host, "."+p.suffix) {
			return p.name
		}
	}
	return strings.TrimPrefix(host, "www.")
}
