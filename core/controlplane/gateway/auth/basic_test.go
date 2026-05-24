package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeKeyStore struct {
	validateFn func(ctx context.Context, rawKey string) (*ManagedKey, error)
	recordFn   func(ctx context.Context, id string) error
}

func (f *fakeKeyStore) List(context.Context, string) ([]*ManagedKey, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeKeyStore) Create(context.Context, *ManagedKey, string) error {
	return errors.New("not implemented")
}

func (f *fakeKeyStore) Revoke(context.Context, string, string) error {
	return errors.New("not implemented")
}

func (f *fakeKeyStore) ValidateKey(ctx context.Context, rawKey string) (*ManagedKey, error) {
	if f.validateFn == nil {
		return nil, errors.New("not implemented")
	}
	return f.validateFn(ctx, rawKey)
}

func (f *fakeKeyStore) RecordUsage(ctx context.Context, id string) error {
	if f.recordFn == nil {
		return errors.New("not implemented")
	}
	return f.recordFn(ctx, id)
}

func TestUsageRecordingDrain(t *testing.T) {
	const calls = 3
	startCh := make(chan struct{}, calls)
	releaseCh := make(chan struct{})
	var recorded int32

	ks := &fakeKeyStore{
		validateFn: func(context.Context, string) (*ManagedKey, error) {
			return &ManagedKey{ID: "managed-key", Tenant: "default"}, nil
		},
		recordFn: func(ctx context.Context, id string) error {
			startCh <- struct{}{}
			defer atomic.AddInt32(&recorded, 1)
			select {
			case <-releaseCh:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}

	b := &BasicAuthProvider{
		defaultTenant: "default",
		keyStore:      ks,
	}

	for i := 0; i < calls; i++ {
		if _, err := b.authenticate(context.Background(), "managed-key", ""); err != nil {
			t.Fatalf("authenticate() error = %v", err)
		}
	}

	for i := 0; i < calls; i++ {
		select {
		case <-startCh:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for RecordUsage start (%d/%d)", i+1, calls)
		}
	}

	done := make(chan struct{})
	go func() {
		b.DrainUsage()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("DrainUsage returned before usage goroutines completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DrainUsage timed out waiting for usage goroutines")
	}

	if got := atomic.LoadInt32(&recorded); got != calls {
		t.Fatalf("recorded usage count = %d, want %d", got, calls)
	}
}

func TestCompositeAuthProvider_BasicProvider(t *testing.T) {
	bp := &BasicAuthProvider{defaultTenant: "test"}
	cp, err := NewCompositeAuthProvider(bp)
	if err != nil {
		t.Fatalf("NewCompositeAuthProvider: %v", err)
	}

	got := cp.BasicProvider()
	if got != bp {
		t.Fatal("expected BasicProvider to return the wrapped BasicAuthProvider")
	}

	us := cp.UserStore()
	// No user store set — should return nil without panic.
	if us != nil {
		t.Fatal("expected nil UserStore when none configured")
	}
}

func TestCompositeAuthProvider_BasicProvider_NotPresent(t *testing.T) {
	// CompositeAuthProvider with only an OIDC adapter (no BasicAuthProvider).
	adapter := &OIDCAuthAdapter{}
	cp, err := NewCompositeAuthProvider(adapter)
	if err != nil {
		t.Fatalf("NewCompositeAuthProvider: %v", err)
	}

	got := cp.BasicProvider()
	if got != nil {
		t.Fatal("expected nil when no BasicAuthProvider in composite")
	}

	us := cp.UserStore()
	if us != nil {
		t.Fatal("expected nil UserStore when no BasicAuthProvider")
	}
}

func TestRoleFromScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   string
	}{
		{"no scopes", nil, "viewer"},
		{"empty scopes", []string{}, "viewer"},
		{"read scope", []string{"read"}, "viewer"},
		{"viewer scope", []string{"viewer"}, "viewer"},
		{"write scope", []string{"write"}, "operator"},
		{"operator scope", []string{"operator"}, "operator"},
		{"admin scope", []string{"admin"}, "admin"},
		{"multiple read+write", []string{"read", "write"}, "operator"},
		{"multiple read+admin", []string{"read", "admin"}, "admin"},
		{"multiple write+admin", []string{"write", "admin"}, "admin"},
		{"all scopes", []string{"read", "write", "admin"}, "admin"},
		{"case insensitive", []string{"ADMIN"}, "admin"},
		{"resource read scope", []string{"jobs:read"}, "viewer"},
		{"resource write scope", []string{"jobs:write"}, "operator"},
		{"resource wildcard scope", []string{"jobs:*"}, "operator"},
		{"resource read plus write", []string{"jobs:read", "audit:write"}, "operator"},
		{"unknown scope", []string{"custom"}, "viewer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roleFromScopes(tt.scopes)
			if got != tt.want {
				t.Fatalf("roleFromScopes(%v) = %q, want %q", tt.scopes, got, tt.want)
			}
		})
	}
}

