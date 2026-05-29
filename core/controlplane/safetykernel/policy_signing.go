package safetykernel

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/policysign"
)

// policySignatureFieldKey is the bundle-map entry that the gateway
// attaches when signing. See core/controlplane/gateway/handlers_policy_bundles_signing.go.
const policySignatureFieldKey = "_signature"

// ErrBundleUnsigned is returned when a bundle has no signature and the
// current strict mode demands one. Callers treat this as a load-refusal
// (enforce mode).
var ErrBundleUnsigned = errors.New("safetykernel: bundle is unsigned")

// ErrBundleSignatureMalformed is returned when a bundle carries a
// _signature entry but it cannot be decoded into a policysign.Signature.
var ErrBundleSignatureMalformed = errors.New("safetykernel: bundle signature is malformed")

// ErrUntrustedKeyID is returned when the signature's key_id is not
// registered in the trust store.
var ErrUntrustedKeyID = errors.New("safetykernel: signature key_id is not trusted")

// ErrNoTrustStoreConfigured is returned when enforce mode is active but
// no trusted public keys are available. The kernel treats this as a
// refusal — if we cannot verify, we cannot trust.
var ErrNoTrustStoreConfigured = errors.New("safetykernel: enforce mode requires trusted public keys")

// verifyBundleSignature decides whether a bundle should be loaded based
// on its signature (or lack thereof) and the current strict mode.
//
// Return contract:
//   - nil error, accept=true: load this bundle
//   - nil error, accept=false: skip this bundle (kept for future use; not
//     currently returned because enforce refuses the whole load instead)
//   - non-nil error: caller must refuse the entire load
//
// The mode/trust store arguments are parameters rather than env reads so
// callers can snapshot them once per reload and feed consistent values
// to every bundle.
func verifyBundleSignature(bundleID string, content []byte, rawSig any, mode policysign.Mode, store *policysign.TrustStore) error {
	if mode.SkipsVerification() {
		return nil
	}
	sig, present := extractBundleSignature(rawSig)
	if !present {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: policy bundle rejected (unsigned, enforce mode)",
				"bundle_id", bundleID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "unsigned",
			)
			return fmt.Errorf("%w: %s", ErrBundleUnsigned, bundleID)
		}
		slog.Warn("safety-kernel: policy bundle is unsigned",
			"bundle_id", bundleID,
			"mode", mode.String(),
			"audit_event", "policy_signature_missing",
		)
		return nil
	}
	// A signature map with missing algorithm or value is stronger
	// evidence of tampering than an absent signature — refuse loudly.
	if strings.TrimSpace(sig.Algorithm) == "" || strings.TrimSpace(sig.Value) == "" {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: policy bundle rejected (malformed signature)",
				"bundle_id", bundleID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "malformed",
			)
			return fmt.Errorf("%w: %s", ErrBundleSignatureMalformed, bundleID)
		}
		slog.Warn("safety-kernel: policy bundle signature malformed", "bundle_id", bundleID)
		return nil
	}
	if store == nil || store.Len() == 0 {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: policy bundle rejected (no trust store)",
				"bundle_id", bundleID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "no_trust_store",
			)
			return fmt.Errorf("%w: %s", ErrNoTrustStoreConfigured, bundleID)
		}
		slog.Warn("safety-kernel: trust store empty; cannot verify bundle",
			"bundle_id", bundleID,
			"mode", mode.String(),
		)
		return nil
	}
	pub, known := store.Lookup(sig.KeyID)
	if !known {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: policy bundle rejected (untrusted key_id)",
				"bundle_id", bundleID,
				"key_id", sig.KeyID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "untrusted_key",
			)
			return fmt.Errorf("%w: %s (key_id=%s)", ErrUntrustedKeyID, bundleID, sig.KeyID)
		}
		slog.Warn("safety-kernel: policy bundle signed by untrusted key",
			"bundle_id", bundleID,
			"key_id", sig.KeyID,
			"mode", mode.String(),
		)
		return nil
	}
	if err := policysign.Verify(pub, content, sig); err != nil {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: policy bundle rejected (signature invalid)",
				"bundle_id", bundleID,
				"key_id", sig.KeyID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "invalid_signature",
				"err", err,
			)
			return fmt.Errorf("bundle %s: %w", bundleID, err)
		}
		slog.Warn("safety-kernel: policy bundle signature verification failed",
			"bundle_id", bundleID,
			"key_id", sig.KeyID,
			"mode", mode.String(),
			"err", err,
		)
		return nil
	}
	slog.Debug("safety-kernel: policy bundle signature verified",
		"bundle_id", bundleID,
		"key_id", sig.KeyID,
	)
	return nil
}

// extractBundleSignature reads a _signature map off a fragment value if
// present. The second return value distinguishes "not signed" (false)
// from "signed but malformed" (true with a zero Signature).
func extractBundleSignature(raw any) (policysign.Signature, bool) {
	if raw == nil {
		return policysign.Signature{}, false
	}
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return policysign.Signature{}, false
	}
	sig := policysign.Signature{
		Algorithm: stringField(m["algorithm"]),
		KeyID:     stringField(m["key_id"]),
		Value:     stringField(m["value"]),
		Hash:      stringField(m["hash"]),
	}
	switch v := m["signed_bytes"].(type) {
	case int:
		sig.SignedBytes = v
	case int64:
		sig.SignedBytes = int(v)
	case float64:
		sig.SignedBytes = int(v)
	}
	return sig, true
}

// fragmentSignature returns the _signature sibling of a fragment map,
// or nil if the fragment is a bare string (legacy, unsigned).
func fragmentSignature(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return m[policySignatureFieldKey]
}

// bundleVerifier wraps the immutable verification parameters for a
// single reload cycle, so the loader does not re-read env between
// bundles.
type bundleVerifier struct {
	mode  policysign.Mode
	store *policysign.TrustStore
}

var (
	modeWarnOnceOnErr sync.Once
)

// newBundleVerifier snapshots the policy-signing config from the
// environment once per reload.
func newBundleVerifier() *bundleVerifier {
	mode, err := policysign.ModeFromEnv()
	if err != nil {
		modeWarnOnceOnErr.Do(func() {
			slog.Warn("safety-kernel: CORDUM_POLICY_STRICT is not a recognised value; defaulting to warn",
				"err", err,
			)
		})
		if env.IsProduction() {
			mode = policysign.ModeEnforce
		}
	}
	store, err := policysign.LoadTrustStoreFromEnv()
	if err != nil {
		slog.Error("safety-kernel: trust store load failed", "err", err)
		// Continue with an empty store — we want the Verify path to emit
		// structured rejections rather than a boot-time crash. Boot-time
		// enforcement of "enforce + empty store" lives in cmd/cordum-safety-kernel.
		store = policysign.NewTrustStore()
	}
	return &bundleVerifier{mode: mode, store: store}
}

func stringField(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
