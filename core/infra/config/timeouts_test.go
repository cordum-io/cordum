package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTimeoutsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	cfg, err := LoadTimeouts(path)
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	if cfg == nil {
		t.Fatalf("expected default config")
	}
	if cfg.Reconciler.DispatchTimeoutSeconds == 0 {
		t.Fatalf("expected default reconciler values")
	}
}

func TestLoadTimeoutsPartial(t *testing.T) {
	data := []byte("reconciler:\n  dispatch_timeout_seconds: 10\n")
	path := filepath.Join(t.TempDir(), "timeouts.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadTimeouts(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Reconciler.DispatchTimeoutSeconds != 10 {
		t.Fatalf("expected dispatch timeout override")
	}
	if cfg.Workflows == nil || cfg.Topics == nil {
		t.Fatalf("expected defaults for missing sections")
	}
}

func TestParseTimeoutsInvalid(t *testing.T) {
	cfg, err := ParseTimeouts([]byte("workflows: ["))
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if cfg == nil || cfg.Reconciler.DispatchTimeoutSeconds == 0 {
		t.Fatalf("expected defaults on parse error")
	}
}

func TestParseTimeoutsSchemaInvalid(t *testing.T) {
	cfg, err := ParseTimeouts([]byte("reconciler:\n  dispatch_timeout_seconds: -5\n"))
	if err == nil {
		t.Fatalf("expected schema error")
	}
	if cfg == nil || cfg.Reconciler.DispatchTimeoutSeconds == 0 {
		t.Fatalf("expected defaults on schema error")
	}
}
