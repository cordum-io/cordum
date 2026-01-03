package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPoolConfigSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pools.yaml")
	body := []byte("topics:\n  job.default: default\n  job.batch:\n    - batch\n    - batch-b\npools:\n  default:\n    requires: [\"docker\", \"git\"]\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadPoolConfig(path)
	if err != nil {
		t.Fatalf("LoadPoolConfig returned error: %v", err)
	}
	if len(cfg.Topics["job.default"]) != 1 || cfg.Topics["job.default"][0] != "default" {
		t.Fatalf("unexpected topics: %#v", cfg.Topics)
	}
	if len(cfg.Topics["job.batch"]) != 2 || cfg.Topics["job.batch"][0] != "batch" {
		t.Fatalf("unexpected batch topics: %#v", cfg.Topics["job.batch"])
	}
	if cfg.Pools["default"].Requires[0] != "docker" {
		t.Fatalf("unexpected pool requires: %#v", cfg.Pools["default"].Requires)
	}
}

func TestLoadPoolConfigErrors(t *testing.T) {
	if _, err := LoadPoolConfig("nope.yaml"); err == nil {
		t.Fatalf("expected error for missing file")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("topics: {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadPoolConfig(path); err == nil {
		t.Fatalf("expected error for empty topics")
	}
}

func TestLoadPoolTopicsLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pools.yaml")
	body := []byte("topics:\n  job.default: default\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	topics, err := LoadPoolTopics(path)
	if err != nil {
		t.Fatalf("LoadPoolTopics returned error: %v", err)
	}
	if topics["job.default"] != "default" {
		t.Fatalf("unexpected topics: %#v", topics)
	}
}
