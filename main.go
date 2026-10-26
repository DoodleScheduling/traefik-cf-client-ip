// Package traefik_cf_client_ip sets the client IP headers from the header Cloudflare sets.
//
// Behind a Kubernetes LoadBalancer with externalTrafficPolicy: Cluster, Traefik sees the
// node's internal address as the client and writes it into X-Forwarded-For and X-Real-Ip.
// Cloudflare sets CF-Connecting-IP to the visitor's IP on every request and rejects requests
// that try to send it, so this middleware copies it into the headers the backends read
// (for example Keycloak with KC_PROXY_HEADERS=xforwarded). Traefik's proxy appends its own
// peer address to X-Forwarded-For afterwards, so the first entry is the visitor.
//
// Requests without exactly one valid IP in the source header are passed through unchanged.
// When trustedIPs is set, the source header is only used if the TCP peer is in that list;
// from any other peer it is removed, so backends that read it directly cannot be spoofed.
package traefik_cf_client_ip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

const defaultSourceHeader = "CF-Connecting-IP"

// Config holds the plugin configuration.
type Config struct {
	// SourceHeader carries the visitor's IP. Default: CF-Connecting-IP.
	SourceHeader string `json:"sourceHeader,omitempty"`
	// TargetHeaders are overwritten with that IP. Default: X-Forwarded-For, X-Real-Ip.
	TargetHeaders []string `json:"targetHeaders,omitempty"`
	// TrustedIPs restricts which TCP peers (IPs or CIDRs) may set the source header.
	// Empty means every peer is trusted.
	TrustedIPs []string `json:"trustedIPs,omitempty"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		SourceHeader:  defaultSourceHeader,
		TargetHeaders: []string{"X-Forwarded-For", "X-Real-Ip"},
		TrustedIPs:    []string{},
	}
}

// ClientIP is the middleware.
type ClientIP struct {
	next          http.Handler
	name          string
	sourceHeader  string
	targetHeaders []string
	trusted       []*net.IPNet
}

// New creates the middleware.
func New(_ context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if config == nil {
		return nil, fmt.Errorf("config can not be nil")
	}

	c := &ClientIP{
		next:         next,
		name:         name,
		sourceHeader: http.CanonicalHeaderKey(strings.TrimSpace(config.SourceHeader)),
	}
	if c.sourceHeader == "" {
		c.sourceHeader = http.CanonicalHeaderKey(defaultSourceHeader)
	}

	targets := config.TargetHeaders
	if len(targets) == 0 {
		targets = CreateConfig().TargetHeaders
	}
	for _, h := range targets {
		h = http.CanonicalHeaderKey(strings.TrimSpace(h))
		if h == "" {
			return nil, fmt.Errorf("targetHeaders must not contain empty names")
		}
		if h == c.sourceHeader {
			return nil, fmt.Errorf("targetHeaders must not contain the source header %q", h)
		}
		c.targetHeaders = append(c.targetHeaders, h)
	}

	for _, s := range config.TrustedIPs {
		n, err := parseNet(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("invalid trustedIPs entry %q: %w", s, err)
		}
		c.trusted = append(c.trusted, n)
	}

	return c, nil
}

func (c *ClientIP) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !c.isTrustedPeer(req.RemoteAddr) {
		req.Header.Del(c.sourceHeader)
	} else if ip := clientIP(req.Header.Values(c.sourceHeader)); ip != "" {
		for _, h := range c.targetHeaders {
			req.Header.Set(h, ip)
		}
	}

	c.next.ServeHTTP(rw, req)
}

func (c *ClientIP) isTrustedPeer(remoteAddr string) bool {
	if len(c.trusted) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range c.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the IP when the header is present exactly once and holds a single valid IP.
func clientIP(values []string) string {
	if len(values) != 1 {
		return ""
	}
	ip := net.ParseIP(strings.TrimSpace(values[0]))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// parseNet accepts a CIDR or a single IP.
func parseNet(s string) (*net.IPNet, error) {
	if strings.Contains(s, "/") {
		_, n, err := net.ParseCIDR(s)
		return n, err
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("not an IP or CIDR")
	}
	bits := 128
	if ip.To4() != nil {
		ip = ip.To4()
		bits = 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}
