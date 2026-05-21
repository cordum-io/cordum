package keychain

import (
	"context"
	"sync"
)

// MockKeyring is an in-memory Keyring implementation for unit tests. It is
// goroutine-safe and supports a programmable failure mode for exercising
// strict bootstrap error paths without touching the host keychain.
type MockKeyring struct {
	mu    sync.Mutex
	store map[string]string
	fail  error
}

// NewMockKeyring returns an empty in-memory Keyring suitable for tests.
// Use the returned *MockKeyring directly to call SetFailure when a test
// needs to simulate ErrKeyringUnavailable or ErrKeyringPermissionDenied.
func NewMockKeyring() *MockKeyring {
	return &MockKeyring{store: make(map[string]string)}
}

// SetFailure programs the mock to return err from every operation. Pass
// nil to clear. Tests use this to simulate an unreachable backend without
// monkey-patching package globals.
func (m *MockKeyring) SetFailure(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = err
}

func (m *MockKeyring) Get(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return "", m.fail
	}
	if key == "" {
		return "", ErrKeyringNotFound
	}
	value, ok := m.store[key]
	if !ok {
		return "", ErrKeyringNotFound
	}
	return value, nil
}

func (m *MockKeyring) Set(ctx context.Context, key, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	if key == "" {
		return ErrKeyringNotFound
	}
	m.store[key] = value
	return nil
}

func (m *MockKeyring) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	if key == "" {
		return ErrKeyringNotFound
	}
	delete(m.store, key)
	return nil
}
