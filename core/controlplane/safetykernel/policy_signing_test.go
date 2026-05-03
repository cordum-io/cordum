package safetykernel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/policysign"
)

func genKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func signatureMap(t *testing.T, priv ed25519.PrivateKey, keyID string, content []byte) map[string]any {
	t.Helper()
	sig, err := policysign.Sign(priv, keyID, content)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"algorithm":    sig.Algorithm,
		"key_id":       sig.KeyID,
		"value":        sig.Value,
		"hash":         sig.Hash,
		"signed_bytes": sig.SignedBytes,
	}
}

func trustStoreWith(t *testing.T, keyID string, pub ed25519.PublicKey) *policysign.TrustStore {
	t.Helper()
	store := policysign.NewTrustStore()
	if err := store.Add(keyID, pub); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestVerifyBundleSignature_OffSkips(t *testing.T) {
	_, _ = genKeyPair(t)
	err := verifyBundleSignature("b1", []byte("x"), nil, policysign.ModeOff, nil)
	if err != nil {
		t.Fatalf("off mode should skip, got %v", err)
	}
}

func TestVerifyBundleSignature_WarnUnsignedContinues(t *testing.T) {
	err := verifyBundleSignature("b1", []byte("x"), nil, policysign.ModeWarn, nil)
	if err != nil {
		t.Fatalf("warn should tolerate unsigned, got %v", err)
	}
}

func TestVerifyBundleSignature_EnforceUnsignedRejects(t *testing.T) {
	err := verifyBundleSignature("b1", []byte("x"), nil, policysign.ModeEnforce, policysign.NewTrustStore())
	if !errors.Is(err, ErrBundleUnsigned) {
		t.Fatalf("enforce+unsigned: want ErrBundleUnsigned, got %v", err)
	}
}

func TestVerifyBundleSignature_WarnAcceptsValidSig(t *testing.T) {
	pub, priv := genKeyPair(t)
	content := []byte("policy")
	sig := signatureMap(t, priv, "k1", content)
	err := verifyBundleSignature("b1", content, sig, policysign.ModeWarn, trustStoreWith(t, "k1", pub))
	if err != nil {
		t.Fatalf("warn should accept valid sig, got %v", err)
	}
}

func TestVerifyBundleSignature_EnforceAcceptsValidSig(t *testing.T) {
	pub, priv := genKeyPair(t)
	content := []byte("policy")
	sig := signatureMap(t, priv, "k1", content)
	err := verifyBundleSignature("b1", content, sig, policysign.ModeEnforce, trustStoreWith(t, "k1", pub))
	if err != nil {
		t.Fatalf("enforce should accept valid sig, got %v", err)
	}
}

func TestVerifyBundleSignature_EnforceRejectsTampered(t *testing.T) {
	pub, priv := genKeyPair(t)
	content := []byte("policy")
	sig := signatureMap(t, priv, "k1", content)
	err := verifyBundleSignature("b1", []byte("policy-TAMPERED"), sig, policysign.ModeEnforce, trustStoreWith(t, "k1", pub))
	if err == nil {
		t.Fatal("tampered content should be rejected in enforce mode")
	}
}

func TestVerifyBundleSignature_WarnLogsTamperedButAccepts(t *testing.T) {
	pub, priv := genKeyPair(t)
	content := []byte("policy")
	sig := signatureMap(t, priv, "k1", content)
	err := verifyBundleSignature("b1", []byte("TAMPERED"), sig, policysign.ModeWarn, trustStoreWith(t, "k1", pub))
	if err != nil {
		t.Fatalf("warn should tolerate tampered (log-only), got %v", err)
	}
}

func TestVerifyBundleSignature_EnforceRejectsUntrustedKey(t *testing.T) {
	_, priv := genKeyPair(t)
	// Trust a DIFFERENT key id
	otherPub, _ := genKeyPair(t)
	content := []byte("policy")
	sig := signatureMap(t, priv, "rogue", content)
	err := verifyBundleSignature("b1", content, sig, policysign.ModeEnforce, trustStoreWith(t, "trusted", otherPub))
	if !errors.Is(err, ErrUntrustedKeyID) {
		t.Fatalf("want ErrUntrustedKeyID, got %v", err)
	}
}

func TestVerifyBundleSignature_EnforceRejectsEmptyTrustStore(t *testing.T) {
	_, priv := genKeyPair(t)
	content := []byte("policy")
	sig := signatureMap(t, priv, "k1", content)
	err := verifyBundleSignature("b1", content, sig, policysign.ModeEnforce, policysign.NewTrustStore())
	if !errors.Is(err, ErrNoTrustStoreConfigured) {
		t.Fatalf("want ErrNoTrustStoreConfigured, got %v", err)
	}
}

func TestVerifyBundleSignature_EnforceRejectsMalformedSig(t *testing.T) {
	store := policysign.NewTrustStore()
	pub, _ := genKeyPair(t)
	_ = store.Add("k1", pub)
	// Non-empty map but missing algorithm+value -> malformed
	malformed := map[string]any{"key_id": "k1"}
	err := verifyBundleSignature("b1", []byte("x"), malformed, policysign.ModeEnforce, store)
	if !errors.Is(err, ErrBundleSignatureMalformed) {
		t.Fatalf("want ErrBundleSignatureMalformed, got %v", err)
	}
}

func TestExtractBundleSignature(t *testing.T) {
	sig, present := extractBundleSignature(nil)
	if present {
		t.Error("nil should not be present")
	}
	sig, present = extractBundleSignature(map[string]any{})
	if present {
		t.Error("empty map should not be present")
	}
	sig, present = extractBundleSignature(map[string]any{
		"algorithm":    "ed25519",
		"key_id":       "k",
		"value":        "abc",
		"hash":         "def",
		"signed_bytes": float64(42),
	})
	if !present {
		t.Fatal("expected present")
	}
	if sig.SignedBytes != 42 {
		t.Errorf("signed_bytes = %d, want 42", sig.SignedBytes)
	}
}

func TestFragmentSignature(t *testing.T) {
	if fragmentSignature("bare string") != nil {
		t.Error("bare string fragments have no signature")
	}
	if fragmentSignature(map[string]any{"content": "x"}) != nil {
		t.Error("absent sig should be nil")
	}
	sigMap := map[string]any{"algorithm": "ed25519"}
	got := fragmentSignature(map[string]any{"_signature": sigMap})
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("want map, got %T", got)
	}
	if gotMap["algorithm"] != "ed25519" {
		t.Errorf("algorithm = %v", gotMap["algorithm"])
	}
}

