package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
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

func TestTrimIPv6Brackets(t *testing.T) {
	got := trimIPv6Brackets("[2001:db8::1]")
	if got != "2001:db8::1" {
		t.Fatalf("expected trimmed ipv6, got %s", got)
	}
}

func TestNormalizeIPCandidate_HostPort(t *testing.T) {
	got := normalizeIPCandidate("198.51.100.10:443")
	if got != "198.51.100.10" {
		t.Fatalf("expected normalized ip 198.51.100.10, got %s", got)
	}
}

func TestParseCIDRs_Invalid(t *testing.T) {
	_, err := parseCIDRs([]string{"not-a-cidr"}, "test")
	if err == nil {
		t.Fatal("expected parse error for invalid cidr")
	}
}

func TestMetricsIPAllowed_UsesDirectRemoteIPOnly(t *testing.T) {
	resetMetricsEndpointRuntimeForTest()

	config.GlobalConfig = &config.Config{
		Metrics: config.MetricsConfig{
			AllowedCIDRs:   []string{"127.0.0.1/32"},
			RateLimitRPS:   100,
			RateLimitBurst: 100,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	if isMetricsIPAllowed(req) {
		t.Fatal("expected metrics ip check to ignore forwarded headers and use direct remote ip")
	}
}

func TestIPInCIDRs(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}

	if !ipInCIDRs("10.1.2.3", []*net.IPNet{ipNet}) {
		t.Fatal("expected ip to be in cidr")
	}

	if ipInCIDRs("192.168.1.1", []*net.IPNet{ipNet}) {
		t.Fatal("expected ip to be outside cidr")
	}
}
