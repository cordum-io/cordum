package gateway

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRouteTableCapturesAllRegistrations(t *testing.T) {
	s, _, _ := newTestGateway(t)

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	got := len(s.Routes())
	want := countRegisterRouteCalls(t,
		filepath.Join("core", "controlplane", "gateway", "gateway.go"),
		filepath.Join("core", "controlplane", "gateway", "handlers_mcp.go"),
	)
	if got != want {
		t.Fatalf("registered routes = %d, want %d", got, want)
	}
}

func TestRouteRegisteredLogEmission(t *testing.T) {
	s, _, _ := newTestGateway(t)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{}))
	orig := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(orig)

	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "msg=route.registered") {
		t.Fatalf("expected route.registered log entries, got: %s", out)
	}
	for _, want := range []string{
		"method=GET",
		"path=/api/v1/audit/verify",
		"auth=admin",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in logs: %s", want, out)
		}
	}
}

func countRegisterRouteCalls(t *testing.T, relPaths ...string) int {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	total := 0
	for _, relPath := range relPaths {
		data, err := os.ReadFile(filepath.Join(repoRoot, relPath))
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		total += strings.Count(string(data), "s.registerRoute(mux,")
	}
	return total
}

var _ io.Writer = (*bytes.Buffer)(nil)
