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

// Edge-case tests that push the package over the ≥85% coverage bar
// required by task-6ced7932 step 5. Each test targets a specific
// branch uncovered by the happy-path + tamper tests.

func TestValidateSignatureEnvelope_EmptyKeyID(t *testing.T) {
	err := validateSignatureEnvelope(Signature{
		KeyID:     "",
		Algorithm: AlgorithmEd25519,
		Value:     "x",
		Domain:    SigningDomain,
	})
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("err = %v, want ErrManifestMalformed", err)
	}
}

func TestValidateSignatureEnvelope_EmptySignatureValue(t *testing.T) {
	err := validateSignatureEnvelope(Signature{
		KeyID:     "pack-1",
		Algorithm: AlgorithmEd25519,
		Value:     "",
		Domain:    SigningDomain,
	})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyManifest_BadBase64(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature.Value = "!!not-base64!!"
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyManifest_WrongSignatureLength(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature.Value = base64.StdEncoding.EncodeToString([]byte("short"))
	err = VerifyManifest(envelope, map[string]ed25519.PublicKey{"pack-1": pub})
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyManifest_PublicKeyWrongSize(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	envelope, err := SignManifest(testManifest(), priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	keyring := map[string]ed25519.PublicKey{"pack-1": ed25519.PublicKey(make([]byte, 16))}
	err = VerifyManifest(envelope, keyring)
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("err = %v, want ErrInvalidKey", err)
	}
}

func TestVerifyPack_SignedFileMissingOnDisk(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	root := writePack(t)
	m, err := BuildManifestWithClock(root, time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	env, err := SignManifest(m, priv, "pack-1")
	if err != nil {
		t.Fatal(err)
	}
	// Delete a signed file on disk AND remove its reference from
	// pack.yaml so the rebuild doesn't fail with ErrMissingFile first.
	if err := os.Remove(filepath.Join(root, "schemas", "In.json")); err != nil {
		t.Fatal(err)
	}
	packYAML := `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: test-pack
  version: 0.0.1
resources:
  schemas:
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
	// Either ErrHashMismatch (pack.yaml itself differs now) or
	// ErrMissingFile — both are correct reasons to fail. We just
	// require the verifier not to claim success.
	if err == nil {
		t.Fatal("expected verification failure")
	}
}

func TestBuildManifest_NonExistentRoot(t *testing.T) {
	_, err := BuildManifest("/definitely/does/not/exist/pack-signing")
	if !errors.Is(err, ErrPackRootNotDirectory) {
		t.Fatalf("err = %v, want ErrPackRootNotDirectory", err)
	}
}

func TestDedupeFiles_NoOp(t *testing.T) {
	in := []FileEntry{{Path: "a"}}
	out := dedupeFiles(in)
	if len(out) != 1 || out[0].Path != "a" {
		t.Fatalf("dedupe single = %+v", out)
	}
	// Duplicate on path removes the second copy.
	in = []FileEntry{{Path: "a", SHA256: "1"}, {Path: "a", SHA256: "2"}}
	out = dedupeFiles(in)
	if len(out) != 1 || out[0].SHA256 != "1" {
		t.Fatalf("dedupe duplicate = %+v", out)
	}
}
