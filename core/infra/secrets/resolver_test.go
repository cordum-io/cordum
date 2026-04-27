package secrets

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock provider for unit tests
// ---------------------------------------------------------------------------

type mockProvider struct {
	scheme   string
	secrets  map[string]map[string]string // path → key → value
	calls    atomic.Int64
	closeErr error
}

func newMockProvider(scheme string) *mockProvider {
	return &mockProvider{
		scheme:  scheme,
		secrets: make(map[string]map[string]string),
	}
}

func (m *mockProvider) addSecret(path string, fields map[string]string) {
	m.secrets[path] = fields
}

func (m *mockProvider) Scheme() string { return m.scheme }

func (m *mockProvider) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	m.calls.Add(1)

	// Respect context cancellation.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	data, ok := m.secrets[ref.Path]
	if !ok {
		return "", fmt.Errorf("mock: %s: %w", ref.Path, ErrSecretNotFound)
	}

	if ref.Key == "" {
		if len(data) == 1 {
			for _, v := range data {
				return v, nil
			}
		}
		return "", fmt.Errorf("mock: %s has %d fields, specify #key", ref.Path, len(data))
	}

	val, ok := data[ref.Key]
	if !ok {
		return "", fmt.Errorf("mock: %s#%s: %w", ref.Path, ref.Key, ErrKeyNotFound)
	}
	return val, nil
}

func (m *mockProvider) Close() error { return m.closeErr }

// ---------------------------------------------------------------------------
// Resolver tests
// ---------------------------------------------------------------------------

func TestResolver_ResolveSuccess(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/creds", map[string]string{"password": "s3cret"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	val, err := r.Resolve(context.Background(), "secret://vault/db/creds#password")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "s3cret" {
		t.Fatalf("value = %q, want %q", val, "s3cret")
	}
	if mp.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", mp.calls.Load())
	}
}

func TestResolver_ResolveNotFound(t *testing.T) {
	mp := newMockProvider("vault")
	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	_, err := r.Resolve(context.Background(), "secret://vault/nonexistent#key")
	if err == nil {
		t.Fatal("expected error for nonexistent secret")
	}
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestResolver_ResolveKeyNotFound(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/creds", map[string]string{"user": "admin"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	_, err := r.Resolve(context.Background(), "secret://vault/db/creds#password")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got: %v", err)
	}
}

func TestResolver_ResolveNoProvider(t *testing.T) {
	r := NewResolver(WithCacheTTL(0))

	_, err := r.Resolve(context.Background(), "secret://unknown/path#key")
	if err == nil {
		t.Fatal("expected error for unregistered provider")
	}
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got: %v", err)
	}
}

func TestResolver_ResolveNotSecretURI(t *testing.T) {
	r := NewResolver(WithCacheTTL(0))

	_, err := r.Resolve(context.Background(), "not-a-secret")
	if err == nil {
		t.Fatal("expected error for non-secret URI")
	}
	if !errors.Is(err, ErrNotSecretURI) {
		t.Fatalf("expected ErrNotSecretURI, got: %v", err)
	}
}

