// Package sign implements release-time binary integrity verification for
// Cordum desktop artefacts (cordum-hook, cordum-agentd, cordum-claude).
// It validates a detached OpenPGP signature over a cosign-compatible
// SHA256SUMS manifest and re-hashes individual binaries before activation.
//
// See docs/security/binary-signing.md for the threat model.
package sign

import "errors"

var (
	// ErrUnsignedManifest is returned when the caller supplies a manifest
	// without an accompanying detached signature.
	ErrUnsignedManifest = errors.New("sign: unsigned manifest")
	// ErrPubkeyNotInTrustSet is returned when the manifest signer is not
	// listed in the verifier's trusted-fingerprint set.
	ErrPubkeyNotInTrustSet = errors.New("sign: signer pubkey not in trust set")
	// ErrSignatureMismatch is returned when the detached signature fails
	// cryptographic verification against the manifest bytes.
	ErrSignatureMismatch = errors.New("sign: signature verification failed")
	// ErrHashMismatch is returned when a binary's SHA-256 differs from
	// the expected value recorded in the manifest.
	ErrHashMismatch = errors.New("sign: binary hash mismatch")
	// ErrBinaryNotFound is returned when the binary at the requested
	// path does not exist or is inaccessible.
	ErrBinaryNotFound = errors.New("sign: binary not found")
	// ErrFingerprintMismatch is returned when the manifest is signed by
	// a trusted key but that key's fingerprint differs from the value
	// pinned at build time via PinnedReleaseFingerprint.
	ErrFingerprintMismatch = errors.New("sign: pinned fingerprint mismatch")
	// ErrPathTraversal is returned when a manifest entry references an
	// absolute path, a parent-traversal segment, or a Windows drive root.
	ErrPathTraversal = errors.New("sign: manifest contains path traversal or absolute path")
	// ErrDowngradeAttempt is returned by VerifyVersionFloor when the
	// candidate binary version is strictly older than the persisted floor
	// and no operator-override has been authorised. EDGE-151-DOWNGRADE.
	ErrDowngradeAttempt = errors.New("sign: downgrade attempt: candidate version below floor")
	// ErrFloorAdvanceFailed is returned by AdvanceFloor when the
	// version-floor file could not be written atomically.
	ErrFloorAdvanceFailed = errors.New("sign: floor advance failed")
	// ErrInvalidVersion is returned by VerifyVersionFloor when either
	// argument is non-empty but cannot be parsed as a semver-2.0 string.
	ErrInvalidVersion = errors.New("sign: invalid version")
	// ErrNoVersionEmbedded is returned by ParseVersion when the manifest
	// carries no leading `# version:` line.
	ErrNoVersionEmbedded = errors.New("sign: no version embedded in manifest")
)
