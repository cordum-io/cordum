package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	return pub, priv
}

func testManifest() Manifest {
	return Manifest{
		Version:     ManifestVersion,
		PackID:      "test-pack",
		PackVersion: "0.0.1",
		SignedAt:    "2026-04-20T12:00:00Z",
		Algorithm:   AlgorithmEd25519,
		Files: []FileEntry{
			{Path: "pack.yaml", SHA256: "aa", SizeBytes: 2, Kind: FileKindManifest},
			{Path: "schemas/In.json", SHA256: "bb", SizeBytes: 2, Kind: FileKindSchema},
		},
	}
}

func TestSignManifest_HappyPath(t *testing.T) {
	_, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatalf("SignManifest: %v", err)
	}
	if envelope.Signature.KeyID != "pack-1" {
		t.Fatalf("kid = %q, want pack-1", envelope.Signature.KeyID)
	}
	if envelope.Signature.Domain != SigningDomain {
		t.Fatalf("domain = %q, want %q", envelope.Signature.Domain, SigningDomain)
	}
	if envelope.Signature.Algorithm != AlgorithmEd25519 {
		t.Fatalf("algorithm = %q", envelope.Signature.Algorithm)
	}
	raw, err := base64.StdEncoding.DecodeString(envelope.Signature.Value)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(raw) != ed25519.SignatureSize {
		t.Fatalf("signature is %d bytes, want %d", len(raw), ed25519.SignatureSize)
	}
}

func TestSignManifest_RejectsWrongKeySize(t *testing.T) {
	shortKey := ed25519.PrivateKey(make([]byte, 16))
	_, err := SignManifest(testManifest(), shortKey, "pack-1")
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}
}

func TestSignManifest_RequiresKeyID(t *testing.T) {
	_, priv := testKeypair(t)
	_, err := SignManifest(testManifest(), priv, "   ")
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}
}

func TestSignManifest_RejectsUnsupportedAlgorithm(t *testing.T) {
	_, priv := testKeypair(t)
	m := testManifest()
	m.Algorithm = "rsa"
	_, err := SignManifest(m, priv, "pack-1")
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestSignManifest_FillsMissingFields(t *testing.T) {
	_, priv := testKeypair(t)
	m := testManifest()
	m.Version = 0
	m.Algorithm = ""
	m.SignedAt = ""
	envelope, err := SignManifest(m, priv, "pack-1")
	if err != nil {
		t.Fatalf("SignManifest: %v", err)
	}
	if envelope.Manifest.Version != ManifestVersion {
		t.Fatalf("version not filled: %d", envelope.Manifest.Version)
	}
	if envelope.Manifest.Algorithm != AlgorithmEd25519 {
		t.Fatalf("algorithm not filled: %q", envelope.Manifest.Algorithm)
	}
	if _, err := time.Parse(time.RFC3339, envelope.Manifest.SignedAt); err != nil {
		t.Fatalf("signed_at not filled with RFC3339: %q", envelope.Manifest.SignedAt)
	}
}

func TestSignManifest_RequiresPackIdentity(t *testing.T) {
	_, priv := testKeypair(t)
	m := testManifest()
	m.PackID = ""
	_, err := SignManifest(m, priv, "pack-1")
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("err = %v, want ErrManifestMalformed", err)
	}
}
