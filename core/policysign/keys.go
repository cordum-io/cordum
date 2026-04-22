package policysign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Environment variable conventions for policy signing. These deliberately
// mirror the "CORDUM_LICENSE_*" scheme from core/licensing so operators
// have one mental model to learn.
const (
	// EnvSigningKey holds the Ed25519 private key that the gateway uses to
	// sign policy bundles on save. Accepted as PEM (PKCS#8 OR raw ed25519
	// "PRIVATE KEY") or base64-encoded 64-byte seed+public representation.
	EnvSigningKey = "CORDUM_POLICY_SIGNING_KEY"

	// EnvSigningKeyPath points to a file containing the same formats as
	// EnvSigningKey. Used when the raw key is inconvenient to place in
	// environment variables.
	EnvSigningKeyPath = "CORDUM_POLICY_SIGNING_KEY_PATH"

	// EnvSigningKeyID identifies which public key verifiers should use.
	// Falls back to "default".
	EnvSigningKeyID = "CORDUM_POLICY_SIGNING_KEY_ID"

	// EnvDevSigningSeed derives a deterministic development-only Ed25519 key
	// pair when explicit signing keys are not configured. This is intended for
	// local Docker/demo stacks so they can exercise signed-policy flows without
	// checking a private key into the repo.
	EnvDevSigningSeed = "CORDUM_POLICY_DEV_SIGNING_SEED"

	// EnvPublicKeyPrefix is the common prefix for trusted verification keys.
	// Each key is exported as CORDUM_POLICY_PUBLIC_KEY_<ID>=<base64>.
	EnvPublicKeyPrefix = "CORDUM_POLICY_PUBLIC_KEY_"

	// Legacy env vars from the initial safety-kernel implementation. The
	// trust store accepts them so existing deployments keep working.
	envLegacyPublicKey = "SAFETY_POLICY_PUBLIC_KEY"
	envLegacyKeyID     = "SAFETY_POLICY_PUBLIC_KEY_ID"

	// DefaultKeyID is used when no explicit key id has been configured.
	DefaultKeyID = "default"
)

// ErrSigningKeyNotConfigured is returned by LoadPrivateKeyFromEnv when
// neither env var nor key-path is set. Callers decide whether that is
// fatal (strict enforce + no key) or acceptable (strict off).
var ErrSigningKeyNotConfigured = errors.New("policysign: signing key not configured")

// TrustStore holds a verifier's view of the world: one or more trusted
// public keys, keyed by key id. Lookups are case-insensitive to match
// how operators type env-var names.
type TrustStore struct {
	keys map[string]ed25519.PublicKey
}

// NewTrustStore returns an empty TrustStore.
func NewTrustStore() *TrustStore {
	return &TrustStore{keys: map[string]ed25519.PublicKey{}}
}

// Add registers a trusted public key under keyID. Duplicate ids replace
// the previous key. An empty key id is rejected; a malformed key is
// rejected.
func (t *TrustStore) Add(keyID string, pub ed25519.PublicKey) error {
	if t == nil {
		return errors.New("policysign: nil trust store")
	}
	id := normalizeKeyID(keyID)
	if id == "" {
		return ErrEmptyKeyID
	}
	if len(pub) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	if t.keys == nil {
		t.keys = map[string]ed25519.PublicKey{}
	}
	t.keys[id] = append(ed25519.PublicKey(nil), pub...)
	return nil
}

// Lookup returns the trusted key for keyID, if any.
func (t *TrustStore) Lookup(keyID string) (ed25519.PublicKey, bool) {
	if t == nil || t.keys == nil {
		return nil, false
	}
	key, ok := t.keys[normalizeKeyID(keyID)]
	return key, ok
}

// Len reports how many trusted keys are loaded.
func (t *TrustStore) Len() int {
	if t == nil {
		return 0
	}
	return len(t.keys)
}

