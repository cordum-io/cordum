package main

import (
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/packs"
)

func TestPackManifestParses(t *testing.T) {
	manifest, err := packs.LoadPackManifest("pack")
	if err != nil {
		t.Fatalf("LoadPackManifest(%q): %v", "pack", err)
	}
	if manifest.Metadata.ID != "hello-pack" {
		t.Fatalf("metadata.id = %q, want hello-pack", manifest.Metadata.ID)
	}
	if manifest.Metadata.Version == "" {
		t.Fatal("metadata.version is empty")
	}
	if got := len(manifest.Topics); got != 1 {
		t.Fatalf("expected exactly one topic, got %d", got)
	}
	topic := manifest.Topics[0]
	if topic.Name != "job.hello-pack.echo" {
		t.Fatalf("topic name = %q, want job.hello-pack.echo", topic.Name)
	}
	if topic.Capability != "hello-pack.echo" {
		t.Fatalf("topic capability = %q, want hello-pack.echo", topic.Capability)
	}
	if err := packs.ValidatePackManifest(manifest); err != nil {
		t.Fatalf("ValidatePackManifest: %v", err)
	}
}
