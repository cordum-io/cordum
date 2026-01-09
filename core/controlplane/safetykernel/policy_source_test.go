package safetykernel

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
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
