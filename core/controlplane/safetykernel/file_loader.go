package safetykernel

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/policysign"
)

// envLegacyRequireSig preserves compatibility with the first-generation
// "on/off" signature flag so existing deployments do not need to switch
// env vars in the same release that ships strict-mode.
const envLegacyRequireSig = "SAFETY_POLICY_SIGNATURE_REQUIRED"

// verifyFilePolicySignature verifies a file-based policy (SAFETY_POLICY_PATH
// or SAFETY_POLICY_URL) using the new .sig sidecar format while staying
// backward-compatible with the legacy raw-bytes format.
//
// Signature sidecar formats, in order of preference:
//  1. JSON document matching policysign.Signature (written by cordumctl
//     policy sign).
//  2. Legacy: raw 64-byte ed25519 signature encoded as base64 or hex.
//
// Strict-mode semantics:
//   - off: skip verification entirely.
//   - warn: verify and log structured failures, but return nil so the
//     kernel keeps loading. Default.
//   - enforce: return an error on any verification failure so the
//     previous known-good policy stays active.
//
// data is the raw policy bytes (YAML). source is the env-configured
// path/URL, used to locate the sibling .sig file when neither
// SAFETY_POLICY_SIGNATURE nor SAFETY_POLICY_SIGNATURE_PATH is set.
func verifyFilePolicySignature(data []byte, source string) error {
	mode := resolveFileLoaderMode()
	if mode.SkipsVerification() {
		return nil
	}
	store, err := policysign.LoadTrustStoreFromEnv()
	if err != nil {
		slog.Error("safety-kernel: trust store load failed", "err", err)
		if mode.RejectsOnFailure() {
			return fmt.Errorf("%w (trust store load: %v)", ErrNoTrustStoreConfigured, err)
		}
		store = policysign.NewTrustStore()
	}
	if store == nil || store.Len() == 0 {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: file policy rejected (no trust store)",
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "no_trust_store",
			)
			return ErrNoTrustStoreConfigured
		}
		slog.Warn("safety-kernel: file policy trust store empty; skipping verify",
			"mode", mode.String(),
		)
		return nil
	}
	sig, rawLegacy, err := readFilePolicySignature(source)
	if err != nil {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: file policy rejected (no signature)",
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "unsigned",
				"err", err,
			)
			return fmt.Errorf("%w: %v", ErrBundleUnsigned, err)
		}
		slog.Warn("safety-kernel: file policy unsigned", "mode", mode.String(), "err", err)
		return nil
	}
	if sig != nil {
		return verifyFileSigStructured(data, *sig, mode, store)
	}
	return verifyFileSigLegacy(data, rawLegacy, mode, store)
}

// resolveFileLoaderMode reads CORDUM_POLICY_STRICT and maps the legacy
// SAFETY_POLICY_SIGNATURE_REQUIRED flag onto the new modes.
// Production without any explicit mode upgrades to enforce so we do not
// regress operators who previously relied on the env.IsProduction()
// default-to-required behaviour.
func resolveFileLoaderMode() policysign.Mode {
	if raw := strings.TrimSpace(os.Getenv(policysign.EnvStrictMode)); raw != "" {
		mode, err := policysign.ParseMode(raw)
		if err != nil {
			fallback := policysign.ModeWarn
			if env.IsProduction() {
				fallback = policysign.ModeEnforce
			}
			slog.Warn("safety-kernel: CORDUM_POLICY_STRICT invalid; falling back", "err", err, "mode", fallback.String())
			return fallback
		}
		return mode
	}
	if env.Bool(envLegacyRequireSig) {
		return policysign.ModeEnforce
	}
	if env.IsProduction() {
		return policysign.ModeEnforce
	}
	return policysign.ModeWarn
}

