package main

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
)

func TestMCPAuditFallbackNilExporter(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"unconfigured", nil}, {"configuration_error", errors.New("invalid audit configuration")}} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureAuditLogs(t)
			b := newAuditFallback(func() (*audit.BufferedExporter, error) { return nil, tc.err })
			if b == nil {
				t.Fatal("expected a fallback buffer")
			}
			t.Cleanup(func() { closeAuditFallback(t, b) })
			if _, ok := b.Backend().(stderrAuditExporter); !ok {
				t.Fatalf("backend = %T, want stderrAuditExporter", b.Backend())
			}
			if got := strings.Contains(logs.String(), "WARN"); got != (tc.err != nil) {
				t.Fatalf("configuration warning = %v, want %v", got, tc.err != nil)
			}
		})
	}
	t.Run("configured", func(t *testing.T) {
		configured := audit.NewBufferedExporter(audit.NewDiscardExporter())
		t.Cleanup(func() { closeAuditFallback(t, configured) })
		if got := newAuditFallback(func() (*audit.BufferedExporter, error) { return configured, nil }); got != configured {
			t.Fatal("configured exporter was replaced")
		}
	})
	t.Run("stderr_drain", func(t *testing.T) {
		logs := captureAuditLogs(t)
		b := newAuditFallback(func() (*audit.BufferedExporter, error) { return nil, nil })
		b.Send(audit.SIEMEvent{EventType: "test.mcp.fallback"})
		closeAuditFallback(t, b)
		if !strings.Contains(logs.String(), "test.mcp.fallback") {
			t.Fatalf("stderr event missing: %s", logs.String())
		}
	})
}

func captureAuditLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func closeAuditFallback(t *testing.T, b *audit.BufferedExporter) {
	t.Helper()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

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
