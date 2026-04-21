package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/packs/signing"
	"gopkg.in/yaml.v3"
)

// signInstallTestPack writes a minimal pack at a temp dir, generates
// a publisher keypair, runs the existing runPackSign path to produce
// pack.yaml.sig, and returns (packRoot, trustedKeysDir, publisherKID).
func signInstallTestPack(t *testing.T, kid string) (packRoot, trustedDir string) {
	t.Helper()
	packRoot = writeTestPack(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	// Private key file.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "pack.key")
	privRec := packPrivateKeyRecord{
		KeyID:      kid,
		Algorithm:  signing.AlgorithmEd25519,
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}
	privBody, err := yaml.Marshal(privRec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, privBody, 0o600); err != nil {
		t.Fatal(err)
	}
	// Sign the pack through the public runPackSign path so we
	// exercise the same code shape operators would.
	if err := runPackSign([]string{"--key", keyPath, packRoot}); err != nil {
		t.Fatalf("runPackSign: %v", err)
	}
	// Trusted keys dir, 0600 perms so the install gate accepts it on
	// POSIX.
	trustedDir = t.TempDir()
	pubRec := packPublicKeyRecord{
		KeyID:        kid,
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(pub),
	}
	pubBody, err := yaml.Marshal(pubRec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trustedDir, kid+".pub"), pubBody, 0o600); err != nil {
		t.Fatal(err)
	}
	return packRoot, trustedDir
}

func TestVerifyInstallBundle_SignedPackAccepted(t *testing.T) {
	packRoot, trustedDir := signInstallTestPack(t, "pack-test")
	var out bytes.Buffer
	result, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:         true,
		TrustedKeysDir: trustedDir,
		Stderr:         &out,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.Signed {
		t.Errorf("Signed=false")
	}
	if result.KID != "pack-test" {
		t.Errorf("KID=%q, want pack-test", result.KID)
	}
	if result.PublisherID == "" {
		t.Errorf("PublisherID not populated")
	}
	if result.VerifiedAt.IsZero() {
		t.Errorf("VerifiedAt not set")
	}
}

func TestVerifyInstallBundle_UnsignedRejectedInStrict(t *testing.T) {
	packRoot := writeTestPack(t)
	var out bytes.Buffer
	_, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict: true,
		Stderr: &out,
	})
	if !errors.Is(err, ErrUnsignedPackInStrict) {
		t.Fatalf("want ErrUnsignedPackInStrict, got %v", err)
	}
	if !strings.Contains(err.Error(), "toolchain-test") {
		t.Errorf("error should cite pack id: %v", err)
	}
}

func TestVerifyInstallBundle_UnsignedWarnsInNonStrict(t *testing.T) {
	packRoot := writeTestPack(t)
	var out bytes.Buffer
	result, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict: false,
		Stderr: &out,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Signed {
		t.Errorf("Signed should be false")
	}
	if !strings.Contains(out.String(), "WARNING") || !strings.Contains(out.String(), "toolchain-test") {
		t.Errorf("warning banner missing: %q", out.String())
	}
}

