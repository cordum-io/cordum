package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestGatewayHTTPClientTrustsCACert: a gateway whose certificate comes from a
// private CA verifies with --cacert naming that CA, and fails verification
// without it.
func TestGatewayHTTPClientTrustsCACert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	caPath := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	withCA, err := gatewayHTTPClient(caPath)
	if err != nil {
		t.Fatalf("gatewayHTTPClient(%s): %v", caPath, err)
	}
	resp, err := withCA.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with --cacert: %v", err)
	}
	_ = resp.Body.Close()

	without, err := gatewayHTTPClient("")
	if err != nil {
		t.Fatalf("gatewayHTTPClient without CA: %v", err)
	}
	if resp, err := without.Get(srv.URL); err == nil {
		_ = resp.Body.Close()
		t.Fatal("GET without --cacert verified a certificate from an untrusted CA")
	} else if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("GET without --cacert failed for another reason: %v", err)
	}
}

// TestGatewayHTTPClientRejectsUnusableCACert: an unreadable CA file, or one
// with no certificates, refuses to start instead of falling back.
func TestGatewayHTTPClientRejectsUnusableCACert(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.crt")
	if err := os.WriteFile(empty, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(t.TempDir(), "missing.crt"): "read CA certificate",
		empty: "no PEM certificates found",
	} {
		if _, err := gatewayHTTPClient(path); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("gatewayHTTPClient(%s) error = %v, want %q", path, err, want)
		}
	}
}
