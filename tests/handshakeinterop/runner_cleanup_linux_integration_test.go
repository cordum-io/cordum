//go:build handshakeinterop && linux

package handshakeinterop

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOwnedTempCleanupRemovesReadOnlyModuleCache(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "cap-handshake-interop.readonly")
	locked := filepath.Join(root, "go-consumer", ".gomodcache", "module@v1")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("create locked cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(locked, "module.go"), []byte("package module\n"), 0o444); err != nil {
		t.Fatalf("write locked cache file: %v", err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("lock module cache directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o755)
		_ = os.RemoveAll(root)
	})

	if output, err := runOwnedTempCleanup(root, base); err != nil {
		t.Fatalf("cleanup read-only module cache: %v\n%s", err, output)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("owned temp root still exists after cleanup: %v", err)
	}
}

func TestOwnedTempCleanupRefusesOutsideBase(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "cap-handshake-interop.outside")
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside root: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if output, err := runOwnedTempCleanup(outside, base); err == nil {
		t.Fatalf("cleanup accepted path outside base\n%s", output)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("outside sentinel was changed: %v", err)
	}
}

func TestOwnedTempCleanupRefusesSymlinkedModuleCache(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "cap-handshake-interop.symlink")
	consumer := filepath.Join(root, "go-consumer")
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.MkdirAll(consumer, 0o755); err != nil {
		t.Fatalf("create consumer root: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside sentinel: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(consumer, ".gomodcache")); err != nil {
		t.Fatalf("symlink module cache: %v", err)
	}

	if output, err := runOwnedTempCleanup(root, base); err == nil {
		t.Fatalf("cleanup accepted symlinked module cache\n%s", output)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("outside sentinel changed: data=%q err=%v", data, err)
	}
}

func runOwnedTempCleanup(root, base string) ([]byte, error) {
	helper, err := filepath.Abs("cleanup.sh")
	if err != nil {
		return nil, err
	}
	command := `. "$1"; remove_owned_temp "$2" "$3"`
	return exec.Command("sh", "-c", command, "cleanup-test", helper, root, base).CombinedOutput()
}
