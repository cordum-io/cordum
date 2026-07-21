package workercredentials

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// ProofAlgorithmECDSAP256SHA256 is the only worker trust proof algorithm.
	ProofAlgorithmECDSAP256SHA256 = "ECDSA_P256_SHA256"
	maxProofKeyIDBytes            = 128
	maxProofPublicKeyPEMBytes     = 4096
)

var (
	ErrFreshProofKeyRequired = errors.New("fresh proof key required when reactivating revoked credential")
	proofKeyIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

func (c Credential) HasProofKey() bool {
	return strings.TrimSpace(c.ProofKeyID) != "" &&
		strings.TrimSpace(c.ProofAlgorithm) != "" &&
		strings.TrimSpace(c.ProofPublicKeyPEM) != ""
}

func (s *Service) ResolveProofKey(ctx context.Context, workerID, keyID string) (*ecdsa.PublicKey, bool, error) {
	record, err := s.GetByWorkerID(ctx, workerID)
	if err != nil || record == nil {
		return nil, false, err
	}
	return record.ResolveProofKey(keyID)
}

// ResolveProofKey parses a key from this exact credential snapshot. Callers
// that already fetched authority must use this method to avoid a second-read
// rotation race that could mix credential metadata with newer key material.
func (c Credential) ResolveProofKey(keyID string) (*ecdsa.PublicKey, bool, error) {
	if c.Revoked() {
		return nil, true, nil
	}
	if !c.HasProofKey() || strings.TrimSpace(keyID) != c.ProofKeyID {
		return nil, false, nil
	}
	publicKey, err := parseProofPublicKey(c.ProofPublicKeyPEM)
	if err != nil {
		return nil, false, fmt.Errorf("decode stored worker proof key: %w", err)
	}
	return publicKey, false, nil
}

func validateAndNormalizeProofKey(record Credential) (Credential, error) {
	provided := 0
	for _, value := range []string{record.ProofKeyID, record.ProofAlgorithm, record.ProofPublicKeyPEM} {
		if value != "" {
			provided++
		}
	}
	if provided == 0 {
		return record, nil
	}
	if provided != 3 {
		return Credential{}, fmt.Errorf("proof key fields must be provided together")
	}
	if err := validateProofKeyID(record.ProofKeyID); err != nil {
		return Credential{}, err
	}
	if record.ProofAlgorithm != ProofAlgorithmECDSAP256SHA256 {
		return Credential{}, fmt.Errorf("proof algorithm must be %s", ProofAlgorithmECDSAP256SHA256)
	}
	if len([]byte(record.ProofPublicKeyPEM)) > maxProofPublicKeyPEMBytes {
		return Credential{}, fmt.Errorf("proof public key PEM too large")
	}
	publicKey, err := parseProofPublicKey(record.ProofPublicKeyPEM)
	if err != nil {
		return Credential{}, err
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return Credential{}, fmt.Errorf("marshal proof public key: %w", err)
	}
	record.ProofPublicKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	return record, nil
}

func validateProofKeyID(keyID string) error {
	if len(keyID) == 0 || len([]byte(keyID)) > maxProofKeyIDBytes || !proofKeyIDPattern.MatchString(keyID) {
		return fmt.Errorf("proof key id must be 1-%d characters using letters, digits, '.', '_', ':', or '-'", maxProofKeyIDBytes)
	}
	return nil
}

func parseProofPublicKey(publicPEM string) (*ecdsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(publicPEM))
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("proof public key must be SPKI PEM")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, fmt.Errorf("proof public key PEM contains trailing data")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse proof public key SPKI PEM: %w", err)
	}
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("proof public key must be ECDSA")
	}
	// publicKey.X/Y are read-only here: a nil guard before ECDH(), which
	// panics (rather than erroring) on zero-value coordinates. Bytes()/ECDH()
	// give no nil-safe alternative to guard with.
	if publicKey.Curve != elliptic.P256() || publicKey.X == nil || publicKey.Y == nil { //nolint:staticcheck // SA1019: nil guard, see comment above
		return nil, fmt.Errorf("proof public key must use P-256")
	}
	if _, err := publicKey.ECDH(); err != nil {
		return nil, fmt.Errorf("proof public key must use P-256")
	}
	return publicKey, nil
}

func copyProofAuthority(target, source Credential) Credential {
	target.ProofKeyID = source.ProofKeyID
	target.ProofAlgorithm = source.ProofAlgorithm
	target.ProofPublicKeyPEM = source.ProofPublicKeyPEM
	if source.AgentID != "" {
		target.AgentID = source.AgentID
	}
	return target
}

func credentialForWrite(candidate, current Credential, replacing bool) (Credential, error) {
	if !replacing || candidate.HasProofKey() {
		return candidate, nil
	}
	if !current.HasProofKey() {
		return candidate, nil
	}
	if current.Revoked() {
		return Credential{}, ErrFreshProofKeyRequired
	}
	return copyProofAuthority(candidate, current), nil
}
