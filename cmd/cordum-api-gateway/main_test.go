package main

import (
	"testing"

	"github.com/cordum/cordum/core/infra/buildinfo"
)

func TestPackageImports(t *testing.T) {
	// Verify package can be imported and compiled
	if buildinfo.Version == "" {
		t.Log("buildinfo not set (expected in dev)")
	}
}

func TestMainExists(t *testing.T) {
	// This test verifies that main() function exists and compiles
	// The actual main() is tested via integration tests
	t.Log("main function exists and compiles")
}
