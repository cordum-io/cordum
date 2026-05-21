//go:build keychain

package keychain

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// TestKeyringRealOSRoundtrip exercises the host OS keychain (macOS Security
// framework / Linux libsecret / Windows Credential Manager) under the
// `keychain` build tag. CI without the platform backend should not depend on
// this passing — invoke via `go test -tags=keychain` and skip via t.Skip when
// the keyring backend is not reachable.
func TestKeyringRealOSRoundtrip(t *testing.T) {
	kr := NewOSKeyring()
	ctx := context.Background()
	key := fmt.Sprintf("cordum-test-%s", uuid.NewString())
	value := "synthetic-test-only-secret-do-not-promote"
	t.Cleanup(func() {
		_ = kr.Delete(ctx, key)
	})

	if err := kr.Set(ctx, key, value); err != nil {
		if errors.Is(err, ErrKeyringUnavailable) {
			t.Skipf("OS keychain unavailable: %v", err)
		}
		t.Fatalf("Set: %v", err)
	}
	got, err := kr.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != value {
		t.Fatalf("OS keychain roundtrip mismatch: got=%q want=%q", got, value)
	}
	if err := kr.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := kr.Get(ctx, key); !errors.Is(err, ErrKeyringNotFound) {
		t.Fatalf("post-delete Get: err=%v, want ErrKeyringNotFound", err)
	}
}
