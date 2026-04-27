package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Vault environment variable names.
const (
	EnvVaultAddr  = "VAULT_ADDR"  // e.g. "https://vault.example.com:8200"
	EnvVaultToken = "VAULT_TOKEN" // Vault authentication token
	EnvVaultMount = "VAULT_MOUNT" // KV v2 mount point (default: "secret")
)

// VaultProvider resolves secrets from a HashiCorp Vault KV v2 engine.
//
// It uses the Vault HTTP API directly (no SDK dependency) for a minimal
// footprint.  The API contract is:
//
//	GET {addr}/v1/{mount}/data/{path}
//	X-Vault-Token: {token}
//	X-Vault-Request: true
//
// Response (200):
//
//	{ "data": { "data": { "key": "value", ... }, "metadata": { ... } } }
//
// The provider extracts .data.data from the response.  When a SecretRef
// includes a Key (URI fragment), only that field is returned.
type VaultProvider struct {
	addr   string       // base URL, no trailing slash
	token  string       // X-Vault-Token header value
	mount  string       // KV v2 mount point
	client *http.Client // configurable for tests
}

// VaultOption configures optional VaultProvider behaviour.
type VaultOption func(*VaultProvider)

// WithVaultHTTPClient overrides the default HTTP client.  Useful for
// tests (httptest) and for injecting custom TLS configuration.
func WithVaultHTTPClient(c *http.Client) VaultOption {
	return func(v *VaultProvider) { v.client = c }
}

// NewVaultProvider creates a VaultProvider.
//
//   - addr: Vault server address (e.g. "https://vault.example.com:8200").
//   - token: Vault authentication token.
//   - mount: KV v2 mount point (empty defaults to "secret").
//
// The provider validates that addr and token are non-empty.
func NewVaultProvider(addr, token, mount string, opts ...VaultOption) (*VaultProvider, error) {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	token = strings.TrimSpace(token)
	mount = strings.TrimSpace(mount)

	if addr == "" {
		return nil, fmt.Errorf("vault: address is required (set %s)", EnvVaultAddr)
	}
	if token == "" {
		return nil, fmt.Errorf("vault: token is required (set %s)", EnvVaultToken)
	}
	if mount == "" {
		mount = "secret"
	}

	v := &VaultProvider{
		addr:  addr,
		token: token,
		mount: mount,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	for _, o := range opts {
		o(v)
	}
	return v, nil
}

func (v *VaultProvider) Scheme() string { return "vault" }

func (v *VaultProvider) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	// Build the KV v2 read URL: /v1/{mount}/data/{path}
	url := fmt.Sprintf("%s/v1/%s/data/%s", v.addr, v.mount, ref.Path)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("vault: build request: %w", err)
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("X-Vault-Request", "true")

	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault: request %s: %w", MaskSecretPath(ref.Path), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		return "", fmt.Errorf("vault: read response: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// success — parse below
	case http.StatusNotFound:
		return "", fmt.Errorf("vault: %s: %w", MaskSecretPath(ref.Path), ErrSecretNotFound)
	case http.StatusForbidden, http.StatusUnauthorized:
		return "", fmt.Errorf("vault: %s: %w", MaskSecretPath(ref.Path), ErrAccessDenied)
	default:
		return "", fmt.Errorf("vault: %s: unexpected status %d: %s",
			MaskSecretPath(ref.Path), resp.StatusCode, truncate(string(body), 200))
	}

	// Parse Vault KV v2 response envelope.
	var envelope vaultKVv2Response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("vault: parse response for %s: %w", MaskSecretPath(ref.Path), err)
	}

	data := envelope.Data.Data
	if data == nil {
		return "", fmt.Errorf("vault: %s: response has no data", MaskSecretPath(ref.Path))
	}

	if ref.Key == "" {
		// No key specified.  If the secret has exactly one field, return
		// it.  Otherwise require a key selector.
		if len(data) == 1 {
			for _, v := range data {
				return fmt.Sprint(v), nil
			}
		}
		return "", fmt.Errorf("vault: %s has %d fields — specify a #key fragment",
			MaskSecretPath(ref.Path), len(data))
	}

	val, ok := data[ref.Key]
	if !ok {
		return "", fmt.Errorf("vault: %s#%s: %w", MaskSecretPath(ref.Path), ref.Key, ErrKeyNotFound)
	}

	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("vault: %s#%s: value is %T, expected string",
			MaskSecretPath(ref.Path), ref.Key, val)
	}
	return s, nil
}

func (v *VaultProvider) Close() error { return nil }

// ---------------------------------------------------------------------------
// Vault KV v2 response types
// ---------------------------------------------------------------------------

type vaultKVv2Response struct {
	Data vaultKVv2DataWrapper `json:"data"`
}

type vaultKVv2DataWrapper struct {
	Data     map[string]any    `json:"data"`
	Metadata map[string]any    `json:"metadata"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
