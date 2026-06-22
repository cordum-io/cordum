package safetykernel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/policysign"
)

// TestLoadPolicyBundle_EnforceRejectsUnsignedFilePolicy locks Fix #4: the
// file/URL policy load path used the legacy verifyPolicySignature, which
// ignored CORDUM_POLICY_STRICT and never consulted the trust store — so an
// unsigned policy was accepted outside production. loadPolicyBundle now uses
// the same trust-store verifier as the Redis-bundle path, so an unsigned
// policy under enforce mode (with a trust store configured) must be rejected.
// On the old code this returned nil (accepted) and the test would fail.
func TestLoadPolicyBundle_EnforceRejectsUnsignedFilePolicy(t *testing.T) {
	clearFileLoaderEnv(t)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"PRIMARY", base64.StdEncoding.EncodeToString(pub))

	dir := t.TempDir()
	// Unsigned policy: no sibling .sig sidecar is written.
	policyPath := writePolicyFile(t, dir, "policy.yaml", []byte(fileLoaderPolicy))

	if _, _, err := loadPolicyBundle(policyPath); err == nil {
		t.Fatal("loadPolicyBundle accepted an UNSIGNED file policy under enforce mode; want rejection")
	} else if !errors.Is(err, ErrBundleUnsigned) {
		t.Fatalf("loadPolicyBundle error = %v, want ErrBundleUnsigned", err)
	}
}

// TestLoadPolicyBundle_WarnAcceptsUnsignedFilePolicy is the negative control:
// the default dev posture (warn) must still load an unsigned policy so the fix
// does not break local development or first-boot bring-up.
func TestLoadPolicyBundle_WarnAcceptsUnsignedFilePolicy(t *testing.T) {
	clearFileLoaderEnv(t)
	t.Setenv(policysign.EnvStrictMode, "warn")

	dir := t.TempDir()
	policyPath := writePolicyFile(t, dir, "policy.yaml", []byte(fileLoaderPolicy))

	policy, _, err := loadPolicyBundle(policyPath)
	if err != nil {
		t.Fatalf("warn mode should accept an unsigned dev policy, got %v", err)
	}
	if policy == nil {
		t.Fatal("expected a parsed policy in warn mode, got nil")
	}
}