// readFilePolicySignature returns either a decoded policysign.Signature
// (new format) or the raw legacy signature bytes. At most one of the
// two return values is populated.
func readFilePolicySignature(source string) (*policysign.Signature, []byte, error) {
	raw, err := readRawSignatureMaterial(source)
	if err != nil {
		return nil, nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var sig policysign.Signature
		if err := json.Unmarshal([]byte(trimmed), &sig); err != nil {
			return nil, nil, fmt.Errorf("invalid signature json: %w", err)
		}
		return &sig, nil, nil
	}
	// Legacy: raw 64 bytes base64/hex encoded. Return as-is; callers will
	// hand off to decodeLegacySignature for further decoding.
	return nil, raw, nil
}

// readRawSignatureMaterial resolves the signature source path/env and
// returns the raw bytes on disk / in env.
func readRawSignatureMaterial(source string) ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("SAFETY_POLICY_SIGNATURE")); raw != "" {
		return []byte(raw), nil
	}
	if path := strings.TrimSpace(os.Getenv("SAFETY_POLICY_SIGNATURE_PATH")); path != "" {
		return os.ReadFile(path) // #nosec -- operator-configured path.
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return nil, errors.New("no sidecar signature for URL-backed policy")
	}
	if source == "" {
		return nil, errors.New("no policy source to resolve .sig from")
	}
	sigPath := source + ".sig"
	if _, err := os.Stat(sigPath); err == nil { // #nosec -- derived from operator-configured path.
		return os.ReadFile(sigPath) // #nosec -- derived from operator-configured path.
	}
	return nil, errors.New("signature file not found")
}

// verifyFileSigStructured handles the new JSON-encoded signature path,
// reusing the same policysign.Verify primitive as Redis-backed bundles
// for byte-for-byte identical semantics.
func verifyFileSigStructured(data []byte, sig policysign.Signature, mode policysign.Mode, store *policysign.TrustStore) error {
	pub, known := store.Lookup(sig.KeyID)
	if !known {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: file policy rejected (untrusted key)",
				"key_id", sig.KeyID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "untrusted_key",
			)
			return fmt.Errorf("%w (key_id=%s)", ErrUntrustedKeyID, sig.KeyID)
		}
		slog.Warn("safety-kernel: file policy signed by untrusted key", "key_id", sig.KeyID)
		return nil
	}
	if err := policysign.Verify(pub, data, sig); err != nil {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: file policy rejected (signature invalid)",
				"key_id", sig.KeyID,
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "invalid_signature",
				"err", err,
			)
			return err
		}
		slog.Warn("safety-kernel: file policy signature invalid", "key_id", sig.KeyID, "err", err)
		return nil
	}
	slog.Info("safety-kernel: file policy signature verified", "key_id", sig.KeyID)
	return nil
}

// verifyFileSigLegacy maintains compatibility with the previous raw-bytes
// signature format. Every trusted public key is tried in turn until one
// verifies — this matches the spirit of the old SAFETY_POLICY_PUBLIC_KEY
// single-key flow, while allowing in-place key rotation.
func verifyFileSigLegacy(data, rawSig []byte, mode policysign.Mode, store *policysign.TrustStore) error {
	decoded, err := policysign.DecodeRawSignature(rawSig)
	if err != nil {
		if mode.RejectsOnFailure() {
			slog.Error("safety-kernel: file policy rejected (legacy signature unreadable)",
				"mode", mode.String(),
				"audit_event", "policy_signature_rejected",
				"reason", "malformed",
				"err", err,
			)
			return fmt.Errorf("%w: %v", ErrBundleSignatureMalformed, err)
		}
		slog.Warn("safety-kernel: file policy legacy signature unreadable", "err", err)
		return nil
	}
	if policysign.VerifyRawAny(store, data, decoded) {
		slog.Info("safety-kernel: file policy legacy signature verified")
		return nil
	}
	if mode.RejectsOnFailure() {
		slog.Error("safety-kernel: file policy rejected (legacy signature invalid)",
			"mode", mode.String(),
			"audit_event", "policy_signature_rejected",
			"reason", "invalid_signature",
		)
		return errors.New("legacy policy signature verification failed")
	}
	slog.Warn("safety-kernel: file policy legacy signature did not verify under any trusted key")
	return nil
}
