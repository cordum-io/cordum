package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// SignManifest signs a canonical manifest with the supplied Ed25519
// private key and wraps it in an envelope ready to be written to disk
// as pack.yaml.sig. The key id is an opaque string a verifier uses to
// pick a public key from a keyring; callers usually carry it
// alongside the private key file.
func SignManifest(manifest Manifest, privKey ed25519.PrivateKey, keyID string) (SignedManifest, error) {
	if len(privKey) != ed25519.PrivateKeySize {
		return SignedManifest{}, fmt.Errorf("%w: private key must be %d bytes", ErrInvalidKey, ed25519.PrivateKeySize)
	}
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return SignedManifest{}, fmt.Errorf("%w: key id required", ErrInvalidKey)
	}
	if manifest.Version == 0 {
		manifest.Version = ManifestVersion
	}
	if strings.TrimSpace(manifest.Algorithm) == "" {
		manifest.Algorithm = AlgorithmEd25519
	}
	if manifest.Algorithm != AlgorithmEd25519 {
		return SignedManifest{}, fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, manifest.Algorithm)
	}
	if strings.TrimSpace(manifest.SignedAt) == "" {
		manifest.SignedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(manifest.PackID) == "" || strings.TrimSpace(manifest.PackVersion) == "" {
		return SignedManifest{}, fmt.Errorf("%w: pack id + version required", ErrManifestMalformed)
	}

	preimage, err := CanonicalBytes(manifest)
	if err != nil {
		return SignedManifest{}, err
	}
	sig := ed25519.Sign(privKey, preimage)
	return SignedManifest{
		APIVersion: EnvelopeAPIVersion,
		Kind:       EnvelopeKind,
		Metadata: Metadata{
			PackID:      manifest.PackID,
			PackVersion: manifest.PackVersion,
			SignedAt:    manifest.SignedAt,
		},
		Signature: Signature{
			KeyID:     keyID,
			Algorithm: AlgorithmEd25519,
			Value:     base64.StdEncoding.EncodeToString(sig),
			Domain:    SigningDomain,
		},
		Manifest: manifest,
	}, nil
}
