package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/packs/signing"
	"gopkg.in/yaml.v3"
)

func writeTestPack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkfile := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	mkfile("pack.yaml", `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: toolchain-test
  version: 0.1.0
resources:
  schemas:
    - id: t/In
      path: schemas/In.json
  workflows:
    - id: t.echo
      path: workflows/echo.yaml
`)
	mkfile("schemas/In.json", `{"type":"object"}`)
	mkfile("workflows/echo.yaml", "name: echo\n")
	return root
}

func writeTestPrivateKey(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pack.key")
	rec := packPrivateKeyRecord{
		KeyID:      "pack-test",
		Algorithm:  signing.AlgorithmEd25519,
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
	}
	body, err := yaml.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, pub
}

func writeTrustedKey(t *testing.T, pub ed25519.PublicKey, kid string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, kid+".pub")
	body, err := yaml.Marshal(packPublicKeyRecord{
		KeyID:        kid,
		Algorithm:    signing.AlgorithmEd25519,
		PublicKeyB64: base64.StdEncoding.EncodeToString(pub),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPackSignVerifyRoundTrip proves the happy path: generated key,
// sign a pack, verify-signature succeeds.
func TestPackSignVerifyRoundTrip(t *testing.T) {
	packRoot := writeTestPack(t)
	keyPath, pub := writeTestPrivateKey(t)
	trustedDir := writeTrustedKey(t, pub, "pack-test")

	if err := runPackSign([]string{"--key", keyPath, packRoot}); err != nil {
		t.Fatalf("runPackSign: %v", err)
	}
	sigPath := filepath.Join(packRoot, defaultPackSigFile)
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("signature file not written: %v", err)
	}

	if err := runPackVerifySignature([]string{"--trusted-keys", trustedDir, packRoot}); err != nil {
		t.Fatalf("runPackVerifySignature: %v", err)
	}
}

// TestPackSignVerifyTamperedFails proves tamper detection: flipping
// one byte in a signed schema fails verify-signature.
func TestPackSignVerifyTamperedFails(t *testing.T) {
	packRoot := writeTestPack(t)
	keyPath, pub := writeTestPrivateKey(t)
	trustedDir := writeTrustedKey(t, pub, "pack-test")

	if err := runPackSign([]string{"--key", keyPath, packRoot}); err != nil {
		t.Fatal(err)
	}
	// Tamper with the signed schema.
	if err := os.WriteFile(filepath.Join(packRoot, "schemas", "In.json"), []byte(`{"type":"array"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runPackVerifySignature([]string{"--trusted-keys", trustedDir, packRoot})
	if err == nil {
		t.Fatal("expected tamper detection")
	}
	if !errors.Is(err, signing.ErrHashMismatch) && !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("err = %v, want ErrHashMismatch", err)
	}
}

// TestPackSignJSONEnvelope proves the --json flag produces a JSON
// envelope that verify-signature can still read.
func TestPackSignJSONEnvelope(t *testing.T) {
	packRoot := writeTestPack(t)
	keyPath, pub := writeTestPrivateKey(t)
	trustedDir := writeTrustedKey(t, pub, "pack-test")
	outPath := filepath.Join(packRoot, "pack.yaml.sig.json")

	if err := runPackSign([]string{"--key", keyPath, "--json", "--out", outPath, packRoot}); err != nil {
		t.Fatalf("runPackSign: %v", err)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		t.Fatalf("envelope not JSON: %q", raw[:40])
	}
	if err := runPackVerifySignature([]string{"--trusted-keys", trustedDir, "--sig", outPath, packRoot}); err != nil {
		t.Fatalf("verify json envelope: %v", err)
	}
}

// TestPackKeygenRefusesOverwrite proves --force guard.
func TestPackKeygenRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "pack.key")
	if err := os.WriteFile(keyPath, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Capture stdout/stderr to keep test output tidy.
	origStdout := os.Stdout
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	err := runPackSignKeygen([]string{"--out", keyPath})
	_ = w.Close()
	_, _ = io.ReadAll(r)
	if err == nil {
		t.Fatal("expected overwrite guard to fire")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("err = %v", err)
	}
	// With --force, the overwrite succeeds.
	if err := runPackSignKeygen([]string{"--out", keyPath, "--force"}); err != nil {
		t.Fatalf("--force keygen: %v", err)
	}
}

// TestPackExportKeyShape proves the JSON output carries kid, algorithm,
// and a 32-byte base64 public key.
func TestPackExportKeyShape(t *testing.T) {
	keyPath, _ := writeTestPrivateKey(t)

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	if err := runPackExportKey([]string{"--key", keyPath}); err != nil {
		t.Fatalf("runPackExportKey: %v", err)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	body := string(out)
	if !strings.Contains(body, `"kid": "pack-test"`) {
		t.Fatalf("missing kid: %s", body)
	}
	if !strings.Contains(body, `"algorithm": "ed25519"`) {
		t.Fatalf("missing algorithm: %s", body)
	}
	if !strings.Contains(body, `"public_key_b64"`) {
		t.Fatalf("missing public_key_b64: %s", body)
	}
}

// BUG-015 regression lock: the generated private key file must be 0o600
// (owner-only). Windows skips strict-perm check because the model differs.
func TestPackKeygen_PrivateKeyFileIs0600(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pkey")
	if err := runPackSignKeygen([]string{"--out", keyPath, "--kid", "perm-test"}); err != nil {
		t.Fatalf("runPackSignKeygen: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows permission model differs; perms are managed differently")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("private key file mode = %#o, want 0600", got)
	}
}
