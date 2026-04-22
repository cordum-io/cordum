package outbound

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptoRand "crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// randRead delegates to crypto/rand. Kept as a package-level var for
// tests that need a deterministic keypair; production path is via the
// randFactory variable below.
var randRead = cryptoRand.Read

// x509MarshalECPrivateKey / x509MarshalPKIXPublicKey — small re-exports
// used by the CLI keygen and by roundtrip tests so they don't need to
// import x509 directly.
var (
	x509MarshalECPrivateKey = x509.MarshalECPrivateKey
	x509MarshalPKIXPublicKey = x509.MarshalPKIXPublicKey
)

// Env var names for outbound signing / inbound trust-store. Kept as
// constants so operator docs can reference one source of truth.
const (
	EnvSigningKey     = "CORDUM_MCP_OUTBOUND_SIGNING_KEY"
	EnvSigningKeyPath = "CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH"
	EnvSigningKeyID   = "CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID"
	EnvTrustedKeyPrefix = "CORDUM_MCP_INBOUND_TRUSTED_KEY_"
)

// ErrSigningKeyNotConfigured — operator didn't set CORDUM_MCP_OUTBOUND_SIGNING_KEY.
// The gateway treats this as "no-op signing, warn once, continue" in
// dev and as fatal in enforce mode (decision lives in the boot check).
var ErrSigningKeyNotConfigured = errors.New("mcp outbound: signing key not configured")

// LoadPrivateKeyFromEnv reads the ECDSA P-256 private key from the
// CORDUM_MCP_OUTBOUND_SIGNING_KEY env var (inline) or
// CORDUM_MCP_OUTBOUND_SIGNING_KEY_PATH (file). Accepts PEM (PKCS#8 or
// "EC PRIVATE KEY") or base64-encoded DER. Rejects non-P-256 curves.
// Key ID defaults to CORDUM_MCP_OUTBOUND_SIGNING_KEY_ID; falls back to
// "default" when unset.
//
// Returns ErrSigningKeyNotConfigured when neither env var is set.
func LoadPrivateKeyFromEnv() (*ecdsa.PrivateKey, string, error) {
	keyID := strings.TrimSpace(os.Getenv(EnvSigningKeyID))
	if keyID == "" {
		keyID = "default"
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
	return nil, "", ErrSigningKeyNotConfigured
}

// LoadTrustStoreFromEnv scans os.Environ() for entries prefixed with
// CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>=<base64-P256-pubkey>. Never logs
// key material. Malformed or non-P-256 entries abort the load so a
// typo is caught at boot rather than at first verify.
func LoadTrustStoreFromEnv() (map[string]*ecdsa.PublicKey, error) {
	out := map[string]*ecdsa.PublicKey{}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if !strings.HasPrefix(name, EnvTrustedKeyPrefix) {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(name, EnvTrustedKeyPrefix))
		if id == "" || strings.TrimSpace(value) == "" {
			continue
		}
		pub, err := parsePublicKey(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", EnvTrustedKeyPrefix, id, err)
		}
		out[id] = pub
	}
	return out, nil
}

// parsePrivateKey accepts PEM or base64 DER input and returns an
// ecdsa.P256 private key. Mirrors the shape used in
// core/policysign/keys.go so operators have one mental model.
func parsePrivateKey(raw string) (*ecdsa.PrivateKey, error) {
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
		case "EC PRIVATE KEY":
			k, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
			}
			if k.Curve != elliptic.P256() {
				return nil, fmt.Errorf("%w: not P-256", ErrInvalidPrivateKey)
			}
			return k, nil
		case "PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
			}
			ec, ok := parsed.(*ecdsa.PrivateKey)
			if !ok || ec.Curve != elliptic.P256() {
				return nil, fmt.Errorf("%w: not P-256 ecdsa", ErrInvalidPrivateKey)
			}
			return ec, nil
		default:
			return nil, fmt.Errorf("%w: unsupported pem type %q", ErrInvalidPrivateKey, block.Type)
		}
	}
	// base64 DER of either PKCS#8 or raw EC private key.
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
			return nil, fmt.Errorf("%w: base64: %v", ErrInvalidPrivateKey, err)
		}
	}
	if k, err := x509.ParseECPrivateKey(data); err == nil {
		if k.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%w: not P-256", ErrInvalidPrivateKey)
		}
		return k, nil
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		if ec, ok := parsed.(*ecdsa.PrivateKey); ok && ec.Curve == elliptic.P256() {
			return ec, nil
		}
	}
	return nil, fmt.Errorf("%w: neither EC nor PKCS#8 P-256", ErrInvalidPrivateKey)
}

// parsePublicKey accepts PEM or base64 DER input and returns an
// ecdsa.P256 public key.
func parsePublicKey(raw string) (*ecdsa.PublicKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidPublicKey
	}
	if strings.HasPrefix(raw, "-----BEGIN") {
		block, _ := pem.Decode([]byte(raw))
		if block == nil {
			return nil, fmt.Errorf("%w: pem block missing", ErrInvalidPublicKey)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
		}
		ec, ok := parsed.(*ecdsa.PublicKey)
		if !ok || ec.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%w: not P-256 ecdsa", ErrInvalidPublicKey)
		}
		return ec, nil
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
			return nil, fmt.Errorf("%w: base64: %v", ErrInvalidPublicKey, err)
		}
	}
	parsed, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("%w: PKIX: %v", ErrInvalidPublicKey, err)
	}
	ec, ok := parsed.(*ecdsa.PublicKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: not P-256 ecdsa", ErrInvalidPublicKey)
	}
	return ec, nil
}

// GeneratePrivateKey creates a fresh P-256 key. Used by
// `cordumctl mcp keygen` and as a convenience for tests.
func GeneratePrivateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), randFactory())
}

// randFactory is a var so tests can inject a deterministic reader.
// Defaults to crypto/rand.Reader.
var randFactory = func() interface {
	Read([]byte) (int, error)
} {
	return cryptoRandReader{}
}

type cryptoRandReader struct{}

func (cryptoRandReader) Read(p []byte) (int, error) {
	// crypto/rand is imported by signing.go via the rand.Reader global —
	// importing the package here directly keeps keys.go self-contained.
	return randRead(p)
}
