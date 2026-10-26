// Package traefik_cf_client_ip sets X-Forwarded-For and X-Real-Ip from CF-Connecting-IP.
//
// Cloudflare sets CF-Connecting-IP to the visitor's IP. When the header holds exactly one
// valid IP, this middleware overwrites X-Forwarded-For and X-Real-Ip with it; otherwise the
// request passes through unchanged. Traefik's proxy then appends its own peer address to
// X-Forwarded-For, so backends receive "<visitor>, <peer>".
//
// The middleware does not decide who may send CF-Connecting-IP: apply it only where the
// connection is known to come from Cloudflare (for example an entrypoint that requires
// Cloudflare's Authenticated Origin Pulls client certificate).
package traefik_cf_client_ip

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// Config is empty: the middleware has no options.
type Config struct{}

// CreateConfig creates the plugin configuration.
func CreateConfig() *Config {
	return &Config{}
}

type clientIP struct {
	next http.Handler
}

// New creates the middleware.
func New(_ context.Context, next http.Handler, _ *Config, _ string) (http.Handler, error) {
	return &clientIP{next: next}, nil
}

func (c *clientIP) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if values := req.Header.Values("Cf-Connecting-Ip"); len(values) == 1 {
		if ip := net.ParseIP(strings.TrimSpace(values[0])); ip != nil {
			req.Header.Set("X-Forwarded-For", ip.String())
			req.Header.Set("X-Real-Ip", ip.String())
		}
	}
	c.next.ServeHTTP(rw, req)
}
