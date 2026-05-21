package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/keychain"
)

// blockingKeyring is a Keyring whose Get blocks until ctx is cancelled. It
// proves the bootstrap secret-load path actually honors caller-side
// cancellation/timeout rather than swallowing the caller ctx and falling
// back to context.Background() inside the keychain package.
type blockingKeyring struct {
	started chan struct{}
}

func newBlockingKeyring() *blockingKeyring { return &blockingKeyring{started: make(chan struct{}, 1)} }

func (b *blockingKeyring) Get(ctx context.Context, _ string) (string, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func (b *blockingKeyring) Set(ctx context.Context, _ string, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (b *blockingKeyring) Delete(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestBootstrapReadsFromKeyringFirst(t *testing.T) {
	t.Parallel()

	kr := keychain.NewMockKeyring()
	if err := kr.Set(context.Background(), "cordum_agentd_nonce", syntheticNonceFromKeyring()); err != nil {
		t.Fatalf("Set nonce: %v", err)
	}
	if err := kr.Set(context.Background(), "cordum_api_key", "synthetic-keychain-api-key"); err != nil {
		t.Fatalf("Set api_key: %v", err)
	}

	env := map[string]string{
		"CORDUM_GATEWAY":   "http://127.0.0.1:8081",
		"CORDUM_TENANT_ID": "tenant-a",
		// CORDUM_AGENTD_NONCE + CORDUM_API_KEY intentionally absent — must come
		// from the keychain in this test, not from env.
	}
	resolved, err := loadBootstrapSecrets(context.Background(), kr, keychain.ModeStrict, env, bytes.NewBuffer(nil))
	if err != nil {
		t.Fatalf("loadBootstrapSecrets: %v", err)
	}
	if resolved["CORDUM_AGENTD_NONCE"] != syntheticNonceFromKeyring() {
		t.Fatalf("nonce not sourced from keychain: %q", resolved["CORDUM_AGENTD_NONCE"])
	}
	if resolved["CORDUM_API_KEY"] != "synthetic-keychain-api-key" {
		t.Fatalf("api_key not sourced from keychain: %q", resolved["CORDUM_API_KEY"])
	}
}

func TestBootstrapStrictModeRefusesEnvFallback(t *testing.T) {
	t.Parallel()

	kr := keychain.NewMockKeyring()
	kr.SetFailure(keychain.ErrKeyringUnavailable)

	env := map[string]string{
		"CORDUM_GATEWAY":      "http://127.0.0.1:8081",
		"CORDUM_TENANT_ID":    "tenant-a",
		"CORDUM_AGENTD_NONCE": "WOULD-LEAK-IF-USED",
		"CORDUM_API_KEY":      "WOULD-LEAK-IF-USED",
	}
	var stderr bytes.Buffer
	_, err := loadBootstrapSecrets(context.Background(), kr, keychain.ModeStrict, env, &stderr)
	if err == nil {
		t.Fatalf("strict mode accepted env fallback when keychain unavailable")
	}
	if !strings.Contains(err.Error(), "BOOTSTRAP-FAIL") {
		t.Fatalf("expected BOOTSTRAP-FAIL in error, got %q", err.Error())
	}
	if strings.Contains(stderr.String(), "WOULD-LEAK-IF-USED") {
		t.Fatalf("stderr echoed env secret bytes: %s", stderr.String())
	}
	if strings.Contains(err.Error(), "WOULD-LEAK-IF-USED") {
		t.Fatalf("error echoed env secret bytes: %v", err)
	}
}

func TestBootstrapDevModeFallsBackToEnvWhenKeychainEmpty(t *testing.T) {
	t.Parallel()

	kr := keychain.NewMockKeyring()
	env := map[string]string{
		"CORDUM_GATEWAY":      "http://127.0.0.1:8081",
		"CORDUM_TENANT_ID":    "tenant-a",
		"CORDUM_AGENTD_NONCE": "env-fallback-nonce",
		"CORDUM_API_KEY":      "env-fallback-api-key",
	}
	var stderr bytes.Buffer
	resolved, err := loadBootstrapSecrets(context.Background(), kr, keychain.ModeDev, env, &stderr)
	if err != nil {
		t.Fatalf("dev fallback: %v", err)
	}
	if resolved["CORDUM_AGENTD_NONCE"] != "env-fallback-nonce" {
		t.Fatalf("nonce not env-fallback in dev: %q", resolved["CORDUM_AGENTD_NONCE"])
	}
	if resolved["CORDUM_API_KEY"] != "env-fallback-api-key" {
		t.Fatalf("api_key not env-fallback in dev: %q", resolved["CORDUM_API_KEY"])
	}
}

// TestLoadBootstrapSecretsHonorsContextCancellation is the PR #276 Sub-G #24
// regression. It pins the contract that a CANCELLED caller-side context
// short-circuits the keychain.Get blocking call — i.e. cancellation actually
// propagates through loadBootstrapSecrets → keychain.LoadSecret → kr.Get
// instead of being swallowed by a context.Background() inside the bootstrap
// flow. The test fails LOUDLY (timeout-detected hang) if a future regression
// re-introduces context.Background() in any link of the chain.
func TestLoadBootstrapSecretsHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	kr := newBlockingKeyring()
	env := map[string]string{
		"CORDUM_GATEWAY":   "http://127.0.0.1:8081",
		"CORDUM_TENANT_ID": "tenant-a",
		// No env fallback for CORDUM_AGENTD_NONCE / CORDUM_API_KEY → strict
		// mode must wait on the keychain and surface cancellation rather
		// than fall back silently.
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := loadBootstrapSecrets(ctx, kr, keychain.ModeStrict, env, bytes.NewBuffer(nil))
		done <- err
	}()
	// Wait until the keyring Get actually starts blocking, so the cancel
	// is observed mid-call rather than racing against a not-yet-started
	// goroutine and accidentally succeeding via short-circuit.
	select {
	case <-kr.started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatalf("blockingKeyring.Get never started — bootstrap path may have skipped the keychain")
	}
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("loadBootstrapSecrets returned nil after ctx cancel — cancellation was swallowed")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want errors.Is(err, context.Canceled)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("loadBootstrapSecrets hung > 2s after cancel — context.Background() likely swallows the caller ctx")
	}
}

// TestLoadBootstrapSecretsRedactsBackendError is the PR #276 Sub-G #21 + #24
// joint regression. The BOOTSTRAP-FAIL diagnostic envelope must (a) still
// match the errors.Is sentinel so service-manager log analyzers can dispatch
// on category, and (b) NEVER carry raw backend bytes — even when the
// underlying Keyring impl embedded a secret-shaped substring in its error
// message. Without this rail a third-party Keyring backend could echo a
// credential into journald via fmt.Errorf("%w", err).
func TestLoadBootstrapSecretsRedactsBackendError(t *testing.T) {
	t.Parallel()

	const syntheticBackend = "raw-backend-bytes-SENTINEL-LEAK-3f9b2c11e4a87de9"
	kr := keychain.NewMockKeyring()
	kr.SetFailure(fmt.Errorf("%w: %s", keychain.ErrKeyringUnavailable, syntheticBackend))

	env := map[string]string{
		"CORDUM_GATEWAY":   "http://127.0.0.1:8081",
		"CORDUM_TENANT_ID": "tenant-a",
	}
	var stderr bytes.Buffer
	_, err := loadBootstrapSecrets(context.Background(), kr, keychain.ModeStrict, env, &stderr)
	if err == nil {
		t.Fatalf("expected BOOTSTRAP-FAIL error, got nil")
	}
	if !strings.Contains(err.Error(), "BOOTSTRAP-FAIL") {
		t.Fatalf("error missing BOOTSTRAP-FAIL sentinel marker: %q", err.Error())
	}
	if !errors.Is(err, keychain.ErrKeyringUnavailable) {
		t.Fatalf("errors.Is(err, ErrKeyringUnavailable) = false; chain unwrap broke")
	}
	if strings.Contains(err.Error(), syntheticBackend) {
		t.Fatalf("error.Error() leaked raw backend bytes %q; got %q", syntheticBackend, err.Error())
	}
	if strings.Contains(stderr.String(), syntheticBackend) {
		t.Fatalf("stderr leaked raw backend bytes %q; got %q", syntheticBackend, stderr.String())
	}
}

// syntheticNonceFromKeyring returns a synthetic base64-encoded nonce that the
// agentd nonce validator (>=32 random bytes, base64) accepts. The exact value
// is irrelevant to bootstrap loading — it only matters that the test value is
// not derived from production secrets and is uniquely identifiable in test
// log assertions.
func syntheticNonceFromKeyring() string {
	return "c3ludGhldGljLW5vbmNlLWZyb20ta2V5Y2hhaW4tdGVzdC0xMjM0NTY3OA=="
}