// IDs returns the sorted list of registered key ids. Useful for boot
// logs.
func (t *TrustStore) IDs() []string {
	if t == nil {
		return nil
	}
	out := make([]string, 0, len(t.keys))
	for id := range t.keys {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// LoadPrivateKeyFromEnv reads the signing private key from the
// environment. The key id is returned alongside the key and defaults to
// "default" when EnvSigningKeyID is unset.
//
// Precedence: EnvSigningKey (inline) > EnvSigningKeyPath (file).
// Both PEM and base64/hex formats are accepted. Key material is never
// logged; errors describe only the failure mode.
func LoadPrivateKeyFromEnv() (ed25519.PrivateKey, string, error) {
	keyID := strings.TrimSpace(os.Getenv(EnvSigningKeyID))
	if keyID == "" {
		keyID = DefaultKeyID
	}
	if raw := strings.TrimSpace(os.Getenv(EnvSigningKey)); raw != "" {
		key, err := parsePrivateKey(raw)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvSigningKey, err)
		}
		return key, keyID, nil
	}
	if path := strings.TrimSpace(os.Getenv(EnvSigningKeyPath)); path != "" {
		data, err := os.ReadFile(path) // #nosec G304 -- operator-configured path.
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvSigningKeyPath, err)
		}
		key, err := parsePrivateKey(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvSigningKeyPath, err)
		}
		return key, keyID, nil
	}
	if seed := strings.TrimSpace(os.Getenv(EnvDevSigningSeed)); seed != "" {
		return derivePrivateKeyFromSeed(seed), keyID, nil
	}
	return nil, "", ErrSigningKeyNotConfigured
}

// LoadTrustStoreFromEnv scans os.Environ() for keys prefixed with
// EnvPublicKeyPrefix (e.g. CORDUM_POLICY_PUBLIC_KEY_PRIMARY=...) and
// returns a populated TrustStore. It also honours the single-key legacy
// SAFETY_POLICY_PUBLIC_KEY (id from SAFETY_POLICY_PUBLIC_KEY_ID, default
// "default") so previously configured deployments keep working during
// rollout.
//
// Malformed or wrong-size values cause the load to fail — operators who
// typo a key value should see the error at boot, not at first request.
func LoadTrustStoreFromEnv() (*TrustStore, error) {
	store := NewTrustStore()
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if !strings.HasPrefix(name, EnvPublicKeyPrefix) {
			continue
		}
		id := strings.TrimPrefix(name, EnvPublicKeyPrefix)
		if strings.TrimSpace(id) == "" {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		pub, err := parsePublicKey(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", EnvPublicKeyPrefix, id, err)
		}
		if err := store.Add(id, pub); err != nil {
			return nil, fmt.Errorf("%s%s: %w", EnvPublicKeyPrefix, id, err)
		}
	}
	if raw := strings.TrimSpace(os.Getenv(envLegacyPublicKey)); raw != "" {
		pub, err := parsePublicKey(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", envLegacyPublicKey, err)
		}
		id := strings.TrimSpace(os.Getenv(envLegacyKeyID))
		if id == "" {
			id = DefaultKeyID
		}
		if _, exists := store.Lookup(id); !exists {
			if err := store.Add(id, pub); err != nil {
				return nil, fmt.Errorf("%s: %w", envLegacyPublicKey, err)
			}
		}
	}
	if store.Len() == 0 {
		if seed := strings.TrimSpace(os.Getenv(EnvDevSigningSeed)); seed != "" {
			keyID := strings.TrimSpace(os.Getenv(EnvSigningKeyID))
			if keyID == "" {
				keyID = DefaultKeyID
			}
			if err := store.Add(keyID, derivePrivateKeyFromSeed(seed).Public().(ed25519.PublicKey)); err != nil {
				return nil, fmt.Errorf("%s: %w", EnvDevSigningSeed, err)
			}
		}
	}
	return store, nil
}

func derivePrivateKeyFromSeed(raw string) ed25519.PrivateKey {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return ed25519.NewKeyFromSeed(sum[:ed25519.SeedSize])
}

