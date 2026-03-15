package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
)

type cidrCache struct {
	once sync.Once
	nets []*net.IPNet
	err  error
}

func (c *cidrCache) Load(cidrs []string, fieldName string) ([]*net.IPNet, error) {
	c.once.Do(func() {
		c.nets, c.err = parseCIDRs(cidrs, fieldName)
	})
	return c.nets, c.err
}

func (c *cidrCache) Reset() {
	c.once = sync.Once{}
	c.nets = nil
	c.err = nil
}

func parseCIDRs(cidrs []string, fieldName string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}

		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid %s cidr %q: %w", fieldName, cidr, err)
		}
		nets = append(nets, ipNet)
	}
	return nets, nil
}

func remoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return strings.TrimSpace(host)
	}

	return remoteAddr
}

func ipInCIDRs(ipStr string, nets []*net.IPNet) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}

	for _, ipNet := range nets {
		if ipNet != nil && ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func extractClientIPFromTrustedProxy(r *http.Request, trustedProxyCIDRs []*net.IPNet) string {
	remote := remoteIP(r)
	if !ipInCIDRs(remote, trustedProxyCIDRs) {
		return remote
	}

	if xff := firstValidForwardedIP(r.Header.Get("X-Forwarded-For")); xff != "" {
		return xff
	}

	if xrip := normalizeIPCandidate(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}

	return remote
}

func firstValidForwardedIP(xff string) string {
	if strings.TrimSpace(xff) == "" {
		return ""
	}

	parts := strings.Split(xff, ",")
	for _, part := range parts {
		if ip := normalizeIPCandidate(part); ip != "" {
			return ip
		}
	}
	return ""
}

func normalizeIPCandidate(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}

	if ip := net.ParseIP(candidate); ip != nil {
		return ip.String()
	}

	host, _, err := net.SplitHostPort(candidate)
	if err == nil {
		host = strings.TrimSpace(host)
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}

	return ""
}
