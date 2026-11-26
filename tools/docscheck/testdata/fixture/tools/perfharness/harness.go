// Package perfharness is a docscheck fixture for a test-harness tree. It
// lives under testdata, so the go tool never builds it; docscheck only
// parses it.
package perfharness

import "os"

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

var (
	_ = envOr("FIX_VAR", "b")
	_ = envOr("FIX_PLAIN", "z")
)
