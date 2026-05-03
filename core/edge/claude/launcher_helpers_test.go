package claude

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// EDGE-045: resolveExecutable must accept Windows hook-command paths that omit
// the `.exe` extension. Pre-fix, `os.Stat(".\\bin\\cordum-hook")` returned
// ErrNotExist on Windows and the launcher errored out before settings.json
// could be rendered. Post-fix, the function tries the explicit path first
// then falls back to `<path>.exe` on `runtime.GOOS == "windows"`.
func TestResolveExecutableAppendsExeOnWindowsWhenMissing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific behavior; .exe fallback is a no-op on " + runtime.GOOS)
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "cordum-hook.exe")
	if err := os.WriteFile(exePath, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("seed cordum-hook.exe: %v", err)
	}

	// User-supplied path is missing `.exe`; resolveExecutable must find the
	// `.exe` companion on disk and return it.
	noExt := filepath.Join(dir, "cordum-hook")
	got, err := resolveExecutable(noExt, "cordum-hook")
	if err != nil {
		t.Fatalf("resolveExecutable(%q) returned error: %v", noExt, err)
	}
	if got != exePath {
		t.Fatalf("resolveExecutable(%q) = %q, want %q (auto-appended .exe)", noExt, got, exePath)
	}
}

// resolveExecutable must NOT double-append `.exe` when the path already ends
// in `.exe` (case-insensitive on Windows).
func TestResolveExecutablePreservesAlreadyExePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific behavior")
	}
	dir := t.TempDir()
	exePath := filepath.Join(dir, "cordum-hook.exe")
	if err := os.WriteFile(exePath, []byte("MZ"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := resolveExecutable(exePath, "cordum-hook")
	if err != nil {
		t.Fatalf("resolveExecutable(%q) returned error: %v", exePath, err)
	}
	if got != exePath {
		t.Fatalf("resolveExecutable(%q) = %q, want %q (no double-append)", exePath, got, exePath)
	}
	// Sanity: the function did NOT try to find `cordum-hook.exe.exe`.
	if _, err := os.Stat(exePath + ".exe"); err == nil {
		t.Fatalf("test seeded a stray %q; resolveExecutable should not depend on this", exePath+".exe")
	}
}

// resolveExecutable returns the explicit path verbatim on Linux/macOS even
// when no `.exe` exists — the .exe fallback is Windows-only.
func TestResolveExecutableLinuxMacOSReturnsExplicit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux/macOS-specific; Windows .exe fallback path is covered by the Windows tests")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "cordum-hook")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := resolveExecutable(binPath, "cordum-hook")
	if err != nil {
		t.Fatalf("resolveExecutable(%q) returned error: %v", binPath, err)
	}
	if got != binPath {
		t.Fatalf("resolveExecutable(%q) = %q, want %q", binPath, got, binPath)
	}
}

// filepathExt extracts the trailing extension verbatim (no path/filepath
// import dep). Smoke-test the corner cases that drive resolveExecutable's
// .exe gating decision.
func TestFilepathExt(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"cordum-hook.exe", ".exe"},
		{".\\bin\\cordum-hook.exe", ".exe"},
		{"./bin/cordum-hook", ""},
		{"cordum-hook", ""},
		{"D:\\Program Files\\app.bat", ".bat"},
		{"path/with.dot/in/dir/file", ""},
		{"", ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := filepathExt(tc.in); got != tc.want {
				t.Fatalf("filepathExt(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
