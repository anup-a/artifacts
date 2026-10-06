package domains

import (
	"context"
	"net"
	"time"
)

// Resolver looks up TXT records. Tests swap in a map.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// NewResolver asks one DNS server directly ("1.1.1.1:53"), or the system
// resolver for "" or "system". Asking a public resolver rather than the
// container's own keeps a record the owner just added from hiding behind a
// negative answer some local cache is still holding.
func NewResolver(server string) Resolver {
	if server == "" || server == "system" {
		return net.DefaultResolver
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
}

// TXTPrefix is the label the ownership record sits under, and TXTValue the
// form of its value. A prefixed name never collides with the CNAME at the
// hostname itself.
const (
	TXTPrefix = "_byagent."
	txtValue  = "byagent-verify="
)

// TXTName is where the ownership record for hostname lives.
func TXTName(hostname string) string { return TXTPrefix + hostname }

// TXTValue is the record's value for a domain's token.
func TXTValue(token string) string { return txtValue + token }
