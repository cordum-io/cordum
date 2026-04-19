package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/policysign"
)

// buildPolicyCLI compiles cordumctl into a temp binary and returns the
// path. The tests exec this binary so they can observe real exit codes
// (which `runPolicy*Cmd` produces via os.Exit).
func buildPolicyCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cordumctl.exe")
	if runtime.GOOS != "windows" {
		bin = strings.TrimSuffix(bin, ".exe")
	}
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build cordumctl: %v", err)
	}
	return bin
}

// runCLI invokes the compiled binary and returns the exit code along
// with stderr so assertions can include the error text when debugging.
func runCLI(t *testing.T, bin string, env []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run cordumctl: %v", err)
		}
	}
	return code, stdoutBuf.String(), stderrBuf.String()
}

func generateKeyEnvs(t *testing.T) (privEnvVal, pubEnvVal string, pub ed25519.PublicKey) {
	t.Helper()
	p, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(p), p
}

func TestPolicySignVerify_RoundTrip(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, pubB64, _ := generateKeyEnvs(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	policyContent := "rules:\n  - id: r\n    decision: allow\n"
	if err := os.WriteFile(policyPath, []byte(policyContent), 0o600); err != nil {
		t.Fatal(err)
	}

	// Sign
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=test-key",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatalf("sign failed: code=%d stderr=%s", code, stderr)
	}

	// Inspect signature file
	sigBytes, err := os.ReadFile(policyPath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	var sig policysign.Signature
	if err := json.Unmarshal(sigBytes, &sig); err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if sig.KeyID != "test-key" {
		t.Errorf("key_id = %q want test-key", sig.KeyID)
	}
	if sig.Algorithm != policysign.AlgorithmEd25519 {
		t.Errorf("algorithm = %q", sig.Algorithm)
	}

	// Verify
	code, stdout, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "TEST-KEY=" + pubB64,
	}, "policy", "verify", "--in", policyPath)
	if code != 0 {
		t.Fatalf("verify failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "verified") {
		t.Errorf("expected 'verified' in output, got %q", stdout)
	}
}

func TestPolicyVerify_TamperedPolicy(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, pubB64, _ := generateKeyEnvs(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=k1",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatal("sign should succeed")
	}
	// Tamper with the policy.
	if err := os.WriteFile(policyPath, []byte("rules: [tampered]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "K1=" + pubB64,
	}, "policy", "verify", "--in", policyPath)
	if code != 1 {
		t.Fatalf("want exit 1 on tamper, got %d stderr=%s", code, stderr)
	}
}

func TestPolicyVerify_WrongPublicKey(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, _, _ := generateKeyEnvs(t)
	// Different public key — NOT the one that signed.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	otherPubB64 := base64.StdEncoding.EncodeToString(otherPub)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=k1",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatal("sign should succeed")
	}
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "K1=" + otherPubB64,
	}, "policy", "verify", "--in", policyPath)
	if code != 1 {
		t.Fatalf("want exit 1 with wrong key, got %d stderr=%s", code, stderr)
	}
}

func TestPolicyVerify_MissingTrustedKey(t *testing.T) {
	bin := buildPolicyCLI(t)
	privB64, _, _ := generateKeyEnvs(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=" + privB64,
		policysign.EnvSigningKeyID + "=k1",
	}, "policy", "sign", "--in", policyPath)
	if code != 0 {
		t.Fatal("sign should succeed")
	}
	// No trust store configured at all.
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvPublicKeyPrefix + "OTHER=",
	}, "policy", "verify", "--in", policyPath)
	if code != 2 {
		t.Fatalf("want exit 2 with no trust store, got %d stderr=%s", code, stderr)
	}
}

func TestPolicySign_NoPrivateKey(t *testing.T) {
	bin := buildPolicyCLI(t)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("rules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, bin, []string{
		policysign.EnvSigningKey + "=",
	}, "policy", "sign", "--in", policyPath)
	if code != 1 {
		t.Fatalf("want exit 1 without key, got %d stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "not set") {
		t.Errorf("expected 'not set' error, got %q", stderr)
	}
}
