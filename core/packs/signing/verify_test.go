package signing

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifyManifest_HappyPath(t *testing.T) {
	pub, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub}); err != nil {
		t.Fatalf("VerifyManifest: %v", err)
	}
}

func TestVerifyManifest_UnknownKeyID(t *testing.T) {
	_, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Keyring advertises a different kid.
	other, _, _ := ed25519.GenerateKey(nil)
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"different": other})
	if !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("err = %v, want ErrUnknownKeyID", err)
	}
}

func TestVerifyManifest_BadSignature(t *testing.T) {
	pub, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the signature value by flipping the middle byte.
	raw, _ := base64.StdEncoding.DecodeString(envelope.Signature.Value)
	raw[len(raw)/2] ^= 0x01
	envelope.Signature.Value = base64.StdEncoding.EncodeToString(raw)
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyManifest_TamperedManifestBody(t *testing.T) {
	pub, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte in the signed manifest — signature must fail.
	envelope.Manifest.Files[0].SHA256 = "cc"
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyManifest_DomainMismatch(t *testing.T) {
	pub, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature.Domain = "cordum.delegation.v1"
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrDomainMismatch) {
		t.Fatalf("err = %v, want ErrDomainMismatch", err)
	}
}

func TestVerifyManifest_UnsupportedAlgorithm(t *testing.T) {
	pub, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature.Algorithm = "rsa"
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestVerifyManifest_MissingEnvelopeKind(t *testing.T) {
	pub, priv := testKeypair(t)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Kind = ""
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("err = %v, want ErrManifestMalformed", err)
	}
}

func TestVerifyPack_EndToEnd(t *testing.T) {
	pub, priv := testKeypair(t)
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	env, err := SignManifest(m, priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPack(root, env, map[string]ed25519.PublicKey{"pack-1": pub}); err != nil {
		t.Fatalf("VerifyPack happy: %v", err)
	}
}

func TestVerifyPack_TamperedSchemaFails(t *testing.T) {
	pub, priv := testKeypair(t)
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	env, err := SignManifest(m, priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Flip one byte in a signed schema on disk.
	victim := filepath.Join(root, "schemas", "In.json")
	if err := os.WriteFile(victim, []byte(`{"type":"number"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = VerifyPack(root, env, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
}

func TestVerifyPack_NewUncoveredFileFails(t *testing.T) {
	pub, priv := testKeypair(t)
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	env, err := SignManifest(m, priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Add a new referenced file to pack.yaml AFTER signing.
	newSchema := filepath.Join(root, "schemas", "Extra.json")
	if err := os.WriteFile(newSchema, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packYAML := `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.1
resources:
  schemas:
    - id: test/In
      path: schemas/In.json
    - id: test/Out
      path: schemas/Out.json
    - id: test/Extra
      path: schemas/Extra.json
  workflows:
    - id: test.echo
      path: workflows/echo.yaml
overlays:
  config:
    - name: pools
      path: overlays/pools.patch.yaml
  policy:
    - name: safety
      path: overlays/policy.yaml
`
	if err := os.WriteFile(filepath.Join(root, "pack.yaml"), []byte(packYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	err = VerifyPack(root, env, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
}

func TestVerifyPack_IdentityDrift(t *testing.T) {
	pub, priv := testKeypair(t)
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	env, err := SignManifest(m, priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite pack.yaml to bump version — envelope was signed for
	// 0.0.1, disk now says 0.0.2.
	packYAML := `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.2
resources:
  schemas:
    - id: test/In
      path: schemas/In.json
    - id: test/Out
      path: schemas/Out.json
  workflows:
    - id: test.echo
      path: workflows/echo.yaml
overlays:
  config:
    - name: pools
      path: overlays/pools.patch.yaml
  policy:
    - name: safety
      path: overlays/policy.yaml
`
	if err := os.WriteFile(filepath.Join(root, "pack.yaml"), []byte(packYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	err = VerifyPack(root, env, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
}

func TestVerifyPack_MultiKidRotation(t *testing.T) {
	pub1, priv1 := testKeypair(t)
	pub2, _ := testKeypair(t)
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	env, err := SignManifest(m, priv1, "pack-v1")
	if err != nil {
		t.Fatal(err)
	}
	// Keyring has BOTH old and new publisher keys — verifier picks
	// the right one by kid.
	keyring := map[string]ed25519.PublicKey{"pack-v1": pub1, "pack-v2": pub2}
	if err := VerifyPack(root, env, keyring); err != nil {
		t.Fatalf("VerifyPack with multi-kid: %v", err)
	}
}