func TestVerifyInstallBundle_TamperedRejected(t *testing.T) {
	packRoot, trustedDir := signInstallTestPack(t, "pack-test")
	// Tamper with a signed file.
	if err := os.WriteFile(filepath.Join(packRoot, "schemas", "In.json"), []byte(`{"type":"array"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:         true,
		TrustedKeysDir: trustedDir,
		Stderr:         &out,
	})
	if err == nil {
		t.Fatal("expected tamper rejection")
	}
	if !errors.Is(err, signing.ErrHashMismatch) {
		t.Errorf("want ErrHashMismatch, got %v", err)
	}
}

func TestVerifyInstallBundle_UnknownPublisherKidRejected(t *testing.T) {
	packRoot, _ := signInstallTestPack(t, "pack-test")
	// Keyring holds an UNRELATED trusted key, so the strict-keyring
	// guard passes but the publisher kid is not known to us.
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedDir := t.TempDir()
	body, err := yaml.Marshal(packPublicKeyRecord{
		KeyID:        "other-publisher",
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(otherPub),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelatedDir, "other-publisher.pub"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err = VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:         true,
		TrustedKeysDir: unrelatedDir,
		Stderr:         &out,
	})
	if !errors.Is(err, signing.ErrUnknownKeyID) {
		t.Fatalf("want ErrUnknownKeyID, got %v", err)
	}
}

func TestVerifyInstallBundle_NoVerifyRequiresForceInStrict(t *testing.T) {
	packRoot := writeTestPack(t)
	var out bytes.Buffer
	_, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:   true,
		NoVerify: true,
		Force:    false,
		Stderr:   &out,
	})
	if !errors.Is(err, ErrNoVerifyRequiresForce) {
		t.Fatalf("want ErrNoVerifyRequiresForce, got %v", err)
	}
}

func TestVerifyInstallBundle_NoVerifyForceEmitsWarning(t *testing.T) {
	packRoot := writeTestPack(t)
	var out bytes.Buffer
	result, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:   true,
		NoVerify: true,
		Force:    true,
		Stderr:   &out,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Signed {
		t.Errorf("Signed should be false when --no-verify used")
	}
	if !strings.Contains(out.String(), "WARNING") {
		t.Errorf("no-verify should emit WARNING banner: %q", out.String())
	}
}

func TestVerifyInstallBundle_RequireCordumSigRejectsPublisherOnly(t *testing.T) {
	packRoot, trustedDir := signInstallTestPack(t, "pack-test")

	// Inject a synthetic embedded cordum key so the gate doesn't fail
	// with ErrCordumSigUnavailable instead.
	cordumPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rec := packPublicKeyRecord{
		KeyID:        "cordum-test",
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(cordumPub),
	}
	body, err := yaml.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	saved := embeddedCordumCounterSigningKey
	embeddedCordumCounterSigningKey = body
	defer func() { embeddedCordumCounterSigningKey = saved }()

	var out bytes.Buffer
	_, err = VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:           true,
		RequireCordumSig: true,
		TrustedKeysDir:   trustedDir,
		Stderr:           &out,
	})
	if !errors.Is(err, ErrMissingCordumSignature) {
		t.Fatalf("want ErrMissingCordumSignature, got %v", err)
	}
}

func TestVerifyInstallBundle_RequireCordumSigPassesWithCounterSig(t *testing.T) {
	packRoot, trustedDir := signInstallTestPack(t, "pack-test")

	// Produce a second envelope for the Cordum counter-signature.
	cordumPub, cordumPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rec := packPublicKeyRecord{
		KeyID:        "cordum-test",
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(cordumPub),
	}
	body, err := yaml.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	saved := embeddedCordumCounterSigningKey
	embeddedCordumCounterSigningKey = body
	defer func() { embeddedCordumCounterSigningKey = saved }()

	// Build + sign a counter-signature envelope using the signing
	// library directly (this is what Cordum's review workflow will
	// eventually do — here we simulate it inline).
	manifest, err := signing.BuildManifest(packRoot)
	if err != nil {
		t.Fatal(err)
	}
	signedEnvelope, err := signing.SignManifest(manifest, cordumPriv, "cordum-test")
	if err != nil {
		t.Fatal(err)
	}
	envBody, err := signing.EncodeEnvelopeYAML(signedEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, cordumCounterSigFilename), envBody, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	result, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:           true,
		RequireCordumSig: true,
		TrustedKeysDir:   trustedDir,
		Stderr:           &out,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !result.HasCordumCounterSig {
		t.Errorf("HasCordumCounterSig=false")
	}
}

func TestVerifyInstallBundle_RequireCordumSigNoEmbeddedKey(t *testing.T) {
	packRoot, trustedDir := signInstallTestPack(t, "pack-test")

	// Blank out the embedded key.
	saved := embeddedCordumCounterSigningKey
	embeddedCordumCounterSigningKey = nil
	defer func() { embeddedCordumCounterSigningKey = saved }()

	var out bytes.Buffer
	_, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:           true,
		RequireCordumSig: true,
		TrustedKeysDir:   trustedDir,
		Stderr:           &out,
	})
	if !errors.Is(err, ErrCordumSigUnavailable) {
		t.Fatalf("want ErrCordumSigUnavailable, got %v", err)
	}
}

func TestVerifyInstallBundle_ErrorMessageCitesKid(t *testing.T) {
	packRoot, trustedDir := signInstallTestPack(t, "pack-test")
	// Tamper to force a hash-mismatch error that should reference the
	// tampered file path.
	if err := os.WriteFile(filepath.Join(packRoot, "schemas", "In.json"), []byte(`{"type":"number"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, err := VerifyInstallBundle(packRoot, "toolchain-test", PackVerificationOptions{
		Strict:         true,
		TrustedKeysDir: trustedDir,
		Stderr:         &out,
	})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "schemas/In.json") {
		t.Errorf("error should cite failing file; got: %v", err)
	}
}

func TestResolvePackStrict(t *testing.T) {
	cases := []struct {
		flag bool
		env  string
		want bool
	}{
		{flag: true, env: "", want: true},
		{flag: false, env: "1", want: true},
		{flag: false, env: "true", want: true},
		{flag: false, env: "YES", want: true},
		{flag: false, env: "0", want: false},
		{flag: false, env: "", want: false},
	}
	for _, c := range cases {
		t.Setenv(envPackStrict, c.env)
		if got := resolvePackStrict(c.flag); got != c.want {
			t.Errorf("resolvePackStrict(flag=%v, env=%q)=%v, want %v", c.flag, c.env, got, c.want)
		}
	}
}
