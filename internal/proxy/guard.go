package proxy

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// errBlocked means the upstream resolved to an address a page may not reach.
var errBlocked = errors.New("upstream address is not a public internet address")

// blocked are ranges that are not the public internet: private networks,
// loopback, link-local (and the cloud metadata service on 169.254.169.254),
// carrier-grade NAT, benchmarking and documentation ranges, multicast and the
// reserved space, and the IPv6 forms that embed an IPv4 address and could be
// used to reach any of those.
var blocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
		"2001::/32", "2001:2::/48", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10",
		"fec0::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicAddr reports whether a page's proxy route may connect to ip.
func PublicAddr(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// dialer connects only to vetted addresses. It resolves the host itself,
// refuses the whole host if any answer is not public (so a name that mixes a
// public and a private record cannot be raced), dials the vetted IP rather
// than the name (so a second lookup cannot rebind it), and checks the socket
// address once more as the connection is made.
type dialer struct {
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	allow   func(netip.Addr) bool
	port    string // the upstream port to dial; "443" outside tests
}

func (d *dialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if port != "443" {
		return nil, errBlocked
	}
	ips, err := d.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("upstream host has no addresses")
	}
	for _, ip := range ips {
		if !d.allow(ip) {
			return nil, errBlocked
		}
	}
	nd := net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !d.allow(ap.Addr()) {
			return errBlocked
		}
		return nil
	}}
	var last error
	for _, ip := range ips {
		conn, err := nd.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), d.port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

func lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}
