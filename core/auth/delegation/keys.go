package delegation

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	envDelegationPrivateKey    = "CORDUM_DELEGATION_PRIVATE_KEY"
	envDelegationActiveKeyID   = "CORDUM_DELEGATION_KEY_ID"
	envDelegationPublicKeyStem = "CORDUM_DELEGATION_PUBLIC_KEY_"
	defaultSigningKeyID        = "dlg-1"
)

var (
	ErrSigningKeyMissing        = errors.New("delegation signing key not configured")
	ErrVerificationKeysMissing  = errors.New("delegation verification keys not configured")
	ErrInvalidSigningKey        = errors.New("invalid delegation signing key")
	ErrInvalidVerificationKey   = errors.New("invalid delegation verification key")
	ErrDelegationKeyPermissions = errors.New("delegation key file must not be group/world accessible")
)

// SigningKey wraps the active Ed25519 signing key plus its advertised KID.
type SigningKey struct {
	KID        string
	PrivateKey ed25519.PrivateKey
}

func (k SigningKey) PublicKey() ed25519.PublicKey {
	if len(k.PrivateKey) != ed25519.PrivateKeySize {
		return nil
	}
	pub, _ := k.PrivateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), pub...)
}

func (k SigningKey) String() string {
	return fmt.Sprintf("delegation.SigningKey(kid=%q, private=<redacted>)", k.KID)
}

func (k SigningKey) GoString() string {
	return k.String()
}

// GenerateSigningKey creates a fresh Ed25519 signing key.
func GenerateSigningKey(kid string) (SigningKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return SigningKey{}, fmt.Errorf("generate delegation signing key: %w", err)
	}
	return SigningKey{
		KID:        normalizeKeyID(kid),
		PrivateKey: append(ed25519.PrivateKey(nil), priv...),
	}, nil
}

// LoadSigningKeyFromEnv reads the active signing key from
// CORDUM_DELEGATION_PRIVATE_KEY.
func LoadSigningKeyFromEnv() (SigningKey, error) {
	raw := strings.TrimSpace(os.Getenv(envDelegationPrivateKey))
	if raw == "" {
		return SigningKey{}, ErrSigningKeyMissing
	}
	priv, err := parsePrivateKey([]byte(raw))
	if err != nil {
		return SigningKey{}, err
	}
	return SigningKey{
		KID:        activeKeyIDFromEnv(),
		PrivateKey: priv,
	}, nil
}

// LoadSigningKeyFromFile reads the active signing key from a filesystem path.
func LoadSigningKeyFromFile(path string) (SigningKey, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return SigningKey{}, fmt.Errorf("delegation signing key path required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return SigningKey{}, fmt.Errorf("stat delegation signing key: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return SigningKey{}, fmt.Errorf("%w: %s has mode %04o", ErrDelegationKeyPermissions, path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path) // #nosec G304 -- operator-configured key path
	if err != nil {
		return SigningKey{}, fmt.Errorf("read delegation signing key: %w", err)
	}
	priv, err := parsePrivateKey(data)
	if err != nil {
		return SigningKey{}, err
	}
	return SigningKey{
		KID:        activeKeyIDFromEnv(),
		PrivateKey: priv,
	}, nil
}

// LoadVerificationKeysFromEnv loads every verification key configured as
// CORDUM_DELEGATION_PUBLIC_KEY_<KID>=<base64 public key>.
func LoadVerificationKeysFromEnv() (map[string]ed25519.PublicKey, error) {
	keyring := make(map[string]ed25519.PublicKey)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, envDelegationPublicKeyStem) {
			continue
		}
		kid := normalizeKeyID(strings.TrimPrefix(key, envDelegationPublicKeyStem))
		if kid == "" {
			return nil, fmt.Errorf("%w: empty kid in %s", ErrInvalidVerificationKey, key)
		}
		pub, err := decodePublicKey(value)
		if err != nil {
			return nil, fmt.Errorf("%w for %s: %v", ErrInvalidVerificationKey, kid, err)
		}
		if _, exists := keyring[kid]; exists {
			return nil, fmt.Errorf("%w: duplicate kid %q", ErrInvalidVerificationKey, kid)
		}
		keyring[kid] = pub
	}
	if len(keyring) == 0 {
		return nil, ErrVerificationKeysMissing
	}
	return keyring, nil
}

// SortedKeyIDs returns the KIDs in lexical order. Useful for deterministic
// logs/tests without exposing key bytes.
func SortedKeyIDs(keyring map[string]ed25519.PublicKey) []string {
	keys := make([]string, 0, len(keyring))
	for kid := range keyring {
		keys = append(keys, kid)
	}
	sort.Strings(keys)
	return keys
}

// EncodePrivateKeyPEM encodes an Ed25519 private key as PKCS#8 PEM.
func EncodePrivateKeyPEM(privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidSigningKey
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal delegation private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}), nil
}

// EncodePublicKeyBase64 returns the standard base64 representation used by
// CORDUM_DELEGATION_PUBLIC_KEY_<KID>.
func EncodePublicKeyBase64(publicKey ed25519.PublicKey) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", ErrInvalidVerificationKey
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}

func activeKeyIDFromEnv() string {
	return normalizeKeyID(os.Getenv(envDelegationActiveKeyID))
}

func normalizeKeyID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSigningKeyID
	}
	raw = strings.ToLower(raw)
	raw = strings.ReplaceAll(raw, "_", "-")
	return raw
}

func parsePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, ErrSigningKeyMissing
	}
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: parse pkcs8: %v", ErrInvalidSigningKey, err)
		}
		privateKey, ok := key.(ed25519.PrivateKey)
		if !ok || len(privateKey) != ed25519.PrivateKeySize {
			return nil, ErrInvalidSigningKey
		}
		return append(ed25519.PrivateKey(nil), privateKey...), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		if decoded, err = base64.RawStdEncoding.DecodeString(trimmed); err != nil {
			return nil, fmt.Errorf("%w: decode base64: %v", ErrInvalidSigningKey, err)
		}
	}
	switch len(decoded) {
	case ed25519.PrivateKeySize:
		return append(ed25519.PrivateKey(nil), decoded...), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	default:
		return nil, fmt.Errorf("%w: expected %d-byte private key or %d-byte seed, got %d bytes", ErrInvalidSigningKey, ed25519.PrivateKeySize, ed25519.SeedSize, len(decoded))
	}
}

func decodePublicKey(raw string) (ed25519.PublicKey, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "ed25519:"))
	if raw == "" {
		return nil, ErrInvalidVerificationKey
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
			return nil, err
		}
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, ErrInvalidVerificationKey
	}
	return ed25519.PublicKey(data), nil
}
