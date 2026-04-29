package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordum/cordum/core/packs/signing"
)

// Pack install verification gate (task-9c63baa0 step 2). Called from
// runPackInstall BEFORE any state-changing step so a failed verify
// never rolls back — we simply refuse to start.

const (
	// envPackStrict mirrors the --strict flag. Either knob flips the
	// install into strict mode; setting it to anything truthy ("1",
	// "true", "yes") is sufficient.
	envPackStrict = "CORDUM_PACK_STRICT"
	// defaultPackSigFilename is the primary publisher signature file
	// produced by `cordumctl pack sign` (sibling task-6ced7932).
	defaultPackSigFilename = "pack.yaml.sig"
	// defaultPackSigJSONFilename is the JSON-formatted publisher
	// signature written when the publisher passes --json to pack sign.
	defaultPackSigJSONFilename = "pack.yaml.sig.json"
	// cordumCounterSigFilename is the Cordum counter-signature file —
	// an optional secondary signed manifest produced by Cordum's
	// review workflow. It carries Cordum's kid signing the SAME
	// canonical manifest bytes as the publisher file.
	cordumCounterSigFilename = "pack.yaml.sig.cordum"
)

// PackVerificationResult is the outcome of the install-time verify
// gate. It's what runPackInstall records on the pack record and what
// the gateway's mirror-verification attaches to the install payload.
type PackVerificationResult struct {
	Signed              bool
	PublisherID         string
	KID                 string
	VerifiedAt          time.Time
	HasCordumCounterSig bool
	SignatureAlgorithm  string
	PackSignatureVer    int
	Warnings            []string
}

// PackVerificationOptions drives the install-time gate.
type PackVerificationOptions struct {
	// Strict rejects unsigned packs outright. When false, an unsigned
	// pack prints a warning and install proceeds.
	Strict bool
	// RequireCordumSig demands a valid Cordum counter-signature in
	// addition to the publisher's signature.
	RequireCordumSig bool
	// NoVerify is the explicit opt-out escape hatch. In strict mode it
	// requires Force to actually skip the gate (and always prints a
	// loud warning). In non-strict mode it also skips.
	NoVerify bool
	// Force allows --no-verify to bypass the gate even in strict mode.
	// Without --force, strict mode refuses to honor --no-verify.
	Force bool
	// TrustedKeysDir feeds LoadPackTrustStore directly.
	TrustedKeysDir string
	// ExtraKeyFiles feeds LoadPackTrustStore.ExtraKeyFiles.
	ExtraKeyFiles []string
	// Stderr is where warnings go; production uses os.Stderr, tests
	// can inject a buffer.
	Stderr io.Writer
	// Now lets tests freeze VerifiedAt.
	Now func() time.Time
}

// Typed errors surfaced by the install gate.
var (
	// ErrUnsignedPackInStrict is returned when strict mode is on and
	// the pack has no signature file. The error message mentions
	// CORDUM_PACK_STRICT and --trusted-keys so operators know the
	// knobs.
	ErrUnsignedPackInStrict = errors.New("pack install: refusing to install unsigned pack in strict mode")
	// ErrMissingCordumSignature is returned when --require-cordum-sig
	// is on but the pack does not carry a Cordum counter-signature.
	ErrMissingCordumSignature = errors.New("pack install: Cordum counter-signature required but not present")
	// ErrNoVerifyRequiresForce is returned when --no-verify is set in
	// strict mode without --force. Keeps the escape hatch costly.
	ErrNoVerifyRequiresForce = errors.New("pack install: --no-verify in strict mode requires --force")
	// ErrCordumSigUnavailable is returned when --require-cordum-sig is
	// on but the binary does not embed a Cordum counter-signing key.
	// Surfaces the OSS/distribution gap clearly.
	ErrCordumSigUnavailable = errors.New("pack install: --require-cordum-sig requested but this cordumctl build has no embedded Cordum counter-signing key")
)