func TestNewBundleVerifier_SnapshotsEnv(t *testing.T) {
	pub, _ := genKeyPair(t)
	t.Setenv(policysign.EnvStrictMode, "enforce")
	t.Setenv(policysign.EnvPublicKeyPrefix+"TEST", base64.StdEncoding.EncodeToString(pub))
	v := newBundleVerifier()
	if v.mode != policysign.ModeEnforce {
		t.Errorf("mode = %v want enforce", v.mode)
	}
	if _, ok := v.store.Lookup("TEST"); !ok {
		t.Error("trust store missing TEST key")
	}
}

func TestNewBundleVerifier_BadModeDefaultsToWarn(t *testing.T) {
	t.Setenv(policysign.EnvStrictMode, "wobble")
	v := newBundleVerifier()
	if v.mode != policysign.ModeWarn {
		t.Errorf("bad mode should fall back to warn, got %v", v.mode)
	}
}

// newLoadFragmentsHarness returns a policyLoader pointed at a live
// miniredis, plus a helper to seed one or more bundles with optional
// signature maps. Used by the end-to-end tests below to exercise the
// real loadFragments call path — proving the verifier is wired in, not
// just defined.
func newLoadFragmentsHarness(t *testing.T) (*policyLoader, func(bundles map[string]any)) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(srv.Close)
	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("config svc: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	seed := func(bundles map[string]any) {
		doc := &configsvc.Document{
			Scope:   configsvc.ScopeSystem,
			ScopeID: "policy",
			Data:    map[string]any{"bundles": bundles},
		}
		if err := svc.Set(context.Background(), doc); err != nil {
			t.Fatalf("seed config: %v", err)
		}
	}

	loader := &policyLoader{
		configSvc:   svc,
		configScope: configsvc.ScopeSystem,
		configID:    "policy",
		configKey:   "bundles",
	}
	return loader, seed
}

const testSignedPolicy = `rules:
  - id: require-ops
    match:
      topics:
        - job.ops.*
    decision: require_approval
    reason: ops writes
`

