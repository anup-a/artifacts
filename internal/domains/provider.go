package domains

import (
	"context"
	"net/http"
	"time"
)

// Provider is the edge that terminates TLS for custom hostnames: it is told
// about a hostname once its owner has proven control of it, reports when the
// certificate is live, and forgets the hostname when it is removed.
//
// Implementations call a fixed API with the hostname as data. They never
// make a request to the hostname itself.
type Provider interface {
	// Name is stored with each domain, so a server that changes provider
	// knows which hostnames still need registering with the new one.
	Name() string
	// Add registers hostname. Adding one the edge already holds is not an
	// error: it returns that hostname's state.
	Add(ctx context.Context, hostname string) (Edge, error)
	// Check reports the hostname's state at the edge.
	Check(ctx context.Context, hostname, ref string) (Edge, error)
	// Remove deletes the hostname. Removing one the edge does not hold is
	// not an error.
	Remove(ctx context.Context, hostname, ref string) error
}

// Edge is a hostname's state at the provider.
type Edge struct {
	// Ref is the provider's id for the hostname, kept for Check and Remove.
	Ref string
	// Ready means traffic for the hostname reaches this server over TLS.
	Ready bool
	// Failed means the provider has given up and the owner must act.
	Failed bool
	// SSLStatus is the provider's own word for the certificate.
	SSLStatus string
	// Message explains a hostname that is not Ready, in the provider's words.
	Message string
}

// apiClient is shared by the HTTP providers: short timeouts, no redirects
// followed, since a provider API answering with a redirect is not one we
// meant to talk to.
var apiClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// None is the provider for tests and for servers whose TLS is handled
// entirely outside byagent: every hostname is ready as soon as it is added.
type None struct{}

func (None) Name() string { return "none" }
func (None) Add(context.Context, string) (Edge, error) {
	return Edge{Ready: true, SSLStatus: "none"}, nil
}
func (None) Check(context.Context, string, string) (Edge, error) {
	return Edge{Ready: true, SSLStatus: "none"}, nil
}
func (None) Remove(context.Context, string, string) error { return nil }

// Manual records domains and leaves the certificate to the operator, who
// adds it at the edge by hand (fly certs add, a Caddy site block). byagent
// cannot see the certificate, so a verified hostname counts as ready.
type Manual struct{}

func (Manual) Name() string { return "manual" }
func (Manual) Add(context.Context, string) (Edge, error) {
	return Edge{Ready: true, SSLStatus: "manual", Message: "this server's operator adds certificates by hand"}, nil
}
func (Manual) Check(context.Context, string, string) (Edge, error) {
	return Edge{Ready: true, SSLStatus: "manual", Message: "this server's operator adds certificates by hand"}, nil
}
func (Manual) Remove(context.Context, string, string) error { return nil }