// VerifyInstallBundle is the install-time gate. It is pure with
// respect to the pack root — it reads disk, loads the trust store,
// verifies the envelope(s), and returns a decision. It never mutates
// the pack directory and never makes a network call.
func VerifyInstallBundle(bundleDir, packID string, opts PackVerificationOptions) (*PackVerificationResult, error) {
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	// --no-verify escape hatch. In strict mode it demands --force.
	if opts.NoVerify {
		if opts.Strict && !opts.Force {
			return nil, ErrNoVerifyRequiresForce
		}
		_, _ = fmt.Fprintf(opts.Stderr, "WARNING: --no-verify set; pack %q signature will NOT be checked. This is unsafe for production.\n", packID)
		return &PackVerificationResult{
			Signed:             false,
			SignatureAlgorithm: signing.AlgorithmEd25519,
			PackSignatureVer:   signing.ManifestVersion,
			Warnings:           []string{"verify skipped via --no-verify"},
		}, nil
	}

	// Locate the primary signature file.
	sigPath, sigPresent := locatePackSignature(bundleDir)
	if !sigPresent {
		if opts.Strict {
			return nil, fmt.Errorf("%w: pack %q (set CORDUM_PACK_STRICT=false, --strict=false, or sign the pack with `cordumctl pack sign`)", ErrUnsignedPackInStrict, packID)
		}
		_, _ = fmt.Fprintf(opts.Stderr, "WARNING: pack %q is unsigned. Install proceeding in non-strict mode.\n", packID)
		// When --require-cordum-sig is set on an unsigned pack the
		// operator's intent is explicit: they want Cordum-counter-sig
		// proof. Unsigned packs can't carry it, so fail even outside
		// strict mode.
		if opts.RequireCordumSig {
			return nil, fmt.Errorf("%w: pack %q is unsigned", ErrMissingCordumSignature, packID)
		}
		return &PackVerificationResult{
			Signed:             false,
			SignatureAlgorithm: signing.AlgorithmEd25519,
			PackSignatureVer:   signing.ManifestVersion,
			Warnings:           []string{"pack installed unsigned in non-strict mode"},
		}, nil
	}

	// Load the trust store. Strict keyring guard applies here so an
	// empty keyring fails fast with an actionable message.
	trust, err := LoadPackTrustStore(PackTrustStoreOptions{
		TrustedKeysDir: opts.TrustedKeysDir,
		ExtraKeyFiles:  opts.ExtraKeyFiles,
		Strict:         opts.Strict,
	})
	if err != nil {
		return nil, err
	}

	signed, err := decodePackSignatureFile(sigPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", signing.ErrManifestMalformed, err)
	}
	if err := signing.VerifyPack(bundleDir, signed, trust.Keys); err != nil {
		return nil, fmt.Errorf("pack %q signature verification failed: %w", packID, err)
	}

	result := &PackVerificationResult{
		Signed:             true,
		KID:                signed.Signature.KeyID,
		VerifiedAt:         now(),
		SignatureAlgorithm: signing.AlgorithmEd25519,
		PackSignatureVer:   signing.ManifestVersion,
	}
	if publisher, ok := trust.Publishers[signed.Signature.KeyID]; ok {
		result.PublisherID = publisher.PublisherID
	}

	// Cordum counter-signature check — optional file, verified only
	// when requested or present.
	cordumSigPath := filepath.Join(bundleDir, cordumCounterSigFilename)
	if info, statErr := os.Stat(cordumSigPath); statErr == nil && !info.IsDir() {
		cordumSigned, err := decodePackSignatureFile(cordumSigPath)
		if err != nil {
			return nil, fmt.Errorf("decode cordum counter-signature: %w", err)
		}
		if err := verifyCordumCounterSignature(bundleDir, cordumSigned, trust); err != nil {
			return nil, err
		}
		result.HasCordumCounterSig = true
	}

	if opts.RequireCordumSig && !result.HasCordumCounterSig {
		if !trust.HasCordumCounterSigningKey() {
			return nil, ErrCordumSigUnavailable
		}
		return nil, fmt.Errorf("%w: pack %q", ErrMissingCordumSignature, packID)
	}

	return result, nil
}

// locatePackSignature returns the first present sig file and a bool.
// YAML (pack.yaml.sig) takes precedence over the JSON-formatted sibling.
func locatePackSignature(bundleDir string) (string, bool) {
	candidates := []string{
		filepath.Join(bundleDir, defaultPackSigFilename),
		filepath.Join(bundleDir, defaultPackSigJSONFilename),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
	}
	return "", false
}

// decodePackSignatureFile reads a YAML- or JSON-formatted envelope
// from disk. Reuses the signing library's format-auto-detect.
func decodePackSignatureFile(path string) (signing.SignedManifest, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- signature path is selected from fixed filenames in the caller-verified bundle directory.
	if err != nil {
		return signing.SignedManifest{}, fmt.Errorf("read signature file %s: %w", path, err)
	}
	return signing.DecodeEnvelope(raw)
}

// verifyCordumCounterSignature validates the optional Cordum
// counter-signature envelope. The envelope must advertise a kid that
// matches the embedded Cordum counter-signing key; anything else is
// rejected (an attacker can't forge a counter-sig with a different
// kid that happens to live in the operator's trust store).
func verifyCordumCounterSignature(bundleDir string, signed signing.SignedManifest, trust *PackTrustStore) error {
	if trust == nil || !trust.HasCordumCounterSigningKey() {
		return ErrCordumSigUnavailable
	}
	if !strings.EqualFold(strings.TrimSpace(signed.Signature.KeyID), trust.CordumCounterSigningKID) {
		return fmt.Errorf("%w: counter-signature kid %q is not the embedded Cordum kid %q", signing.ErrUnknownKeyID, signed.Signature.KeyID, trust.CordumCounterSigningKID)
	}
	keyring := map[string]ed25519.PublicKey{
		trust.CordumCounterSigningKID: *trust.CordumCounterSigningKey,
	}
	return signing.VerifyPack(bundleDir, signed, keyring)
}

// resolvePackStrict combines the --strict flag and the env var into
// a single effective boolean. The env var only raises strictness; it
// can't lower an explicit --strict=false.
func resolvePackStrict(flagStrict bool) bool {
	if flagStrict {
		return true
	}
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(envPackStrict)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// verificationFromResult converts a verify-gate outcome into the
// on-the-wire packRecord.Verification shape. Returns nil when the
// result is nil so the JSON field is omitted entirely for callers
// that skipped the gate.
func verificationFromResult(r *PackVerificationResult) *packRecordVerification {
	if r == nil {
		return nil
	}
	out := &packRecordVerification{
		Signed:              r.Signed,
		PublisherID:         r.PublisherID,
		KID:                 r.KID,
		HasCordumCounterSig: r.HasCordumCounterSig,
		SignatureAlgorithm:  r.SignatureAlgorithm,
		PackSignatureVer:    r.PackSignatureVer,
		Warnings:            append([]string(nil), r.Warnings...),
	}
	if !r.VerifiedAt.IsZero() {
		out.VerifiedAt = r.VerifiedAt.UTC().Format(time.RFC3339)
	}
	return out
}
