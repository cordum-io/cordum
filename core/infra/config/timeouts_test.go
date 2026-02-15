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

func TestLoadTimeouts_EmptyPath(t *testing.T) {
	cfg, err := LoadTimeouts("")
	if err != nil {
		t.Fatalf("expected no error for empty path, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default config")
	}
	if cfg.Reconciler.DispatchTimeoutSeconds != 300 {
		t.Fatalf("expected default dispatch timeout 300, got %d", cfg.Reconciler.DispatchTimeoutSeconds)
	}
}

func TestLoadTimeouts_MalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("this is not valid yaml: [[["), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadTimeouts(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
	if cfg != nil {
		t.Fatal("expected nil config for malformed file (not defaults)")
	}
}

func TestLoadTimeouts_InvalidValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	data := []byte("topics:\n  bad-topic:\n    timeout_seconds: -10\n    max_retries: 3\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadTimeouts(path)
	if err == nil {
		t.Fatal("expected error for negative timeout value")
	}
	if cfg != nil {
		t.Fatal("expected nil config for invalid values (not defaults)")
	}
}

func TestLoadTimeouts_ValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "valid.yaml")
	data := []byte("reconciler:\n  dispatch_timeout_seconds: 600\n  running_timeout_seconds: 1800\n  scan_interval_seconds: 60\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadTimeouts(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Reconciler.DispatchTimeoutSeconds != 600 {
		t.Fatalf("expected dispatch timeout 600, got %d", cfg.Reconciler.DispatchTimeoutSeconds)
	}
}

func TestDefaultTimeouts_ReturnsValidDefaults(t *testing.T) {
	cfg := DefaultTimeouts()
	if cfg == nil {
		t.Fatal("expected non-nil defaults")
	}
	if cfg.Reconciler.DispatchTimeoutSeconds != 300 {
		t.Fatalf("expected default dispatch 300, got %d", cfg.Reconciler.DispatchTimeoutSeconds)
	}
	if cfg.Reconciler.RunningTimeoutSeconds != 9000 {
		t.Fatalf("expected default running 9000, got %d", cfg.Reconciler.RunningTimeoutSeconds)
	}
	if cfg.Reconciler.ScanIntervalSeconds != 30 {
		t.Fatalf("expected default scan 30, got %d", cfg.Reconciler.ScanIntervalSeconds)
	}
}