// ParsePrivateKey is the exported form of parsePrivateKey. It is used
// by cordumctl and other external callers that need to accept the same
// range of input formats as the env loaders.
func ParsePrivateKey(raw string) (ed25519.PrivateKey, error) { return parsePrivateKey(raw) }

// ParsePublicKey is the exported form of parsePublicKey.
func ParsePublicKey(raw string) (ed25519.PublicKey, error) { return parsePublicKey(raw) }

// parsePrivateKey accepts PEM ("PRIVATE KEY" PKCS#8 or "ED25519 PRIVATE
// KEY" for convenience) or base64/hex of the raw 64-byte ed25519 seed.
func parsePrivateKey(raw string) (ed25519.PrivateKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidPrivateKey
	}
	if strings.HasPrefix(raw, "-----BEGIN") {
		block, _ := pem.Decode([]byte(raw))
		if block == nil {
			return nil, fmt.Errorf("%w: pem block missing", ErrInvalidPrivateKey)
		}
		switch block.Type {
		case "PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
			}
			key, ok := parsed.(ed25519.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("%w: not ed25519", ErrInvalidPrivateKey)
			}
			if len(key) != ed25519.PrivateKeySize {
				return nil, ErrInvalidPrivateKey
			}
			return key, nil
		case "ED25519 PRIVATE KEY":
			if len(block.Bytes) == ed25519.PrivateKeySize {
				return ed25519.PrivateKey(block.Bytes), nil
			}
			if len(block.Bytes) == ed25519.SeedSize {
				return ed25519.NewKeyFromSeed(block.Bytes), nil
			}
			return nil, ErrInvalidPrivateKey
		default:
			return nil, fmt.Errorf("%w: unsupported pem type %q", ErrInvalidPrivateKey, block.Type)
		}
	}
	raw = strings.TrimPrefix(raw, "ed25519:")
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return privateFromBytes(data)
	}
	if data, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return privateFromBytes(data)
	}
	if data, err := hex.DecodeString(raw); err == nil {
		return privateFromBytes(data)
	}
	return nil, fmt.Errorf("%w: not PEM, base64, or hex", ErrInvalidPrivateKey)
}

func privateFromBytes(data []byte) (ed25519.PrivateKey, error) {
	switch len(data) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(append([]byte(nil), data...)), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(data), nil
	default:
		return nil, fmt.Errorf("%w: wrong length %d", ErrInvalidPrivateKey, len(data))
	}
}

func parsePublicKey(raw string) (ed25519.PublicKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidPublicKey
	}
	if strings.HasPrefix(raw, "-----BEGIN") {
		block, _ := pem.Decode([]byte(raw))
		if block == nil {
			return nil, fmt.Errorf("%w: pem block missing", ErrInvalidPublicKey)
		}
		switch block.Type {
		case "PUBLIC KEY":
			parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
			}
			key, ok := parsed.(ed25519.PublicKey)
			if !ok {
				return nil, fmt.Errorf("%w: not ed25519", ErrInvalidPublicKey)
			}
			if len(key) != ed25519.PublicKeySize {
				return nil, ErrInvalidPublicKey
			}
			return key, nil
		default:
			return nil, fmt.Errorf("%w: unsupported pem type %q", ErrInvalidPublicKey, block.Type)
		}
	}
	raw = strings.TrimPrefix(raw, "ed25519:")
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil && len(data) == ed25519.PublicKeySize {
		return ed25519.PublicKey(append([]byte(nil), data...)), nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(data) == ed25519.PublicKeySize {
		return ed25519.PublicKey(append([]byte(nil), data...)), nil
	}
	if data, err := hex.DecodeString(raw); err == nil && len(data) == ed25519.PublicKeySize {
		return ed25519.PublicKey(append([]byte(nil), data...)), nil
	}
	return nil, fmt.Errorf("%w: expected %d-byte ed25519 public key", ErrInvalidPublicKey, ed25519.PublicKeySize)
}

func normalizeKeyID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
