package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/fxwio/go-llm-gateway/internal/config"
)

func resetTrustedProxyCache() {
	trustedProxyOnce = sync.Once{}
	trustedProxyNets = nil
	trustedProxyErr = nil
}

func TestExtractClientIP_UntrustedProxyIgnoresForwardedHeaders(t *testing.T) {
	resetTrustedProxyCache()
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "198.51.100.10:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
	r.Header.Set("X-Real-IP", "203.0.113.20")

	ip := extractClientIP(r)
	if ip != "198.51.100.10" {
		t.Fatalf("expected remote addr ip, got %s", ip)
	}
}

func TestExtractClientIP_TrustedProxyUsesXForwardedFor(t *testing.T) {
	resetTrustedProxyCache()
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")

	ip := extractClientIP(r)
	if ip != "203.0.113.10" {
		t.Fatalf("expected 203.0.113.10, got %s", ip)
	}
}

func TestExtractClientIP_TrustedProxyUsesXRealIPAsFallback(t *testing.T) {
	resetTrustedProxyCache()
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Real-IP", "198.51.100.8")

	ip := extractClientIP(r)
	if ip != "198.51.100.8" {
		t.Fatalf("expected 198.51.100.8, got %s", ip)
	}
}

func TestExtractClientIP_FromRemoteAddrWhenNoProxyHeaders(t *testing.T) {
	resetTrustedProxyCache()
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "192.0.2.1:43210"

	ip := extractClientIP(r)
	if ip != "192.0.2.1" {
		t.Fatalf("expected 192.0.2.1, got %s", ip)
	}
}

func TestBuildRateLimitIdentity_PreferToken(t *testing.T) {
	resetTrustedProxyCache()
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "192.0.2.1:43210"

	authCtx := &ClientAuthContext{
		Token: "sk-my-gateway-token-001",
	}
	ctx := context.WithValue(r.Context(), ClientAuthContextKey, authCtx)
	r = r.WithContext(ctx)

	scope, key := buildRateLimitIdentity(r)

	if scope != "token" {
		t.Fatalf("expected scope token, got %s", scope)
	}
	if key == "" || key == "rate_limit:token:" {
		t.Fatalf("expected non-empty token rate limit key, got %s", key)
	}
}

func TestBuildRateLimitIdentity_FallbackToIP(t *testing.T) {
	resetTrustedProxyCache()
	config.GlobalConfig = &config.Config{
		Server: config.ServerConfig{
			TrustedProxyCIDRs: []string{"127.0.0.1/32"},
		},
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = "192.0.2.1:43210"
	r.Header.Set("X-Forwarded-For", "203.0.113.10")

	scope, key := buildRateLimitIdentity(r)

	if scope != "ip" {
		t.Fatalf("expected scope ip, got %s", scope)
	}
	if key != "rate_limit:ip:192.0.2.1" {
		t.Fatalf("unexpected key: %s", key)
	}
}
