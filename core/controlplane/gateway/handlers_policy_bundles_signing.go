package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/policysign"
)

// policyBundleSignatureKey is the map entry attached to a bundle when
// it has been signed. Verifiers ignore any keys prefixed with an
// underscore when parsing YAML, so this will not pollute the policy
// itself.
const policyBundleSignatureKey = "_signature"

// signerOutcome carries the result of attempting to sign a bundle. If
// status is non-zero the caller must abort the request with that HTTP
// code and message — typically 503 when strict-mode requires a signature
// but no key is configured.
type signerOutcome struct {
	Signature map[string]any
	Status    int
	Message   string
}

// signPolicyBundleContent hashes content with SHA-256 and signs it with
// the Ed25519 key from CORDUM_POLICY_SIGNING_KEY / _PATH, honouring
// CORDUM_POLICY_STRICT.
//
// Returns:
//   - outcome.Signature populated and Status=0 on success
//   - outcome.Status=0 and outcome.Signature=nil when strict=off and no
//     key is configured (signing skipped, OK to save unsigned bundle)
//   - outcome.Status=503 with actionable Message when strict != off and
//     no key is configured
//   - outcome.Status=500 for internal errors (malformed key)
//
// Key material is never logged.
func signPolicyBundleContent(_ context.Context, content []byte) signerOutcome {
	mode, err := policysign.ModeFromEnv()
	if err != nil {
		slog.Warn("policy bundle signing: invalid CORDUM_POLICY_STRICT value, defaulting to warn", "error", err)
	}
	priv, keyID, err := policysign.LoadPrivateKeyFromEnv()
	if err != nil {
		if errors.Is(err, policysign.ErrSigningKeyNotConfigured) {
			if mode.SkipsVerification() {
				return signerOutcome{}
			}
			return signerOutcome{
				Status: http.StatusServiceUnavailable,
				Message: "policy signing key not configured: set CORDUM_POLICY_SIGNING_KEY (Ed25519 PEM or base64) " +
					"or set CORDUM_POLICY_STRICT=off to allow unsigned bundles",
			}
		}
		slog.Error("policy bundle signing: failed to load signing key", "error", err)
		return signerOutcome{Status: http.StatusInternalServerError, Message: "failed to load policy signing key"}
	}
	sig, err := policysign.Sign(priv, keyID, content)
	if err != nil {
		slog.Error("policy bundle signing: Sign failed", "error", err)
		return signerOutcome{Status: http.StatusInternalServerError, Message: "failed to sign policy bundle"}
	}
	slog.Info("policy bundle signed", "key_id", sig.KeyID, "hash", sig.Hash, "bytes", sig.SignedBytes)
	return signerOutcome{Signature: signatureToMap(sig)}
}

// signatureToMap returns the wire shape stored in Redis. Keys mirror
// policysign.Signature JSON tags so the kernel and cordumctl can
// marshal/unmarshal symmetrically.
func signatureToMap(sig policysign.Signature) map[string]any {
	return map[string]any{
		"algorithm":    sig.Algorithm,
		"key_id":       sig.KeyID,
		"value":        sig.Value,
		"hash":         sig.Hash,
		"signed_bytes": sig.SignedBytes,
	}
}

// signatureFromMap is the inverse of signatureToMap. A nil or empty raw
// returns an IsZero Signature so callers can distinguish "unsigned
// bundle" from "malformed signature" with policysign.Signature.IsZero.
func signatureFromMap(raw any) (policysign.Signature, bool) {
	if raw == nil {
		return policysign.Signature{}, false
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return policysign.Signature{}, false
	}
	sig := policysign.Signature{
		Algorithm: policybundles.StringFromAny(m["algorithm"]),
		KeyID:     policybundles.StringFromAny(m["key_id"]),
		Value:     policybundles.StringFromAny(m["value"]),
		Hash:      policybundles.StringFromAny(m["hash"]),
	}
	switch v := m["signed_bytes"].(type) {
	case int:
		sig.SignedBytes = v
	case int64:
		sig.SignedBytes = int(v)
	case float64:
		sig.SignedBytes = int(v)
	}
	if strings.TrimSpace(sig.Algorithm) == "" && strings.TrimSpace(sig.Value) == "" {
		return policysign.Signature{}, false
	}
	return sig, true
}
