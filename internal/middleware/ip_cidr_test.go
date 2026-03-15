package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteIP_StripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.10:54321"

	got := remoteIP(req)
	if got != "203.0.113.10" {
		t.Fatalf("expected remote ip 203.0.113.10, got %s", got)
	}
}

func TestExtractClientIPFromTrustedProxy_UsesXForwardedFor(t *testing.T) {
	trustedCIDRs, err := parseCIDRs([]string{"10.0.0.0/8"}, "trusted proxy")
	if err != nil {
		t.Fatalf("parse cidrs failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "10.1.2.3:12345"
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 10.1.2.3")

	got := extractClientIPFromTrustedProxy(req, trustedCIDRs)
	if got != "198.51.100.10" {
		t.Fatalf("expected client ip 198.51.100.10, got %s", got)
	}
}

func TestExtractClientIPFromTrustedProxy_IgnoresForwardedHeadersFromUntrustedSource(t *testing.T) {
	trustedCIDRs, err := parseCIDRs([]string{"10.0.0.0/8"}, "trusted proxy")
	if err != nil {
		t.Fatalf("parse cidrs failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "198.51.100.200:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.10")

	got := extractClientIPFromTrustedProxy(req, trustedCIDRs)
	if got != "198.51.100.200" {
		t.Fatalf("expected client ip 198.51.100.200, got %s", got)
	}
}

func TestExtractClientIPFromTrustedProxy_FallsBackToXRealIPWhenXFFInvalid(t *testing.T) {
	trustedCIDRs, err := parseCIDRs([]string{"10.0.0.0/8"}, "trusted proxy")
	if err != nil {
		t.Fatalf("parse cidrs failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	req.RemoteAddr = "10.1.2.3:12345"
	req.Header.Set("X-Forwarded-For", "unknown, garbage")
	req.Header.Set("X-Real-IP", "198.51.100.11")

	got := extractClientIPFromTrustedProxy(req, trustedCIDRs)
	if got != "198.51.100.11" {
		t.Fatalf("expected client ip 198.51.100.11, got %s", got)
	}
}

func TestCIDRCache_Reset(t *testing.T) {
	cache := &cidrCache{}

	nets, err := cache.Load([]string{"127.0.0.1/32"}, "test")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if len(nets) != 1 {
		t.Fatalf("expected 1 cidr, got %d", len(nets))
	}

	cache.Reset()

	nets, err = cache.Load([]string{"10.0.0.0/8"}, "test")
	if err != nil {
		t.Fatalf("unexpected load error after reset: %v", err)
	}
	if len(nets) != 1 {
		t.Fatalf("expected 1 cidr after reset, got %d", len(nets))
	}
	if !nets[0].Contains([]byte{10, 1, 2, 3}) {
		t.Fatal("expected reset cache to reload new cidr")
	}
}
