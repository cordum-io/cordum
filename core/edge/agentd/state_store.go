package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type StateStore interface {
	Save(context.Context, SessionState) error
	Load(context.Context, string) (SessionState, bool, error)
}

type MemoryStateStore struct {
	mu     sync.Mutex
	states map[string]SessionState
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{states: make(map[string]SessionState)}
}

func (s *MemoryStateStore) Save(_ context.Context, state SessionState) error {
	if s == nil {
		return errors.New("memory state store is nil")
	}
	if strings.TrimSpace(state.SessionID) == "" {
		return errors.New("session_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state = scrubStateForPersistence(state)
	s.states[state.SessionID] = state
	return nil
}

func (s *MemoryStateStore) Load(_ context.Context, sessionID string) (SessionState, bool, error) {
	if s == nil {
		return SessionState{}, false, errors.New("memory state store is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[sessionID]
	return state, ok, nil
}

type FileStateStore struct {
	root        string
	strictPerms bool
}

func NewFileStateStore(root string) (*FileStateStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("state dir is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create state root: %w", err)
	}
	_ = os.Chmod(root, 0o700) // #nosec G302 -- directory needs the owner execute bit to be traversable; 0700 is owner-only
	store := &FileStateStore{root: root, strictPerms: stateStoreStrictPermsEnabled()}
	if err := store.verifyRootPermissions(); err != nil {
		return nil, err
	}
	if err := store.hardenPath(root); err != nil {
		return nil, err
	}
	if err := store.verifyRootPermissions(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStateStore) StatePath(sessionID string) string {
	return filepath.Join(s.root, safePathSegment(sessionID), "state.json")
}

func (s *FileStateStore) Save(_ context.Context, state SessionState) error {
	if s == nil {
		return errors.New("file state store is nil")
	}
	if strings.TrimSpace(state.SessionID) == "" {
		return errors.New("session_id is required")
	}
	state = scrubStateForPersistence(state)
	dir := filepath.Dir(s.StatePath(state.SessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create session state dir: %w", err)
	}
	_ = os.Chmod(dir, 0o700) // #nosec G302 -- directory needs the owner execute bit to be traversable; 0700 is owner-only
	if err := s.hardenPath(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write state file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state file: %w", err)
	}
	if err := os.Rename(tmpName, s.StatePath(state.SessionID)); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}
	_ = os.Chmod(s.StatePath(state.SessionID), 0o600)
	if err := s.hardenPath(s.StatePath(state.SessionID)); err != nil {
		return err
	}
	return nil
}

func (s *FileStateStore) hardenPath(path string) error {
	if err := applyStatePathPermissions(path); err != nil {
		return handleStatePermissionError(s.strictPerms, path, err)
	}
	return nil
}

func (s *FileStateStore) verifyRootPermissions() error {
	if err := verifyStateDirPermissions(s.root); err != nil {
		return handleStatePermissionError(s.strictPerms, s.root, err)
	}
	return nil
}

func stateStoreStrictPermsEnabled() bool {
	return parseBool(os.Getenv("CORDUM_AGENTD_STRICT_PERMS"))
}

func handleStatePermissionError(strict bool, path string, err error) error {
	if strict {
		return fmt.Errorf("state permissions strict check failed for %s: %w", path, err)
	}
	warnStatePermissionOnce(path, err)
	return nil
}

var statePermissionsWarnOnce sync.Once

func warnStatePermissionOnce(path string, err error) {
	statePermissionsWarnOnce.Do(func() {
		slog.Warn(
			"agentd state permissions are broader than owner-only; continuing because CORDUM_AGENTD_STRICT_PERMS is not set",
			"path", path,
			"error", err,
		)
	})
}

func (s *FileStateStore) Load(_ context.Context, sessionID string) (SessionState, bool, error) {
	if s == nil {
		return SessionState{}, false, errors.New("file state store is nil")
	}
	data, err := os.ReadFile(s.StatePath(sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return SessionState{}, false, nil
	}
	if err != nil {
		return SessionState{}, false, fmt.Errorf("read state file: %w", err)
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return SessionState{}, false, fmt.Errorf("decode state file: %w", err)
	}
	return state, true, nil
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	return replacer.Replace(value)
}

func scrubStateForPersistence(state SessionState) SessionState {
	state.TransientSecrets = nil
	state.Metadata = sanitizeMetadata(state.Metadata)
	return state
}

func sanitizeMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if isSensitiveMetadataKey(key) {
			continue
		}
		out[key] = redactSecretLike(value)
	}
	return out
}

func isSensitiveMetadataKey(key string) bool {
	k := strings.ToLower(key)
	for _, marker := range []string{
		"password", "passwd", "secret", "token", "nonce", "api_key", "apikey", "credential", "auth", "raw_hook_payload", "raw_payload",
	} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}
