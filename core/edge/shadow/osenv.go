package shadow

import (
	"io"
	"os"
)

// getEnv wraps os.Getenv so the rest of the package can stay free of
// direct os imports. Read-only env access is fine; the scanner package
// is forbidden from mutating the filesystem or spawning subprocesses
// (task rail #2 — see scanner_test for the static-source guard).
func getEnv(key string) string { return os.Getenv(key) }

// discardWriter is a minimal io.Writer used as the default slog sink so
// the scanner never spams stderr unless the caller installs a real logger.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

var _ io.Writer = discardWriter{}
