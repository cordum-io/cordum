package safetykernel

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPolicy = `version: v1
default_tenant: default
tenants:
  default:
    allow_topics:
      - job.*
`

func TestPolicySourceFromEnv(t *testing.T) {
	os.Setenv("SAFETY_POLICY_URL", "http://example")
	if got := policySourceFromEnv("/tmp/policy.yaml"); got != "http://example" {
		t.Fatalf("unexpected policy source: %s", got)
	}
	os.Unsetenv("SAFETY_POLICY_URL")
	if got := policySourceFromEnv("/tmp/policy.yaml"); got != "/tmp/policy.yaml" {
		t.Fatalf("unexpected policy source fallback: %s", got)
	}
}

func TestLoadPolicyBundleFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(testPolicy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	policy, snapshot, err := loadPolicyBundle(path)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if policy == nil || policy.Version != "v1" {
		t.Fatalf("expected policy version v1")
	}
	if !strings.HasPrefix(snapshot, "v1:") {
		t.Fatalf("expected versioned snapshot, got %s", snapshot)
	}
}

func TestReadPolicySourceHTTP(t *testing.T) {
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(testPolicy))
	}))
	defer srv.Close()

	data, err := readPolicySource(srv.URL)
	if err != nil {
		t.Fatalf("read policy url: %v", err)
	}
	if string(data) != testPolicy {
		t.Fatalf("unexpected policy data")
	}
}

func TestReadPolicySourceHTTPMaxSize(t *testing.T) {
	t.Setenv("SAFETY_POLICY_MAX_BYTES", "16")
	t.Setenv("SAFETY_POLICY_URL_ALLOW_PRIVATE", "1")
	oversized := strings.Repeat("a", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(oversized))
	}))
	defer srv.Close()

	_, err := readPolicySource(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "max size") {
		t.Fatalf("expected max size error, got %v", err)
	}
}

func TestReadPolicySourceHTTPBlocksPrivate(t *testing.T) {
	_, err := readPolicySource("http://127.0.0.1:1/policy.yaml")
	if err == nil {
		t.Fatalf("expected private policy url to be blocked")
	}
}

func TestFetchPolicyURLRejectsDNSRebinding(t *testing.T) {
	originalLookup := policyLookupIP
	t.Cleanup(func() { policyLookupIP = originalLookup })

	call := 0
	policyLookupIP = func(host string) ([]net.IP, error) {
		call++
		if call == 1 {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}

	_, err := fetchPolicyURL("http://example.com/policy.yaml")
	if err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Fatalf("expected rebinding rejection, got %v", err)
	}
}

func TestReadPolicySourceFileMaxSize(t *testing.T) {
	t.Setenv("SAFETY_POLICY_MAX_BYTES", "16")
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 32)), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := readPolicySource(path)
	if err == nil || !strings.Contains(err.Error(), "max size") {
		t.Fatalf("expected max size error, got %v", err)
	}
}

func TestVerifyPolicySignature(t *testing.T) {
	data := []byte(testPolicy)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, data)

	os.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	os.Setenv("SAFETY_POLICY_SIGNATURE", base64.StdEncoding.EncodeToString(sig))
	defer os.Unsetenv("SAFETY_POLICY_PUBLIC_KEY")
	defer os.Unsetenv("SAFETY_POLICY_SIGNATURE")

	if err := verifyPolicySignature(data, "policy.yaml"); err != nil {
		t.Fatalf("verify signature: %v", err)
	}
}

func TestVerifyPolicySignatureRequiresKeyWhenEnforced(t *testing.T) {
	os.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	defer os.Unsetenv("SAFETY_POLICY_SIGNATURE_REQUIRED")

	if err := verifyPolicySignature([]byte(testPolicy), "policy.yaml"); err == nil {
		t.Fatalf("expected signature requirement error")
	}
}

func TestDecodeKey(t *testing.T) {
	data := []byte("hello")
	b64 := base64.StdEncoding.EncodeToString(data)
	if out, err := decodeKey(b64); err != nil || string(out) != string(data) {
		t.Fatalf("decode base64 failed")
	}
	hexStr := hex.EncodeToString(data)
	if out, err := decodeKey(hexStr); err != nil || string(out) != string(data) {
		t.Fatalf("decode hex failed")
	}
	if _, err := decodeKey("@@@"); err == nil {
		t.Fatalf("expected decode error")
	}
}

// ---------------------------------------------------------------------------
// Signature enforcement security regression tests
// ---------------------------------------------------------------------------

func TestVerifyPolicySignatureProductionRequiresKey(t *testing.T) {
	// In production mode, signature verification is mandatory.
	// Missing SAFETY_POLICY_PUBLIC_KEY must be an error, not a silent bypass.
	t.Setenv("CORDUM_PRODUCTION", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", "")
	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "")

	err := verifyPolicySignature([]byte(testPolicy), "policy.yaml")
	if err == nil {
		t.Fatalf("production mode must require signature key")
	}
	if !strings.Contains(err.Error(), "SAFETY_POLICY_PUBLIC_KEY not configured") {
		t.Fatalf("expected missing key error, got: %v", err)
	}
}

