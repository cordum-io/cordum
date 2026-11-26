package gateway

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cordum/cordum/core/policysign"
)

var errPolicyStoreUnsignable = errors.New("policy store content cannot be stored signed")

type policyStoreSigner struct {
	key   ed25519.PrivateKey
	keyID string
}

func (p *policyStoreSigner) sign(content []byte) (map[string]any, error) {
	sig, err := policysign.Sign(p.key, p.keyID, content)
	if err != nil {
		return nil, err
	}
	return signatureToMap(sig), nil
}

// resolvePolicyStoreSigner preflights any policy-store writer before mutation.
// An undetermined mode is enforce; explicit warn/off retain unsigned writes.
// Capture the key once so each write uses the same key the preflight checked.
func resolvePolicyStoreSigner(subject string) (*policyStoreSigner, error) {
	mode, modeErr := policysign.ResolveMode()
	enforce := modeErr != nil || mode == policysign.ModeEnforce
	key, keyID, keyErr := policysign.LoadPrivateKeyFromEnv()
	// Raw Ed25519 keys carry a public half; require it to match the seed.
	if keyErr == nil && !key.Equal(ed25519.NewKeyFromSeed(key.Seed())) {
		keyErr = policysign.ErrInvalidPrivateKey
	}
	if keyErr != nil {
		reason := "the policy signing key could not be loaded"
		if errors.Is(keyErr, policysign.ErrSigningKeyNotConfigured) {
			reason = "no policy signing key is configured"
		}
		if enforce {
			return nil, fmt.Errorf("%w: %s", errPolicyStoreUnsignable, reason)
		}
		slog.Warn("policy store content will be stored unsigned", "subject", subject, "policy_mode", mode, "reason", reason)
		return nil, nil
	}
	signer := &policyStoreSigner{key: key, keyID: keyID}
	trust, trustErr := policysign.LoadTrustStoreFromEnv()
	pub, found := trust.Lookup(keyID)
	if trustErr == nil && found && pub.Equal(key.Public()) {
		return signer, nil
	}
	if enforce {
		return nil, fmt.Errorf("%w: policy signing key %q is not in the policy trust store", errPolicyStoreUnsignable, keyID)
	}
	slog.Warn("policy store content signed with an untrusted key", "subject", subject, "policy_mode", mode, "key_id", keyID)
	return signer, nil
}