func TestManagedKeyScope_ReflectedInAuthContext(t *testing.T) {
	tests := []struct {
		name     string
		scopes   []string
		wantRole string
	}{
		{"read key", []string{"read"}, "viewer"},
		{"write key", []string{"write"}, "operator"},
		{"admin key", []string{"admin"}, "admin"},
		{"no scopes key", nil, "viewer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks := &fakeKeyStore{
				validateFn: func(context.Context, string) (*ManagedKey, error) {
					return &ManagedKey{ID: "key-1", Tenant: "default", Scopes: tt.scopes}, nil
				},
				recordFn: func(context.Context, string) error { return nil },
			}
			b := &BasicAuthProvider{
				defaultTenant: "default",
				keyStore:      ks,
			}
			authCtx, err := b.authenticate(context.Background(), "test-key", "")
			if err != nil {
				t.Fatalf("authenticate() error = %v", err)
			}
			b.DrainUsage()
			if authCtx.Role != tt.wantRole {
				t.Fatalf("role = %q, want %q", authCtx.Role, tt.wantRole)
			}
		})
	}
}

func TestUsageRecordingErrorLogged(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	ks := &fakeKeyStore{
		validateFn: func(context.Context, string) (*ManagedKey, error) {
			return &ManagedKey{ID: "managed-key", Tenant: "default"}, nil
		},
		recordFn: func(context.Context, string) error {
			return errors.New("record failed")
		},
	}

	b := &BasicAuthProvider{
		defaultTenant: "default",
		keyStore:      ks,
	}

	if _, err := b.authenticate(context.Background(), "managed-key", ""); err != nil {
		t.Fatalf("authenticate() error = %v", err)
	}

	b.DrainUsage()

	logOutput := buf.String()
	if !strings.Contains(logOutput, "failed to record api key usage") {
		t.Fatalf("expected warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "key_id=managed-key") {
		t.Fatalf("expected key_id in log, got %q", logOutput)
	}
}

func TestNewBasicAuthProviderLogsAPIKeySource(t *testing.T) {
	for _, key := range []string{
		"CORDUM_API_KEYS",
		"CORDUM_API_KEY",
		"CORDUM_API_KEYS_PATH",
		"CORDUM_API_KEY_SOURCE",
		"CORDUM_API_KEY_SOURCE_FILE",
		"CORDUM_ALLOW_INSECURE_NO_AUTH",
		"CORDUM_ENV",
		"CORDUM_PRODUCTION",
		"CORDUM_JWT_HMAC_SECRET",
		"CORDUM_JWT_PUBLIC_KEY",
		"CORDUM_JWT_PUBLIC_KEY_PATH",
		"CORDUM_JWT_REQUIRED",
		"CORDUM_JWT_ISSUER",
		"CORDUM_JWT_AUDIENCE",
	} {
		t.Setenv(key, "")
	}
	const apiKey = "test-key-source-log-000000000000000000000000000000"
	t.Setenv("CORDUM_API_KEY", apiKey)
	t.Setenv("CORDUM_API_KEY_SOURCE_FILE", ".env")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	provider, err := NewBasicAuthProvider("default")
	if err != nil {
		t.Fatalf("NewBasicAuthProvider() error = %v", err)
	}
	if provider == nil {
		t.Fatalf("NewBasicAuthProvider() returned nil provider")
	}

	logs := buf.String()
	for _, want := range []string{
		"msg=auth.api_key.source",
		"source=env",
		"source_file=.env",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected log to contain %q, got:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, apiKey) {
		t.Fatalf("api key value leaked in logs: %s", logs)
	}
}

func TestAPIKeyFingerprint(t *testing.T) {
	const raw = "super-secret-raw-key-value-0000000000000000000000"
	fp := APIKeyFingerprint(raw)

	if len(fp) != 12 {
		t.Fatalf("fingerprint length = %d, want 12 (%q)", len(fp), fp)
	}
	for _, c := range fp {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("fingerprint %q contains non lowercase-hex rune %q", fp, c)
		}
	}
	// Must be the 12-char prefix of the full SHA-256 hex digest — same hasher,
	// single source of truth, never the raw key.
	if want := hashAPIKey(raw)[:12]; fp != want {
		t.Fatalf("fingerprint = %q, want %q (hashAPIKey prefix)", fp, want)
	}
	if fp == raw {
		t.Fatalf("fingerprint must not equal the raw key")
	}
	if again := APIKeyFingerprint(raw); again != fp {
		t.Fatalf("fingerprint not deterministic: %q != %q", again, fp)
	}
	if other := APIKeyFingerprint("a-completely-different-key-value-11111111111111111"); other == fp {
		t.Fatalf("distinct keys produced identical fingerprint %q", fp)
	}
}

