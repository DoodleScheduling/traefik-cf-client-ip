package traefik_cf_client_ip

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const proxyIP = "10.0.0.10"

// serve runs the middleware and returns the request headers the next handler received.
func serve(t *testing.T, headers http.Header) http.Header {
	t.Helper()
	var seen http.Header
	h, err := New(context.Background(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
	}), CreateConfig(), "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/login", nil)
	req.RemoteAddr = proxyIP + ":4711"
	req.Header = headers
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == nil {
		t.Fatal("next handler was not called")
	}
	return seen
}

func expect(t *testing.T, h http.Header, name string, want ...string) {
	t.Helper()
	if got := h.Values(name); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

func TestOverwritesWithValidIP(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ipv4", "198.51.100.7", "198.51.100.7"},
		{"ipv4 with spaces", " 198.51.100.7 ", "198.51.100.7"},
		{"ipv6", "2001:db8::1", "2001:db8::1"},
		{"ipv6 long form", "2001:DB8:0:0::1", "2001:db8::1"},
		{"ipv4-mapped ipv6", "::ffff:198.51.100.7", "198.51.100.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serve(t, http.Header{
				"Cf-Connecting-Ip": {tc.in},
				"X-Forwarded-For":  {"203.0.113.77, 198.51.100.7", proxyIP},
				"X-Real-Ip":        {proxyIP, "203.0.113.77"},
			})
			expect(t, got, "X-Forwarded-For", tc.want)
			expect(t, got, "X-Real-Ip", tc.want)
			expect(t, got, "Cf-Connecting-Ip", tc.in)
		})
	}
}

func TestHeaderNameCaseDoesNotMatter(t *testing.T) {
	for _, name := range []string{"CF-Connecting-IP", "cf-connecting-ip", "CF-CONNECTING-IP"} {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			h.Set(name, "198.51.100.7")
			expect(t, serve(t, h), "X-Forwarded-For", "198.51.100.7")
		})
	}
}

func TestLeavesRequestUnchangedWithoutOneValidIP(t *testing.T) {
	cases := map[string][]string{
		"missing":       nil,
		"empty":         {""},
		"blank":         {"  "},
		"not an ip":     {"not-an-ip"},
		"list":          {"198.51.100.7, 203.0.113.1"},
		"duplicated":    {"198.51.100.7", "203.0.113.1"},
		"same twice":    {"198.51.100.7", "198.51.100.7"},
		"with port":     {"198.51.100.7:443"},
		"cidr":          {"198.51.100.0/24"},
		"ipv6 zone":     {"fe80::1%eth0"},
		"header inject": {"198.51.100.7\r\nX-Evil: 1"},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			h := http.Header{"X-Forwarded-For": {proxyIP}, "X-Real-Ip": {proxyIP}}
			if values != nil {
				h["Cf-Connecting-Ip"] = values
			}
			got := serve(t, h)
			expect(t, got, "X-Forwarded-For", proxyIP)
			expect(t, got, "X-Real-Ip", proxyIP)
		})
	}
}

func TestNeverWritesEmptyValues(t *testing.T) {
	got := serve(t, http.Header{})
	expect(t, got, "X-Forwarded-For")
	expect(t, got, "X-Real-Ip")
}

func TestOnlyTouchesClientIPHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h, err := New(context.Background(), next, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api", strings.NewReader("body"))
	req.Header.Set("Cf-Connecting-Ip", "198.51.100.7")
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
	if len(rec.Header()) != 0 {
		t.Errorf("response headers changed: %v", rec.Header())
	}
	for name, want := range map[string]string{"Authorization": "Bearer x", "Connection": "Upgrade", "Upgrade": "websocket"} {
		if got := req.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestBehindReverseProxy(t *testing.T) {
	// The middleware runs in front of a reverse proxy that appends its peer address to
	// X-Forwarded-For, as Traefik does, so the backend sees "<visitor>, <peer>".
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s|%s", r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-Ip"))
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(context.Background(), httputil.NewSingleHostReverseProxy(target), CreateConfig(), "test")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.RemoteAddr = proxyIP + ":4711"
	req.Header.Set("Cf-Connecting-Ip", "198.51.100.7")
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got, want := rec.Body.String(), "198.51.100.7, "+proxyIP+"|198.51.100.7"; got != want {
		t.Errorf("backend saw %q, want %q", got, want)
	}
}

func TestConcurrentRequests(t *testing.T) {
	h, err := New(context.Background(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != r.Header.Get("X-Want") {
			t.Errorf("got %q want %q", r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Want"))
		}
	}), CreateConfig(), "test")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := fmt.Sprintf("198.51.100.%d", i%250)
			req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
			req.Header.Set("Cf-Connecting-Ip", ip)
			req.Header.Set("X-Want", ip)
			h.ServeHTTP(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()
}

func BenchmarkServeHTTP(b *testing.B) {
	h, err := New(context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), CreateConfig(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	rec := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req.Header.Set("Cf-Connecting-Ip", "198.51.100.7")
		h.ServeHTTP(rec, req)
	}
}
