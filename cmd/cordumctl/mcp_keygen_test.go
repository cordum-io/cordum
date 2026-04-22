package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/mcp/outbound"
)

// buildKeygenCLI compiles cordumctl once per test so we can exec it
// and capture both stdout (the base64 SPKI) and stderr (where the
// banner + private PEM go).
func buildKeygenCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cordumctl.exe")
	if runtime.GOOS != "windows" {
		bin = strings.TrimSuffix(bin, ".exe")
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build: %v — %s", err, stderr.String())
	}
	return bin
}

func TestMCPKeygen_WritesPrivatePEMAndPrintsBase64Public(t *testing.T) {
	bin := buildKeygenCLI(t)
	dir := t.TempDir()
	privPath := filepath.Join(dir, "priv.pem")

	cmd := exec.Command(bin, "mcp", "keygen", "--out", privPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("keygen: %v — %s", err, stderr.String())
	}

	// Public key: base64 SPKI on stdout, single line, decodable.
	pubB64 := strings.TrimSpace(stdout.String())
	if pubB64 == "" {
		t.Fatal("stdout empty — expected base64 public key")
	}
	pubDER, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		t.Fatalf("public key not base64: %v", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		t.Fatalf("public key not PKIX: %v", err)
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key wrong type: %T", parsed)
	}
	if pub.Curve.Params().Name != "P-256" {
		t.Errorf("curve = %q, want P-256", pub.Curve.Params().Name)
	}

	// Round-trip: load the private key via outbound.LoadPrivateKeyFromEnv
	// pointed at the file path, and verify it matches the SPKI stdout.
	t.Setenv(outbound.EnvSigningKeyPath, privPath)
	t.Setenv(outbound.EnvSigningKey, "")
	loaded, keyID, err := outbound.LoadPrivateKeyFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if keyID != "default" {
		t.Errorf("keyID = %q, want default", keyID)
	}
	if loaded.X.Cmp(pub.X) != 0 || loaded.Y.Cmp(pub.Y) != 0 {
		t.Error("loaded private key's public half does NOT match printed base64 SPKI — keygen round-trip is broken")
	}

	// Private PEM on disk should be PKCS#8 and 0600 permissions (skip
	// mode check on Windows — ACL model doesn't translate cleanly).
	data, err := loadPrivPEMForTest(privPath)
	if err != nil {
		t.Fatalf("read priv file: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		t.Fatalf("expected PRIVATE KEY pem block, got %q", block.Type)
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		t.Errorf("private PEM not PKCS#8: %v", err)
	}
}

func loadPrivPEMForTest(path string) ([]byte, error) {
	return os.ReadFile(path)
}