func TestKeyIDAndName_PopulatedInAuthContext(t *testing.T) {
	t.Run("managed key uses id and name", func(t *testing.T) {
		ks := &fakeKeyStore{
			validateFn: func(context.Context, string) (*ManagedKey, error) {
				return &ManagedKey{ID: "mk_abc123", Name: "ci-deploy", Tenant: "default", Scopes: []string{"admin"}}, nil
			},
			recordFn: func(context.Context, string) error { return nil },
		}
		b := &BasicAuthProvider{defaultTenant: "default", keyStore: ks}

		ac, err := b.authenticate(context.Background(), "managed-raw-secret", "")
		if err != nil {
			t.Fatalf("authenticate() error = %v", err)
		}
		b.DrainUsage()

		if ac.KeyID != "mk_abc123" {
			t.Fatalf("KeyID = %q, want %q", ac.KeyID, "mk_abc123")
		}
		if ac.KeyName != "ci-deploy" {
			t.Fatalf("KeyName = %q, want %q", ac.KeyName, "ci-deploy")
		}
	})

	t.Run("static key uses static fingerprint and empty name", func(t *testing.T) {
		const raw = "static-raw-secret-value-2222222222222222222222222"
		b := &BasicAuthProvider{
			defaultTenant: "default",
			keyHashes: buildKeyHashes(map[string]apiKeyMeta{
				raw: {Tenant: "default", Role: "admin"},
			}),
		}

		ac, err := b.authenticate(context.Background(), raw, "")
		if err != nil {
			t.Fatalf("authenticate() error = %v", err)
		}

		const wantPrefix = "static:"
		if !strings.HasPrefix(ac.KeyID, wantPrefix) {
			t.Fatalf("KeyID = %q, want prefix %q", ac.KeyID, wantPrefix)
		}
		gotFP := strings.TrimPrefix(ac.KeyID, wantPrefix)
		if gotFP != APIKeyFingerprint(raw) {
			t.Fatalf("static KeyID fingerprint = %q, want %q", gotFP, APIKeyFingerprint(raw))
		}
		if ac.KeyID == raw || strings.Contains(ac.KeyID, raw) {
			t.Fatalf("static KeyID %q must never contain the raw key", ac.KeyID)
		}
		if ac.KeyName != "" {
			t.Fatalf("static KeyName = %q, want empty (apiKeyMeta carries no name field)", ac.KeyName)
		}
	})
}