// TestLoadFragments_EnforceRejectsUnsignedBundle proves the kernel's
// live fragment-load path honours enforce mode. Regression guard for
// the QA finding that verifyBundleSignature existed but was never
// called from loadFragments.
func TestLoadFragments_EnforceRejectsUnsignedBundle(t *testing.T) {
	t.Setenv(policysign.EnvStrictMode, "enforce")
	// Trust store populated so enforce does not fail on "no trust
	// store" before reaching the unsigned-bundle branch.
	pub, _ := genKeyPair(t)
	t.Setenv(policysign.EnvPublicKeyPrefix+"TEST", base64.StdEncoding.EncodeToString(pub))

	loader, seed := newLoadFragmentsHarness(t)
	seed(map[string]any{
		"custom-unsigned": map[string]any{
			"content": testSignedPolicy,
			"enabled": true,
		},
	})

	_, _, _, _, err := loader.loadFragments(context.Background())
	if err == nil {
		t.Fatal("expected enforce mode to reject unsigned bundle, got nil")
	}
	if !errors.Is(err, ErrBundleUnsigned) {
		t.Errorf("expected ErrBundleUnsigned, got %v", err)
	}
}

// TestLoadFragments_EnforceAcceptsSignedBundle proves the full happy
// path: bundle with valid _signature + trust store key => loadFragments
// returns the parsed policy and snapshot.
func TestLoadFragments_EnforceAcceptsSignedBundle(t *testing.T) {
	t.Setenv(policysign.EnvStrictMode, "enforce")
	pub, priv := genKeyPair(t)
	t.Setenv(policysign.EnvPublicKeyPrefix+"TEST", base64.StdEncoding.EncodeToString(pub))

	loader, seed := newLoadFragmentsHarness(t)
	seed(map[string]any{
		"custom-signed": map[string]any{
			"content":                  testSignedPolicy,
			"enabled":                  true,
			policySignatureFieldKey:    signatureMap(t, priv, "TEST", []byte(testSignedPolicy)),
		},
	})

	policy, _, snapshot, _, err := loader.loadFragments(context.Background())
	if err != nil {
		t.Fatalf("loadFragments: %v", err)
	}
	if policy == nil {
		t.Fatal("expected non-nil policy")
	}
	if snapshot == "" {
		t.Fatal("expected non-empty snapshot")
	}
}

// TestLoadFragments_EnforceRejectsTamperedContent proves hash-mismatch
// propagation: the stored signature was over "policy-A" but the stored
// content is "policy-B" — loadFragments must refuse the load.
func TestLoadFragments_EnforceRejectsTamperedContent(t *testing.T) {
	t.Setenv(policysign.EnvStrictMode, "enforce")
	pub, priv := genKeyPair(t)
	t.Setenv(policysign.EnvPublicKeyPrefix+"TEST", base64.StdEncoding.EncodeToString(pub))

	loader, seed := newLoadFragmentsHarness(t)
	original := []byte(testSignedPolicy)
	tampered := []byte(testSignedPolicy + "\n  # attacker appended a comment\n")
	seed(map[string]any{
		"custom-tampered": map[string]any{
			"content":                  string(tampered),
			"enabled":                  true,
			policySignatureFieldKey:    signatureMap(t, priv, "TEST", original),
		},
	})

	_, _, _, _, err := loader.loadFragments(context.Background())
	if err == nil {
		t.Fatal("expected tampered content to be rejected")
	}
}

// TestLoadFragments_WarnAcceptsUnsignedBundle confirms that an
// unsigned bundle is accepted in warn mode so operators can stage a
// rollout without losing policy.
func TestLoadFragments_WarnAcceptsUnsignedBundle(t *testing.T) {
	t.Setenv(policysign.EnvStrictMode, "warn")

	loader, seed := newLoadFragmentsHarness(t)
	seed(map[string]any{
		"custom-warn": map[string]any{
			"content": testSignedPolicy,
			"enabled": true,
		},
	})

	policy, _, _, _, err := loader.loadFragments(context.Background())
	if err != nil {
		t.Fatalf("warn mode should accept unsigned, got %v", err)
	}
	if policy == nil {
		t.Fatal("expected policy to load in warn mode")
	}
}
