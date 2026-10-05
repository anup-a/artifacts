package proxy

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "127.255.0.9", "10.0.0.1", "172.16.5.4", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "100.64.0.1", "100.100.100.200", "0.0.0.0", "0.1.2.3",
		"192.0.0.170", "192.0.2.1", "198.18.0.1", "198.51.100.7", "203.0.113.9", "224.0.0.1",
		"239.1.2.3", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fe80::1", "fc00::1", "fd00:ec2::254", "fec0::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "::ffff:10.0.0.1",
		"64:ff9b::a9fe:a9fe", "64:ff9b:1::1", "2002:7f00:1::", "2001::1", "2001:db8::1", "100::1",
		"::7f00:1", "::a9fe:a9fe", "::ffff:0:7f00:1", "::ffff:0:a9fe:a9fe", "5f00::1", "3fff::1", "3fff:fff::1",
	} {
		if PublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "104.16.0.1", "2606:4700:4700::1111", "2a00:1450:4001::200e"} {
		if !PublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s must be allowed", s)
		}
	}
	if PublicAddr(netip.Addr{}) {
		t.Error("the zero address must be blocked")
	}
}

func fixedResolve(addrs ...string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range addrs {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestDialerRefusesPrivateAnswers(t *testing.T) {
	ctx := context.Background()
	cases := map[string][]string{
		"loopback":             {"127.0.0.1"},
		"metadata":             {"169.254.169.254"},
		"mixed public+private": {"93.184.216.34", "10.0.0.5"},
		"mapped loopback":      {"::ffff:127.0.0.1"},
		"cgnat":                {"100.64.1.1"},
		"ipv6 unique local":    {"fd12::1"},
	}
	for name, addrs := range cases {
		d := &dialer{resolve: fixedResolve(addrs...), allow: PublicAddr, port: "443"}
		if _, err := d.dial(ctx, "tcp", "api.example.com:443"); !errors.Is(err, errBlocked) {
			t.Errorf("%s: dial error %v, want blocked", name, err)
		}
	}
	d := &dialer{resolve: fixedResolve("93.184.216.34"), allow: PublicAddr, port: "443"}
	if _, err := d.dial(ctx, "tcp", "api.example.com:8080"); !errors.Is(err, errBlocked) {
		t.Errorf("non-443 port: %v", err)
	}
}

// The socket-level check holds even when the resolver vets an address the
// dial then does not use: what is connected to is checked as it connects.
func TestDialerChecksTheSocketAddress(t *testing.T) {
	calls := 0
	allow := func(ip netip.Addr) bool {
		calls++
		return calls == 1 // the resolver pass approves, the socket pass refuses
	}
	d := &dialer{resolve: fixedResolve("127.0.0.1"), allow: allow, port: "1"}
	if _, err := d.dial(context.Background(), "tcp", "api.example.com:443"); !errors.Is(err, errBlocked) {
		t.Fatalf("socket check skipped: %v", err)
	}
}
