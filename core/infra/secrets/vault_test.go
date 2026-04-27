package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Vault mock server
// ---------------------------------------------------------------------------

func newVaultMockServer(t *testing.T, secrets map[string]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate auth.
		token := r.Header.Get("X-Vault-Token")
		if token == "" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errors": []string{"permission denied"},
			})
			return
		}
		if token == "bad-token" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errors": []string{"permission denied"},
			})
			return
		}

		// Extract path: /v1/{mount}/data/{path}
		// We'll strip /v1/secret/data/ prefix.
		path := strings.TrimPrefix(r.URL.Path, "/v1/secret/data/")
		if path == r.URL.Path {
			// Try with custom mount.
			path = strings.TrimPrefix(r.URL.Path, "/v1/custom-mount/data/")
		}

		data, ok := secrets[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errors": []string{"secret not found"},
			})
			return
		}

		resp := map[string]any{
			"data": map[string]any{
				"data": data,
				"metadata": map[string]any{
					"version": 1,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestVaultProvider_ResolveWithKey(t *testing.T) {
	srv := newVaultMockServer(t, map[string]map[string]any{
		"database/creds": {"username": "admin", "password": "s3cret"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "secret",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "database/creds", Key: "password"}
	val, err := vp.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "s3cret" {
		t.Fatalf("value = %q, want %q", val, "s3cret")
	}
}

func TestVaultProvider_ResolveSingleField(t *testing.T) {
	// When a secret has exactly one field and no key is specified,
	// return that field's value.
	srv := newVaultMockServer(t, map[string]map[string]any{
		"api/token": {"value": "tok-123"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "api/token"}
	val, err := vp.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "tok-123" {
		t.Fatalf("value = %q, want %q", val, "tok-123")
	}
}

func TestVaultProvider_ResolveMultiFieldNoKey(t *testing.T) {
	// When a secret has multiple fields and no key, error.
	srv := newVaultMockServer(t, map[string]map[string]any{
		"multi": {"a": "1", "b": "2"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "multi"}
	_, err = vp.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected error for multi-field secret without key")
	}
	if !strings.Contains(err.Error(), "#key fragment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVaultProvider_SecretNotFound(t *testing.T) {
	srv := newVaultMockServer(t, map[string]map[string]any{})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "nonexistent"}
	_, err = vp.Resolve(context.Background(), ref)
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got: %v", err)
	}
}

func TestVaultProvider_KeyNotFound(t *testing.T) {
	srv := newVaultMockServer(t, map[string]map[string]any{
		"db": {"user": "admin"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "db", Key: "password"}
	_, err = vp.Resolve(context.Background(), ref)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got: %v", err)
	}
}

func TestVaultProvider_AccessDenied(t *testing.T) {
	srv := newVaultMockServer(t, map[string]map[string]any{
		"db": {"pass": "x"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "bad-token", "",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "db", Key: "pass"}
	_, err = vp.Resolve(context.Background(), ref)
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied, got: %v", err)
	}
}

func TestVaultProvider_Timeout(t *testing.T) {
	// Server that hangs.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "",
		WithVaultHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "db", Key: "pass"}
	_, err = vp.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestVaultProvider_CustomMount(t *testing.T) {
	srv := newVaultMockServer(t, map[string]map[string]any{
		"mykey": {"value": "custom-mount-val"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "custom-mount",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ref := SecretRef{Provider: "vault", Path: "mykey"}
	val, err := vp.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if val != "custom-mount-val" {
		t.Fatalf("value = %q", val)
	}
}

func TestVaultProvider_Scheme(t *testing.T) {
	vp, err := NewVaultProvider("https://vault.example.com", "tok", "")
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}
	if vp.Scheme() != "vault" {
		t.Fatalf("scheme = %q, want vault", vp.Scheme())
	}
}

func TestVaultProvider_ValidationErrors(t *testing.T) {
	_, err := NewVaultProvider("", "tok", "")
	if err == nil {
		t.Fatal("expected error for empty addr")
	}

	_, err = NewVaultProvider("https://vault.example.com", "", "")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestVaultProvider_Close(t *testing.T) {
	vp, _ := NewVaultProvider("https://vault.example.com", "tok", "")
	if err := vp.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestVaultProvider_ContextCancelled(t *testing.T) {
	srv := newVaultMockServer(t, map[string]map[string]any{
		"db": {"pass": "x"},
	})
	defer srv.Close()

	vp, err := NewVaultProvider(srv.URL, "test-token", "",
		WithVaultHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new vault provider: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ref := SecretRef{Provider: "vault", Path: "db", Key: "pass"}
	_, err = vp.Resolve(ctx, ref)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
