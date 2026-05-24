package claude

import (
	"net"
	"net/url"
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

// TestLoopbackReservationNoTOCTOU asserts the per-platform reservation contract.
// On inheritance platforms (Unix) reserveLoopbackHookURL holds the reserved
// listener open until it is handed to agentd across exec, so the port must NOT be
// re-bindable — the no-TOCTOU guarantee. On non-inheritance platforms (Windows,
// where supportsAgentdListenerInheritance() is false; see
// listener_inheritance_windows.go) the wrapper deliberately uses the close-then-
// bind legacy path: the port is released so agentd can re-bind it fresh, which is
// the accepted Option-A reserve->bind race tradeoff.
func TestLoopbackReservationNoTOCTOU(t *testing.T) {
	rawURL, err := reserveLoopbackHookURL()
	if err != nil {
		t.Fatalf("reserveLoopbackHookURL: %v", err)
	}
	t.Cleanup(func() { releaseReservedLoopbackHookURL(rawURL) })
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse reserved URL: %v", err)
	}

	attacker, attackErr := net.Listen("tcp", u.Host)
	if supportsAgentdListenerInheritance() {
		if attackErr == nil {
			_ = attacker.Close()
			t.Fatalf("reserved loopback port %s was bindable after reservation; launcher has a TOCTOU window", u.Host)
		}
		return
	}
	// Legacy close-then-bind path: the reserved port is expected to be free so
	// agentd can re-bind it. A failure here means the freed port is not
	// re-bindable, which would break the Windows fix's core assumption.
	if attackErr != nil {
		t.Fatalf("legacy reservation should release %s for agentd to re-bind, but it was not bindable: %v", u.Host, attackErr)
	}
	_ = attacker.Close()
}

func TestRejectSettingsOverrideBlocksAllSettingsVariants(t *testing.T) {
	for _, args := range [][]string{
		{"--settings", "/tmp/settings.json"},
		{"--settings=/tmp/settings.json"},
		{"--settings-path", "/tmp/settings.json"},
		{"--settings-path=/tmp/settings.json"},
		{"--settings-file", "/tmp/settings.json"},
		{"--settings-file=/tmp/settings.json"},
	} {
		t.Run(args[0], func(t *testing.T) {
			if err := rejectSettingsOverride(args); err == nil {
				t.Fatalf("rejectSettingsOverride(%v) returned nil, want settings override rejection", args)
			}
		})
	}
}
