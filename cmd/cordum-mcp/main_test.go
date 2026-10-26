package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveTransportConfig_DefaultStdio(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "")
	t.Setenv("MCP_HTTP_ADDR", "")

	mode, addr, err := resolveTransportConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "stdio" {
		t.Fatalf("expected mode=stdio, got %q", mode)
	}
	if addr != defaultHTTPAddr {
		t.Fatalf("expected addr=%s, got %q", defaultHTTPAddr, addr)
	}
}

func TestResolveTransportConfig_ExplicitStdio(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "stdio")
	t.Setenv("MCP_HTTP_ADDR", "")

	mode, _, err := resolveTransportConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "stdio" {
		t.Fatalf("expected mode=stdio, got %q", mode)
	}
}

func TestResolveTransportConfig_HTTP(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", "")

	mode, addr, err := resolveTransportConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "http" {
		t.Fatalf("expected mode=http, got %q", mode)
	}
	if addr != defaultHTTPAddr {
		t.Fatalf("expected default addr=%s, got %q", defaultHTTPAddr, addr)
	}
}

func TestResolveTransportConfig_HTTPCustomAddr(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("MCP_HTTP_ADDR", ":9999")

	mode, addr, err := resolveTransportConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "http" {
		t.Fatalf("expected mode=http, got %q", mode)
	}
	if addr != ":9999" {
		t.Fatalf("expected addr=:9999, got %q", addr)
	}
}

func TestResolveTransportConfig_CaseInsensitive(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "HTTP")
	t.Setenv("MCP_HTTP_ADDR", "")

	mode, _, err := resolveTransportConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "http" {
		t.Fatalf("expected mode=http, got %q", mode)
	}
}

func TestResolveTransportConfig_InvalidMode(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "grpc")
	t.Setenv("MCP_HTTP_ADDR", "")

	_, _, err := resolveTransportConfig()
	if err == nil {
		t.Fatal("expected error for invalid transport mode")
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_MCP_VAR", "custom")
	if v := envOrDefault("TEST_MCP_VAR", "fallback"); v != "custom" {
		t.Fatalf("expected custom, got %q", v)
	}

	t.Setenv("TEST_MCP_VAR", "")
	if v := envOrDefault("TEST_MCP_VAR", "fallback"); v != "fallback" {
		t.Fatalf("expected fallback, got %q", v)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		input  string
		expect int
	}{
		{"", 0},
		{"  ", 0},
		{"a,b,c", 3},
		{"a , b , c", 3},
		{"single", 1},
	}
	for _, tc := range cases {
		got := splitCSV(tc.input)
		if len(got) != tc.expect {
			t.Errorf("splitCSV(%q) = %d items, want %d", tc.input, len(got), tc.expect)
		}
	}
}

func TestDefaultHTTPAddrIsLoopback(t *testing.T) {
	if !isLoopbackListenAddr(defaultHTTPAddr) {
		t.Fatalf("defaultHTTPAddr %q must bind loopback only", defaultHTTPAddr)
	}
}

func TestCheckHTTPExposure(t *testing.T) {
	cases := []struct {
		addr, token string
		allow       bool
		wantErr     bool
	}{
		{"127.0.0.1:8090", "", false, false},
		{"localhost:8090", "", false, false},
		{"[::1]:8090", "", false, false},
		{":8090", "", false, true},
		{"0.0.0.0:8090", "", false, true},
		{"[::]:8090", "", false, true},
		{"10.0.0.5:8090", "", false, true},
		{"mcp.internal:8090", "", false, true},
		{"not-an-addr", "", false, true},
		{":8090", "secret", false, false},
		{":8090", "", true, false},
	}
	for _, tc := range cases {
		err := checkHTTPExposure(tc.addr, tc.token, tc.allow)
		if (err != nil) != tc.wantErr {
			t.Errorf("checkHTTPExposure(%q, token=%t, allow=%t) err=%v, wantErr=%t", tc.addr, tc.token != "", tc.allow, err, tc.wantErr)
		}
	}
}

func TestRequireBearer(t *testing.T) {
	next := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	status := func(h http.HandlerFunc, authorization string) int {
		req := httptest.NewRequest(http.MethodPost, "/message", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr.Code
	}
	h := requireBearer("s3cret", next)
	for authorization, want := range map[string]int{
		"":                    http.StatusUnauthorized,
		"Bearer wrong":        http.StatusUnauthorized,
		"Bearer s3cret-extra": http.StatusUnauthorized,
		"Basic s3cret":        http.StatusUnauthorized,
		"s3cret":              http.StatusUnauthorized,
		"Bearer s3cret":       http.StatusNoContent,
		"bearer s3cret":       http.StatusNoContent,
	} {
		if got := status(h, authorization); got != want {
			t.Errorf("Authorization %q: status %d, want %d", authorization, got, want)
		}
	}
	if got := status(requireBearer("", next), ""); got != http.StatusNoContent {
		t.Errorf("no token configured: status %d, want passthrough", got)
	}
}
