package agentd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestFileStateStorePersistsOnlyNonSecretStateWithRestrictedPermissions(t *testing.T) {
	t.Parallel()

	const apiKey = "super-secret-api-key-1234"
	const rawHookPayload = "Bearer sk-test-secret"
	store, err := NewFileStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStateStore: %v", err)
	}
	state := SessionState{
		SessionID:        "sess-1",
		ExecutionID:      "exec-1",
		TraceID:          "trace-1",
		TenantID:         "tenant-a",
		PrincipalID:      "principal-a",
		PolicySnapshot:   "snap-1",
		DashboardURL:     "/edge/sessions/sess-1",
		PolicyMode:       edgecore.PolicyModeObserve,
		Status:           edgecore.SessionStatusRunning,
		SocketPath:       filepath.Join(t.TempDir(), "agentd.sock"),
		StartedAt:        time.Date(2026, 5, 2, 7, 40, 0, 0, time.UTC),
		TransientSecrets: map[string]string{"api_key": apiKey, "raw_payload": rawHookPayload},
	}

	if err := store.Save(context.Background(), state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := store.StatePath(state.SessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	text := string(data)
	if !strings.Contains(text, "sess-1") || !strings.Contains(text, "exec-1") {
		t.Fatalf("state file missing persisted ids: %s", text)
	}
	if strings.Contains(text, apiKey) || strings.Contains(text, rawHookPayload) {
		t.Fatalf("state file leaked transient secret: %s", text)
	}
	if runtime.GOOS != "windows" {
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
}

func TestFileStateStoreDropsSecretLikeMetadataKeys(t *testing.T) {
	t.Parallel()

	const apiKey = "super-secret-api-key-1234"
	const hookNonce = "f00ddeadbeefcafe0123456789abcdef"
	const providerToken = "provider-token-5678"
	store, err := NewFileStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStateStore: %v", err)
	}
	state := SessionState{
		SessionID:    "sess-secret-metadata",
		ExecutionID:  "exec-secret-metadata",
		TenantID:     "tenant-a",
		PolicyMode:   edgecore.PolicyModeObserve,
		Status:       edgecore.SessionStatusRunning,
		StartedAt:    time.Date(2026, 5, 2, 8, 20, 0, 0, time.UTC),
		DashboardURL: "/edge/sessions/sess-secret-metadata",
		Metadata: map[string]string{
			"cwd":                  "D:/Cordum/cordum",
			"cordum_api_key":       apiKey,
			"agentd_hook_nonce":    hookNonce,
			"model_provider_token": providerToken,
			"raw_hook_payload":     `{"authorization":"Bearer ` + providerToken + `"}`,
		},
	}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(store.StatePath(state.SessionID))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if strings.Contains(text, apiKey) || strings.Contains(text, hookNonce) || strings.Contains(text, providerToken) || strings.Contains(text, "raw_hook_payload") {
		t.Fatalf("state persisted secret-like metadata: %s", text)
	}
	loaded, ok, err := store.Load(context.Background(), state.SessionID)
	if err != nil || !ok {
		t.Fatalf("Load = ok:%v err:%v", ok, err)
	}
	if loaded.Metadata["cwd"] != "D:/Cordum/cordum" {
		t.Fatalf("safe metadata missing after load: %#v", loaded.Metadata)
	}
	if _, ok := loaded.Metadata["cordum_api_key"]; ok {
		t.Fatalf("secret metadata key survived load: %#v", loaded.Metadata)
	}
	if _, ok := loaded.Metadata["agentd_hook_nonce"]; ok {
		t.Fatalf("nonce metadata key survived load: %#v", loaded.Metadata)
	}
}
