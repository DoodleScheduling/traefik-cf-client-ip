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

const nodeIP = "100.96.9.208"

type result struct {
	header http.Header
	called bool
}

func newHandler(t *testing.T, config *Config) (http.Handler, *result) {
	t.Helper()
	res := &result{}
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		res.called = true
		res.header = r.Header.Clone()
	})
	h, err := New(context.Background(), next, config, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h, res
}

func serve(t *testing.T, config *Config, remoteAddr string, headers http.Header) http.Header {
	t.Helper()
	h, res := newHandler(t, config)
	req := httptest.NewRequest(http.MethodGet, "https://doodle.com/login", nil)
	req.RemoteAddr = remoteAddr
	req.Header = headers
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !res.called {
		t.Fatal("next handler was not called")
	}
	return res.header
}

func expectHeader(t *testing.T, h http.Header, name string, want ...string) {
	t.Helper()
	got := h.Values(name)
	if len(want) == 0 {
		if len(got) != 0 {
			t.Errorf("%s = %q, want absent", name, got)
		}
		return
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

func TestCopiesCloudflareIP(t *testing.T) {
	got := serve(t, CreateConfig(), nodeIP+":4711", http.Header{
		"Cf-Connecting-Ip": {"198.51.100.7"},
		"X-Forwarded-For":  {nodeIP},
		"X-Real-Ip":        {nodeIP},
	})
	expectHeader(t, got, "X-Forwarded-For", "198.51.100.7")
	expectHeader(t, got, "X-Real-Ip", "198.51.100.7")
	expectHeader(t, got, "Cf-Connecting-Ip", "198.51.100.7")
}

func TestReplacesClientChosenChain(t *testing.T) {
	got := serve(t, CreateConfig(), nodeIP+":4711", http.Header{
		"Cf-Connecting-Ip": {"198.51.100.7"},
		"X-Forwarded-For":  {"203.0.113.77, 198.51.100.7", "10.0.0.1"},
	})
	expectHeader(t, got, "X-Forwarded-For", "198.51.100.7")
}

func TestHeaderNameCaseDoesNotMatter(t *testing.T) {
	for _, name := range []string{"CF-Connecting-IP", "cf-connecting-ip", "Cf-Connecting-Ip", "CF-CONNECTING-IP"} {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			h.Set(name, "198.51.100.7")
			expectHeader(t, serve(t, CreateConfig(), nodeIP+":1", h), "X-Forwarded-For", "198.51.100.7")
		})
	}
	for _, name := range []string{"cf-connecting-ip", "CF-CONNECTING-IP"} {
		t.Run("config "+name, func(t *testing.T) {
			got := serve(t, &Config{SourceHeader: name}, nodeIP+":1", http.Header{"Cf-Connecting-Ip": {"198.51.100.7"}})
			expectHeader(t, got, "X-Forwarded-For", "198.51.100.7")
		})
	}
}

func TestNormalisesIPs(t *testing.T) {
	cases := map[string]string{
		" 198.51.100.7 ":      "198.51.100.7",
		"2001:db8::1":         "2001:db8::1",
		"2001:DB8:0:0::1":     "2001:db8::1",
		"::ffff:198.51.100.7": "198.51.100.7",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := serve(t, CreateConfig(), nodeIP+":1", http.Header{"Cf-Connecting-Ip": {in}})
			expectHeader(t, got, "X-Forwarded-For", want)
			expectHeader(t, got, "X-Real-Ip", want)
		})
	}
}

func TestPassThroughWithoutUsableHeader(t *testing.T) {
	cases := map[string]http.Header{
		"missing":       {},
		"empty":         {"Cf-Connecting-Ip": {""}},
		"blank":         {"Cf-Connecting-Ip": {"  "}},
		"not an ip":     {"Cf-Connecting-Ip": {"not-an-ip"}},
		"list":          {"Cf-Connecting-Ip": {"198.51.100.7, 203.0.113.1"}},
		"repeated":      {"Cf-Connecting-Ip": {"198.51.100.7", "203.0.113.1"}},
		"with port":     {"Cf-Connecting-Ip": {"198.51.100.7:443"}},
		"cidr":          {"Cf-Connecting-Ip": {"198.51.100.0/24"}},
		"ipv6 zone":     {"Cf-Connecting-Ip": {"fe80::1%eth0"}},
		"header inject": {"Cf-Connecting-Ip": {"198.51.100.7\r\nX-Evil: 1"}},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			h.Set("X-Forwarded-For", nodeIP)
			h.Set("X-Real-Ip", nodeIP)
			got := serve(t, CreateConfig(), nodeIP+":1", h)
			expectHeader(t, got, "X-Forwarded-For", nodeIP)
			expectHeader(t, got, "X-Real-Ip", nodeIP)
		})
	}
}

func TestNoEmptyValuesWhenHeaderMissing(t *testing.T) {
	// cloudflarewarp wrote empty values here ("X-Forwarded-For: , 10.0.0.1"); we must not.
	got := serve(t, CreateConfig(), nodeIP+":1", http.Header{})
	expectHeader(t, got, "X-Forwarded-For")
	expectHeader(t, got, "X-Real-Ip")
}

