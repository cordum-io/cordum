package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPoolTopicsSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pools.yaml")
	body := []byte("topics:\n  job.echo: echo\n  job.chat.simple: chat-simple\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	topics, err := LoadPoolTopics(path)
	if err != nil {
		t.Fatalf("LoadPoolTopics returned error: %v", err)
	}
	if len(topics) != 2 || topics["job.echo"] != "echo" || topics["job.chat.simple"] != "chat-simple" {
		t.Fatalf("unexpected topics: %#v", topics)
	}
}

func TestLoadPoolTopicsErrors(t *testing.T) {
	if _, err := LoadPoolTopics("nope.yaml"); err == nil {
		t.Fatalf("expected error for missing file")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("topics: {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadPoolTopics(path); err == nil {
		t.Fatalf("expected error for empty topics")
	}
}
