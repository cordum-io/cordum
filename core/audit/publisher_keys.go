package audit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/policysign"
)

// Audit publisher keys are configured separately from control-plane policy keys.
const (
	EnvPublisherSigningKey      = "CORDUM_AUDIT_SIGNING_KEY"
	EnvPublisherSigningKeyPath  = "CORDUM_AUDIT_SIGNING_KEY_PATH"
	EnvPublisherSigningKeyID    = "CORDUM_AUDIT_SIGNING_KEY_ID"
	EnvPublisherDevSigningSeed  = "CORDUM_AUDIT_DEV_SIGNING_SEED"
	EnvPublisherPublicKeyPrefix = "CORDUM_AUDIT_PUBLIC_KEY_"
)

// ErrPublisherKeyNotConfigured leaves the missing-key policy to the caller.
var ErrPublisherKeyNotConfigured = errors.New("audit: publisher signing key not configured")

// LoadPublisherKeyFromEnv reads an audit publisher key: inline, file, then dev seed.
func LoadPublisherKeyFromEnv(identity string) (ed25519.PrivateKey, string, error) {
	if identity != servicetoken.IdentityMCP && identity != servicetoken.IdentitySafetyKernel {
		return nil, "", errors.New("audit: unsupported publisher identity")
	}
	keyID := strings.TrimSpace(os.Getenv(EnvPublisherSigningKeyID))
	if keyID == "" {
		keyID = policysign.DefaultKeyID
	}
	if raw := strings.TrimSpace(os.Getenv(EnvPublisherSigningKey)); raw != "" {
		key, err := policysign.ParsePrivateKey(raw)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvPublisherSigningKey, err)
		}
		return key, keyID, nil
	}
	if path := strings.TrimSpace(os.Getenv(EnvPublisherSigningKeyPath)); path != "" {
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvPublisherSigningKeyPath, err)
		}
		key, err := policysign.ParsePrivateKey(strings.TrimSpace(string(data)))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", EnvPublisherSigningKeyPath, err)
		}
		return key, keyID, nil
	}
	if seed := strings.TrimSpace(os.Getenv(EnvPublisherDevSigningSeed)); seed != "" {
		return publisherKeyFromSeed(identity, seed), keyID, nil
	}
	return nil, "", ErrPublisherKeyNotConfigured
}

// LoadPublisherTrustFromEnv returns public keys bound to audit publisher subjects.
// An explicit public key takes precedence over the identity-specific dev key.
func LoadPublisherTrustFromEnv() (map[string]ed25519.PublicKey, error) {
	trust := make(map[string]ed25519.PublicKey)
	seed := strings.TrimSpace(os.Getenv(EnvPublisherDevSigningSeed))
	for _, identity := range []string{servicetoken.IdentityMCP, servicetoken.IdentitySafetyKernel} {
		name := EnvPublisherPublicKeyPrefix + strings.ReplaceAll(strings.ToUpper(identity), "-", "_")
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			key, err := policysign.ParsePublicKey(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			trust[identity] = key
		} else if seed != "" {
			trust[identity] = publisherKeyFromSeed(identity, seed).Public().(ed25519.PublicKey)
		}
	}
	return trust, nil
}

func publisherKeyFromSeed(identity, seed string) ed25519.PrivateKey {
	sum := sha256.Sum256([]byte("cordum-audit-publisher:" + identity + ":" + seed))
	return ed25519.NewKeyFromSeed(sum[:])
}