func TestTrustedIPs(t *testing.T) {
	config := &Config{TrustedIPs: []string{"100.96.0.0/11", "10.1.2.3", "2001:db8:ffff::/48"}}
	cases := []struct {
		name       string
		remoteAddr string
		trusted    bool
	}{
		{"pod network", "100.96.9.208:4711", true},
		{"single ip", "10.1.2.3:80", true},
		{"ipv6 cidr", "[2001:db8:ffff::5]:443", true},
		{"outside", "192.168.50.10:4711", false},
		{"neighbour of single ip", "10.1.2.4:80", false},
		{"no port", "100.96.9.208", true},
		{"garbage", "not-an-address", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serve(t, config, tc.remoteAddr, http.Header{
				"Cf-Connecting-Ip": {"198.51.100.7"},
				"X-Forwarded-For":  {nodeIP},
			})
			if tc.trusted {
				expectHeader(t, got, "X-Forwarded-For", "198.51.100.7")
				expectHeader(t, got, "Cf-Connecting-Ip", "198.51.100.7")
			} else {
				expectHeader(t, got, "X-Forwarded-For", nodeIP)
				expectHeader(t, got, "Cf-Connecting-Ip")
			}
		})
	}
}

func TestCustomHeaders(t *testing.T) {
	got := serve(t, &Config{SourceHeader: "True-Client-IP", TargetHeaders: []string{"X-Client-Ip"}}, nodeIP+":1", http.Header{
		"True-Client-Ip":   {"198.51.100.7"},
		"Cf-Connecting-Ip": {"203.0.113.1"},
	})
	expectHeader(t, got, "X-Client-Ip", "198.51.100.7")
	expectHeader(t, got, "X-Forwarded-For")
	expectHeader(t, got, "X-Real-Ip")
}

func TestEmptyConfigUsesDefaults(t *testing.T) {
	got := serve(t, &Config{}, nodeIP+":1", http.Header{"Cf-Connecting-Ip": {"198.51.100.7"}})
	expectHeader(t, got, "X-Forwarded-For", "198.51.100.7")
	expectHeader(t, got, "X-Real-Ip", "198.51.100.7")
}

func TestInvalidConfig(t *testing.T) {
	cases := map[string]*Config{
		"nil":                nil,
		"empty target":       {TargetHeaders: []string{" "}},
		"target is source":   {TargetHeaders: []string{"cf-connecting-ip"}},
		"bad trusted ip":     {TrustedIPs: []string{"100.96.0.0/33"}},
		"bad trusted string": {TrustedIPs: []string{"cloudflare"}},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(context.Background(), http.NotFoundHandler(), config, "x"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestWebsocketUpgradePassesThrough(t *testing.T) {
	// cloudflarewarp panicked on websocket connections; make sure upgrades are untouched.
	got := serve(t, CreateConfig(), nodeIP+":1", http.Header{
		"Cf-Connecting-Ip":      {"198.51.100.7"},
		"Connection":            {"Upgrade"},
		"Upgrade":               {"websocket"},
		"Sec-Websocket-Key":     {"dGhlIHNhbXBsZSBub25jZQ=="},
		"Sec-Websocket-Version": {"13"},
	})
	expectHeader(t, got, "Upgrade", "websocket")
	expectHeader(t, got, "Connection", "Upgrade")
	expectHeader(t, got, "X-Forwarded-For", "198.51.100.7")
}

func TestDoesNotTouchResponseOrOtherHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen", r.Header.Get("X-Forwarded-For"))
		w.WriteHeader(http.StatusTeapot)
	})
	h, err := New(context.Background(), next, CreateConfig(), "test")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://doodle.com/api", strings.NewReader("body"))
	req.Header.Set("Cf-Connecting-Ip", "198.51.100.7")
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot || rec.Header().Get("X-Seen") != "198.51.100.7" {
		t.Fatalf("code=%d seen=%q", rec.Code, rec.Header().Get("X-Seen"))
	}
	if len(rec.Header().Values("X-Forwarded-For")) != 0 {
		t.Error("response headers must not be modified")
	}
	if req.Header.Get("Authorization") != "Bearer x" {
		t.Error("unrelated request headers must not be modified")
	}
}

func TestBehindReverseProxy(t *testing.T) {
	// End to end: the middleware runs in front of a reverse proxy that appends its peer
	// address to X-Forwarded-For, as Traefik does, so the backend sees "visitor, node".
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

	req := httptest.NewRequest(http.MethodGet, "https://doodle.com/", nil)
	req.RemoteAddr = nodeIP + ":4711"
	req.Header.Set("Cf-Connecting-Ip", "198.51.100.7")
	req.Header.Set("X-Forwarded-For", "203.0.113.77")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got, want := rec.Body.String(), "198.51.100.7, "+nodeIP+"|198.51.100.7"; got != want {
		t.Errorf("backend saw %q, want %q", got, want)
	}
}

func TestConcurrentRequests(t *testing.T) {
	h, err := New(context.Background(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != r.Header.Get("X-Want") {
			t.Errorf("got %q want %q", r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Want"))
		}
	}), &Config{TrustedIPs: []string{"100.96.0.0/11"}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := fmt.Sprintf("198.51.100.%d", i%250)
			req := httptest.NewRequest(http.MethodGet, "https://doodle.com/", nil)
			req.RemoteAddr = nodeIP + ":1"
			req.Header.Set("Cf-Connecting-Ip", ip)
			req.Header.Set("X-Want", ip)
			h.ServeHTTP(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()
}

func BenchmarkServeHTTP(b *testing.B) {
	h, err := New(context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		&Config{TrustedIPs: []string{"100.96.0.0/11"}}, "bench")
	if err != nil {
		b.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://doodle.com/", nil)
	req.RemoteAddr = nodeIP + ":1"
	rec := httptest.NewRecorder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req.Header.Set("Cf-Connecting-Ip", "198.51.100.7")
		h.ServeHTTP(rec, req)
	}
}