func TestVerifyPolicySignatureProductionInvalidSig(t *testing.T) {
	// In production, a tampered signature must be rejected.
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	t.Setenv("CORDUM_PRODUCTION", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	// Wrong signature (all zeros).
	t.Setenv("SAFETY_POLICY_SIGNATURE", base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)))

	err = verifyPolicySignature([]byte(testPolicy), "policy.yaml")
	if err == nil {
		t.Fatalf("expected signature verification failure")
	}
	if !strings.Contains(err.Error(), "verification failed") {
		t.Fatalf("expected verification failure error, got: %v", err)
	}
}

func TestVerifyPolicySignatureTamperedData(t *testing.T) {
	// Valid signature for original data must fail for modified data.
	data := []byte(testPolicy)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sig := ed25519.Sign(priv, data)

	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	t.Setenv("SAFETY_POLICY_SIGNATURE", base64.StdEncoding.EncodeToString(sig))

	// Tampered data: change policy content
	tampered := []byte(testPolicy + "\n  evil:\n    allow_topics:\n      - '*'\n")
	err = verifyPolicySignature(tampered, "policy.yaml")
	if err == nil {
		t.Fatalf("expected tampered data to fail verification")
	}
}

func TestVerifyPolicySignatureWrongKeyPair(t *testing.T) {
	// Signature from a different key pair must be rejected.
	data := []byte(testPolicy)
	_, priv1, _ := ed25519.GenerateKey(nil)
	pub2, _, _ := ed25519.GenerateKey(nil)
	sig := ed25519.Sign(priv1, data)

	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub2))
	t.Setenv("SAFETY_POLICY_SIGNATURE", base64.StdEncoding.EncodeToString(sig))

	err := verifyPolicySignature(data, "policy.yaml")
	if err == nil {
		t.Fatalf("expected wrong key pair to fail verification")
	}
}

func TestVerifyPolicySignatureFromSigFile(t *testing.T) {
	// Test .sig sidecar file path for file-based policy sources.
	data := []byte(testPolicy)
	pub, priv, _ := ed25519.GenerateKey(nil)
	sig := ed25519.Sign(priv, data)

	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")
	sigPath := policyPath + ".sig"
	if err := os.WriteFile(policyPath, data, 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	t.Setenv("SAFETY_POLICY_SIGNATURE", "")
	t.Setenv("SAFETY_POLICY_SIGNATURE_PATH", "")

	err := verifyPolicySignature(data, policyPath)
	if err != nil {
		t.Fatalf("expected .sig sidecar to be used: %v", err)
	}
}

func TestVerifyPolicySignatureHTTPNoSigFails(t *testing.T) {
	// HTTP policy source with no signature env var must fail when signature required.
	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	pub, _, _ := ed25519.GenerateKey(nil)
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	t.Setenv("SAFETY_POLICY_SIGNATURE", "")
	t.Setenv("SAFETY_POLICY_SIGNATURE_PATH", "")

	err := verifyPolicySignature([]byte(testPolicy), "https://example.com/policy.yaml")
	if err == nil {
		t.Fatalf("expected error for HTTP source with no signature")
	}
	if !strings.Contains(err.Error(), "no signature provided") {
		t.Fatalf("expected 'no signature provided' error, got: %v", err)
	}
}

func TestVerifyPolicySignatureDevModeNoKeySkips(t *testing.T) {
	// In dev mode without any key or enforcement, verification should pass silently.
	t.Setenv("CORDUM_PRODUCTION", "")
	t.Setenv("CORDUM_ENV", "dev")
	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", "")

	err := verifyPolicySignature([]byte(testPolicy), "policy.yaml")
	if err != nil {
		t.Fatalf("dev mode without key should pass: %v", err)
	}
}

func TestVerifyPolicySignatureFromSignaturePath(t *testing.T) {
	// Signature can be provided via SAFETY_POLICY_SIGNATURE_PATH.
	data := []byte(testPolicy)
	pub, priv, _ := ed25519.GenerateKey(nil)
	sig := ed25519.Sign(priv, data)

	dir := t.TempDir()
	sigPath := filepath.Join(dir, "policy.sig")
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		t.Fatalf("write sig: %v", err)
	}

	t.Setenv("SAFETY_POLICY_SIGNATURE_REQUIRED", "1")
	t.Setenv("SAFETY_POLICY_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	t.Setenv("SAFETY_POLICY_SIGNATURE", "")
	t.Setenv("SAFETY_POLICY_SIGNATURE_PATH", sigPath)

	err := verifyPolicySignature(data, "https://example.com/policy.yaml")
	if err != nil {
		t.Fatalf("signature path should work: %v", err)
	}
}

func TestReadPolicySourceHTTPBlocksHTTPInProduction(t *testing.T) {
	// Integration test: readPolicySource → fetchPolicyURL rejects HTTP in production.
	t.Setenv("CORDUM_PRODUCTION", "1")

	_, err := readPolicySource("http://example.com/policy.yaml")
	if err == nil {
		t.Fatalf("expected HTTP to be rejected in production")
	}
	if !strings.Contains(err.Error(), "HTTPS required") {
		t.Fatalf("expected HTTPS requirement error, got: %v", err)
	}
}
