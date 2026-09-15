package api

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresProxyHeadersWhenUntrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4444"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if got := clientIP(r, false); got != "10.0.0.5" {
		t.Fatalf("clientIP = %q, want 10.0.0.5 (proxy headers must not be trusted)", got)
	}

	// Two requests with different spoofed XFF values must share one bucket.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.5:5555"
	r2.Header.Set("X-Forwarded-For", "8.8.8.8")
	if clientIP(r2, false) != clientIP(r, false) {
		t.Fatal("spoofed XFF produced separate buckets with TRUST_PROXY_HEADERS off")
	}
}

func TestClientIPUsesProxyHeadersWhenTrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4444"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	if got := clientIP(r, true); got != "1.2.3.4" {
		t.Fatalf("clientIP = %q, want 1.2.3.4", got)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.5:5555"
	r2.Header.Set("X-Forwarded-For", "8.8.8.8")
	if clientIP(r2, true) == clientIP(r, true) {
		t.Fatal("distinct XFF values must produce separate buckets when trusted")
	}

	cf := httptest.NewRequest("GET", "/", nil)
	cf.RemoteAddr = "10.0.0.5:4444"
	cf.Header.Set("CF-Connecting-IP", "9.9.9.9")
	cf.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(cf, true); got != "9.9.9.9" {
		t.Fatalf("CF-Connecting-IP should win, got %q", got)
	}
}

func TestClientIPHandlesIPv6RemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8::1]:4444"
	if got := clientIP(r, false); got != "2001:db8::1" {
		t.Fatalf("clientIP = %q, want 2001:db8::1", got)
	}
}