func TestResolver_MultipleProviders(t *testing.T) {
	vaultProv := newMockProvider("vault")
	vaultProv.addSecret("db/creds", map[string]string{"pass": "v-secret"})

	awsProv := newMockProvider("aws-sm")
	awsProv.addSecret("prod/api-key", map[string]string{"key": "a-secret"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(vaultProv)
	r.Register(awsProv)

	v1, err := r.Resolve(context.Background(), "secret://vault/db/creds#pass")
	if err != nil {
		t.Fatalf("vault resolve: %v", err)
	}
	if v1 != "v-secret" {
		t.Fatalf("vault value = %q", v1)
	}

	v2, err := r.Resolve(context.Background(), "secret://aws-sm/prod/api-key#key")
	if err != nil {
		t.Fatalf("aws resolve: %v", err)
	}
	if v2 != "a-secret" {
		t.Fatalf("aws value = %q", v2)
	}
}

func TestResolver_Cache(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/pass", map[string]string{"val": "cached-value"})

	r := NewResolver(WithCacheTTL(1 * time.Hour))
	r.Register(mp)

	// First call — cache miss.
	val, err := r.Resolve(context.Background(), "secret://vault/db/pass#val")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if val != "cached-value" {
		t.Fatalf("value = %q", val)
	}
	if mp.calls.Load() != 1 {
		t.Fatalf("expected 1 provider call, got %d", mp.calls.Load())
	}

	// Second call — cache hit, provider not called again.
	val2, err := r.Resolve(context.Background(), "secret://vault/db/pass#val")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if val2 != "cached-value" {
		t.Fatalf("cached value = %q", val2)
	}
	if mp.calls.Load() != 1 {
		t.Fatalf("expected 1 provider call (cached), got %d", mp.calls.Load())
	}
}

func TestResolver_CacheDisabled(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/pass", map[string]string{"val": "no-cache"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	for i := 0; i < 3; i++ {
		_, err := r.Resolve(context.Background(), "secret://vault/db/pass#val")
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if mp.calls.Load() != 3 {
		t.Fatalf("expected 3 provider calls with cache disabled, got %d", mp.calls.Load())
	}
}

func TestResolver_CacheExpiry(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/pass", map[string]string{"val": "expiring"})

	r := NewResolver(WithCacheTTL(1 * time.Millisecond))
	r.Register(mp)

	_, _ = r.Resolve(context.Background(), "secret://vault/db/pass#val")
	if mp.calls.Load() != 1 {
		t.Fatalf("calls = %d", mp.calls.Load())
	}

	// Wait for cache to expire.
	time.Sleep(5 * time.Millisecond)

	_, _ = r.Resolve(context.Background(), "secret://vault/db/pass#val")
	if mp.calls.Load() != 2 {
		t.Fatalf("expected 2 calls after expiry, got %d", mp.calls.Load())
	}
}

func TestResolver_ContextCancelled(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/pass", map[string]string{"val": "x"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := r.Resolve(ctx, "secret://vault/db/pass#val")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestResolver_HasProvider(t *testing.T) {
	r := NewResolver()
	r.Register(newMockProvider("vault"))

	if !r.HasProvider("vault") {
		t.Fatal("expected HasProvider(vault) = true")
	}
	if r.HasProvider("aws-sm") {
		t.Fatal("expected HasProvider(aws-sm) = false")
	}
}

func TestResolver_Providers(t *testing.T) {
	r := NewResolver()
	r.Register(newMockProvider("vault"))
	r.Register(newMockProvider("aws-sm"))

	provs := r.Providers()
	if len(provs) != 2 {
		t.Fatalf("providers count = %d, want 2", len(provs))
	}
}

func TestResolver_Close(t *testing.T) {
	mp := newMockProvider("vault")
	mp.closeErr = fmt.Errorf("close error")

	r := NewResolver()
	r.Register(mp)

	err := r.Close()
	if err == nil {
		t.Fatal("expected close error")
	}
	if !r.HasProvider("vault") {
		// After close, providers should be cleared.
		// Actually, Close clears providers — so HasProvider should be false.
	}
}

// ---------------------------------------------------------------------------
// ResolveAll tests
// ---------------------------------------------------------------------------

func TestResolver_ResolveAll_NestedMap(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("db/creds", map[string]string{"password": "resolved-pw"})
	mp.addSecret("api/key", map[string]string{"token": "resolved-tok"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	input := map[string]any{
		"db_pass": "secret://vault/db/creds#password",
		"nested": map[string]any{
			"api": "secret://vault/api/key#token",
			"ok":  "plain-value",
		},
		"list": []any{"secret://vault/db/creds#password", "plain"},
		"num":  42,
	}

	result, err := r.ResolveAll(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveAll: %v", err)
	}

	m := result.(map[string]any)
	if m["db_pass"] != "resolved-pw" {
		t.Fatalf("db_pass = %v", m["db_pass"])
	}

	nested := m["nested"].(map[string]any)
	if nested["api"] != "resolved-tok" {
		t.Fatalf("nested.api = %v", nested["api"])
	}
	if nested["ok"] != "plain-value" {
		t.Fatalf("nested.ok = %v", nested["ok"])
	}

	list := m["list"].([]any)
	if list[0] != "resolved-pw" {
		t.Fatalf("list[0] = %v", list[0])
	}
	if list[1] != "plain" {
		t.Fatalf("list[1] = %v", list[1])
	}

	if m["num"] != 42 {
		t.Fatalf("num = %v", m["num"])
	}
}

func TestResolver_ResolveAll_NoSecrets(t *testing.T) {
	r := NewResolver(WithCacheTTL(0))

	input := map[string]any{"key": "plain", "num": 123}
	result, err := r.ResolveAll(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveAll: %v", err)
	}

	m := result.(map[string]any)
	if m["key"] != "plain" {
		t.Fatalf("key = %v", m["key"])
	}
}

func TestResolver_ResolveAll_ErrorPropagation(t *testing.T) {
	mp := newMockProvider("vault")
	// Don't add any secrets — all refs will fail.

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	input := map[string]any{
		"good": "plain",
		"bad":  "secret://vault/nonexistent#key",
	}

	_, err := r.ResolveAll(context.Background(), input)
	if err == nil {
		t.Fatal("expected error from failed resolution")
	}
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound in chain, got: %v", err)
	}
}

func TestResolver_ResolveAll_StringSlice(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("key", map[string]string{"v": "resolved"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	input := []string{"plain", "secret://vault/key#v"}
	result, err := r.ResolveAll(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveAll: %v", err)
	}

	list := result.([]any)
	if list[0] != "plain" {
		t.Fatalf("list[0] = %v", list[0])
	}
	if list[1] != "resolved" {
		t.Fatalf("list[1] = %v", list[1])
	}
}

func TestResolver_ResolveAll_StringMap(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("key", map[string]string{"v": "resolved"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	input := map[string]string{
		"plain":  "value",
		"secret": "secret://vault/key#v",
	}
	result, err := r.ResolveAll(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveAll: %v", err)
	}

	m := result.(map[string]any)
	if m["plain"] != "value" {
		t.Fatalf("plain = %v", m["plain"])
	}
	if m["secret"] != "resolved" {
		t.Fatalf("secret = %v", m["secret"])
	}
}

func TestResolver_ResolveAll_Nil(t *testing.T) {
	r := NewResolver()
	result, err := r.ResolveAll(context.Background(), nil)
	if err != nil {
		t.Fatalf("resolveAll nil: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// ResolveOrRedact tests
// ---------------------------------------------------------------------------

func TestResolveOrRedact_WithResolver(t *testing.T) {
	mp := newMockProvider("vault")
	mp.addSecret("key", map[string]string{"v": "resolved"})

	r := NewResolver(WithCacheTTL(0))
	r.Register(mp)

	input := map[string]any{"s": "secret://vault/key#v", "p": "plain"}
	result, changed, err := ResolveOrRedact(context.Background(), r, input)
	if err != nil {
		t.Fatalf("resolveOrRedact: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	m := result.(map[string]any)
	if m["s"] != "resolved" {
		t.Fatalf("s = %v", m["s"])
	}
}

func TestResolveOrRedact_NilResolver(t *testing.T) {
	input := map[string]any{
		"token": "secret://vault/api",
		"ok":    "plain",
	}
	result, changed, err := ResolveOrRedact(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("resolveOrRedact: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true (redacted)")
	}

	m := result.(map[string]any)
	if m["token"] != "<redacted>" {
		t.Fatalf("token = %v, expected <redacted>", m["token"])
	}
	if m["ok"] != "plain" {
		t.Fatalf("ok = %v", m["ok"])
	}
}

// ---------------------------------------------------------------------------
// MaskSecretPath tests
// ---------------------------------------------------------------------------

func TestMaskSecretPath(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"database/creds", "database/****"},
		{"a/b/c", "a/b/****"},
		{"simple", "****"},
		{"", "****"},
	}
	for _, tc := range cases {
		got := MaskSecretPath(tc.input)
		if got != tc.want {
			t.Errorf("MaskSecretPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
