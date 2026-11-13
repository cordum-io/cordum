package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// VerifyManifest checks the signature on a SignedManifest envelope
// against a multi-kid keyring. It does NOT touch disk — it only
// validates the cryptographic bindings between the envelope's
// manifest and its signature.
//
// Use VerifyPack on top of this to also confirm every FileEntry
// matches the pack as laid out on disk.
func VerifyManifest(signed SignedManifest, keyring map[string]ed25519.PublicKey) error {
	if strings.TrimSpace(signed.APIVersion) == "" || signed.Kind != EnvelopeKind {
		return fmt.Errorf("%w: envelope apiVersion/kind missing", ErrManifestMalformed)
	}
	if err := validateSignatureEnvelope(signed.Signature); err != nil {
		return err
	}
	pub, ok := keyring[signed.Signature.KeyID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKeyID, signed.Signature.KeyID)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key for kid %s is %d bytes (want %d)", ErrInvalidKey, signed.Signature.KeyID, len(pub), ed25519.PublicKeySize)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(signed.Signature.Value)
	if err != nil {
		return fmt.Errorf("%w: signature not base64", ErrBadSignature)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is %d bytes (want %d)", ErrBadSignature, len(sigBytes), ed25519.SignatureSize)
	}
	preimage, err := CanonicalBytes(signed.Manifest)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, preimage, sigBytes) {
		return ErrBadSignature
	}
	return nil
}

// VerifyPack re-walks packRoot, builds a fresh manifest, and asserts
// that every FileEntry.SHA256 matches the signed manifest. This is
// the complete supply-chain check: a tampered schema on disk fails
// here even if the envelope signature verifies against the manifest.
func VerifyPack(packRoot string, signed SignedManifest, keyring map[string]ed25519.PublicKey) error {
	if err := VerifyManifest(signed, keyring); err != nil {
		return err
	}
	signedAt, _ := time.Parse(time.RFC3339, signed.Manifest.SignedAt)
	fresh, err := BuildManifestWithClock(packRoot, signedAt)
	if err != nil {
		return err
	}
	if signed.Manifest.PackID != fresh.PackID || signed.Manifest.PackVersion != fresh.PackVersion {
		return fmt.Errorf("%w: pack identity mismatch (signed=%s@%s, disk=%s@%s)",
			ErrHashMismatch,
			signed.Manifest.PackID, signed.Manifest.PackVersion,
			fresh.PackID, fresh.PackVersion)
	}
	signedByPath := indexFilesByPath(signed.Manifest.Files)
	freshByPath := indexFilesByPath(fresh.Files)
	// Every signed file must exist on disk with the recorded hash.
	for p, want := range signedByPath {
		got, ok := freshByPath[p]
		if !ok {
			return fmt.Errorf("%w: signed file missing on disk: %s", ErrMissingFile, p)
		}
		if got.SHA256 != want.SHA256 {
			return fmt.Errorf("%w: %s", ErrHashMismatch, p)
		}
		if got.SizeBytes != want.SizeBytes {
			return fmt.Errorf("%w: %s (signed size %d, disk size %d)", ErrHashMismatch, p, want.SizeBytes, got.SizeBytes)
		}
		if got.Kind != want.Kind {
			return fmt.Errorf("%w: %s kind drift (signed %s, disk %s)", ErrHashMismatch, p, want.Kind, got.Kind)
		}
	}
	// Every disk file referenced by pack.yaml must be in the signed
	// manifest (catches the case where the publisher added a new
	// referenced file but forgot to re-sign).
	for p := range freshByPath {
		if _, ok := signedByPath[p]; !ok {
			return fmt.Errorf("%w: pack.yaml references %s but signature does not cover it", ErrHashMismatch, p)
		}
	}
	return nil
}

func validateSignatureEnvelope(sig Signature) error {
	if strings.TrimSpace(sig.KeyID) == "" {
		return fmt.Errorf("%w: empty key id", ErrManifestMalformed)
	}
	if sig.Algorithm != AlgorithmEd25519 {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, sig.Algorithm)
	}
	if sig.Domain != SigningDomain {
		return fmt.Errorf("%w: envelope domain %q (want %q)", ErrDomainMismatch, sig.Domain, SigningDomain)
	}
	if strings.TrimSpace(sig.Value) == "" {
		return fmt.Errorf("%w: empty signature value", ErrBadSignature)
	}
	return nil
}

func indexFilesByPath(files []FileEntry) map[string]FileEntry {
	out := make(map[string]FileEntry, len(files))
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}
