// Package policysign provides Ed25519 signing and verification for Cordum
// policy bundles. It is a distinct trust domain from core/licensing, so
// keys are loaded independently, but the underlying algorithm primitives
// are shared.
//
// A Signature describes the cryptographic binding between a payload
// (canonically, the exact bytes stored in Redis or on disk) and a
// particular signing key. The Hash field is the SHA-256 of those same
// bytes and is purely informational — Verify recomputes it.
package policysign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Algorithm identifiers.
const (
	AlgorithmEd25519 = "ed25519"
)

// Errors returned by Sign and Verify. Callers should compare with
// errors.Is to distinguish semantic categories.
var (
	ErrEmptyPayload      = errors.New("policysign: empty payload")
	ErrEmptyKeyID        = errors.New("policysign: empty key id")
	ErrInvalidPrivateKey = errors.New("policysign: invalid private key")
	ErrInvalidPublicKey  = errors.New("policysign: invalid public key")
	ErrInvalidSignature  = errors.New("policysign: invalid signature")
	ErrUnsupportedAlgo   = errors.New("policysign: unsupported algorithm")
	ErrHashMismatch      = errors.New("policysign: signature hash does not match payload")
	ErrVerifyFailed      = errors.New("policysign: signature verification failed")
)

// Signature is the on-wire representation of a policy signature. It is
// persisted alongside the bundle (e.g. as a map entry `_signature`) and
// carries everything the verifier needs apart from the trusted public
// key.
type Signature struct {
	Algorithm   string `json:"algorithm"`
	KeyID       string `json:"key_id"`
	Value       string `json:"value"`
	Hash        string `json:"hash"`
	SignedBytes int    `json:"signed_bytes"`
}

// Sign computes an Ed25519 signature over payload using key. keyID is an
// opaque identifier the verifier uses to select a trusted public key.
// The returned Signature embeds a hex-encoded SHA-256 of the payload
// for fast bundle-integrity checks.
func Sign(key ed25519.PrivateKey, keyID string, payload []byte) (Signature, error) {
	if len(payload) == 0 {
		return Signature{}, ErrEmptyPayload
	}
	if strings.TrimSpace(keyID) == "" {
		return Signature{}, ErrEmptyKeyID
	}
	if len(key) != ed25519.PrivateKeySize {
		return Signature{}, ErrInvalidPrivateKey
	}
	sum := sha256.Sum256(payload)
	raw := ed25519.Sign(key, payload)
	return Signature{
		Algorithm:   AlgorithmEd25519,
		KeyID:       strings.TrimSpace(keyID),
		Value:       base64.StdEncoding.EncodeToString(raw),
		Hash:        hex.EncodeToString(sum[:]),
		SignedBytes: len(payload),
	}, nil
}

// Verify validates sig against payload using pub. It rejects unknown
// algorithms, malformed fields, payload/hash mismatches, and invalid
// Ed25519 signatures. A nil or zero Signature is treated as invalid.
func Verify(pub ed25519.PublicKey, payload []byte, sig Signature) error {
	if len(payload) == 0 {
		return ErrEmptyPayload
	}
	if len(pub) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	if strings.TrimSpace(sig.Algorithm) == "" || !strings.EqualFold(sig.Algorithm, AlgorithmEd25519) {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgo, sig.Algorithm)
	}
	if strings.TrimSpace(sig.Value) == "" {
		return ErrInvalidSignature
	}
	raw, err := decodeSignatureValue(sig.Value)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%w: wrong length %d", ErrInvalidSignature, len(raw))
	}
	// Require sig.SignedBytes to match len(payload). Sign always writes
	// it, so a missing or zero value is either a replay of a stale
	// signature or a tampered payload — reject both rather than trust
	// the downstream hash check in isolation.
	if sig.SignedBytes != len(payload) {
		return fmt.Errorf("%w: signed_bytes=%d actual=%d", ErrHashMismatch, sig.SignedBytes, len(payload))
	}
	if h := strings.TrimSpace(sig.Hash); h != "" {
		sum := sha256.Sum256(payload)
		if !strings.EqualFold(h, hex.EncodeToString(sum[:])) {
			return ErrHashMismatch
		}
	}
	if !ed25519.Verify(pub, payload, raw) {
		return ErrVerifyFailed
	}
	return nil
}

// HashPayload returns the hex-encoded SHA-256 of payload. Exposed for
// callers that need to reference the hash before signing (e.g. logs).
func HashPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// IsZero reports whether sig carries no signature material. Useful for
// distinguishing "unsigned bundle" from "verification failed".
func (s Signature) IsZero() bool {
	return strings.TrimSpace(s.Algorithm) == "" &&
		strings.TrimSpace(s.Value) == "" &&
		strings.TrimSpace(s.KeyID) == ""
}

func decodeSignatureValue(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "ed25519:")
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(raw); err == nil {
		return data, nil
	}
	if data, err := hex.DecodeString(raw); err == nil {
		return data, nil
	}
	return nil, errors.New("signature is not valid base64 or hex")
}

// DecodeRawSignature converts a legacy signature file's raw bytes (which
// may be plain binary, base64, or hex) into the 64-byte ed25519
// signature slice. It exists to preserve backward compatibility with
// the pre-Signature-struct sidecar format.
func DecodeRawSignature(raw []byte) ([]byte, error) {
	if len(raw) == ed25519.SignatureSize {
		return raw, nil
	}
	return decodeSignatureValue(string(raw))
}

// VerifyRawAny reports whether a raw ed25519 signature verifies under
// any of the trust store's public keys. It is used for legacy
// signature sidecars that lack a key_id; consumers with a key_id must
// use Verify directly.
func VerifyRawAny(store *TrustStore, payload, rawSig []byte) bool {
	if store == nil || len(rawSig) != ed25519.SignatureSize {
		return false
	}
	for _, id := range store.IDs() {
		if pub, ok := store.Lookup(id); ok && ed25519.Verify(pub, payload, rawSig) {
			return true
		}
	}
	return false
}
