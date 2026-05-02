//go:build !windows

package agentd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestFileStateStoreUnixPermissionsUnchanged(t *testing.T) {
	t.Parallel()

	store, err := NewFileStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStateStore: %v", err)
	}
	state := SessionState{
		SessionID:  "sess-unix-perms",
		TenantID:   "tenant-a",
		PolicyMode: edgecore.PolicyModeObserve,
		Status:     edgecore.SessionStatusRunning,
		StartedAt:  time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC),
	}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := store.StatePath(state.SessionID)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("state file perm = %o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("state dir perm = %o, want 0700", got)
	}
}
