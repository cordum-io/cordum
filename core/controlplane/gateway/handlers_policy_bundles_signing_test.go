package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/policysign"
)

// putSignedBundle is a lightweight helper that shares the boilerplate of
// calling handlePutPolicyBundle from signing-related tests.
func putSignedBundle(t *testing.T, s *server, id, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"content": content,
		"enabled": true,
		"author":  "tester",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/"+id, bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handlePutPolicyBundle(rec, req)
	return rec
}

// loadRawBundle reads the bundle directly from the config store so tests
// can inspect the `_signature` sibling, which the GET handler does not
// expose.
func loadRawBundle(t *testing.T, s *server, id string) map[string]any {
	t.Helper()
	bundles, _, err := s.loadPolicyBundles(testContext())
	if err != nil {
		t.Fatalf("load bundles: %v", err)
	}
	raw, ok := bundles[id].(map[string]any)
	if !ok {
		t.Fatalf("bundle %s missing or malformed", id)
	}
	return raw
}

func testContext() context.Context { return context.Background() }

// TestPutPolicyBundle_SignedInWarnMode asserts that when a signing key
// is configured, a _signature is attached to the bundle even in warn
// mode.
func TestPutPolicyBundle_SignedInWarnMode(t *testing.T) {
	setTestSigningEnv(t, "warn")

	s, _, _ := newTestGateway(t)
	rec := putSignedBundle(t, s, "secops/signed", policyContent)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}

	raw := loadRawBundle(t, s, "secops/signed")
	sigMap, ok := raw[policyBundleSignatureKey].(map[string]any)
	if !ok {
		t.Fatalf("missing _signature on saved bundle; bundle=%v", raw)
	}
	if got := sigMap["algorithm"]; got != policysign.AlgorithmEd25519 {
		t.Errorf("algorithm = %v want %s", got, policysign.AlgorithmEd25519)
	}
	if sigMap["value"] == "" || sigMap["hash"] == "" {
		t.Errorf("expected non-empty value/hash, got %v", sigMap)
	}
}

// TestPutPolicyBundle_SignatureVerifiesAgainstContent proves the
// signature produced by the gateway round-trips through policysign.Verify
// using the public key counterpart of the test signing key.
func TestPutPolicyBundle_SignatureVerifiesAgainstContent(t *testing.T) {
	pub, _ := setTestSigningEnv(t, "warn")

	s, _, _ := newTestGateway(t)
	if rec := putSignedBundle(t, s, "secops/verify", policyContent); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}

	raw := loadRawBundle(t, s, "secops/verify")
	sigAny, ok := raw[policyBundleSignatureKey]
	if !ok {
		t.Fatal("missing _signature")
	}
	sig, ok := signatureFromMap(sigAny)
	if !ok {
		t.Fatal("signatureFromMap failed")
	}
	content, _ := raw["content"].(string)
	if err := policysign.Verify(pub, []byte(content), sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestPutPolicyBundle_RefusesWhenEnforceAndNoKey asserts the gateway
// refuses unsigned saves in enforce mode when no key is configured.
func TestPutPolicyBundle_RefusesWhenEnforceAndNoKey(t *testing.T) {
	clearTestSigningEnv(t)
	t.Setenv(policysign.EnvStrictMode, "enforce")

	s, _, _ := newTestGateway(t)
	rec := putSignedBundle(t, s, "secops/enforce", policyContent)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d want 503; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "signing key") {
		t.Errorf("expected actionable error mentioning signing key, got %s", rec.Body.String())
	}
}

// TestPutPolicyBundle_RefusesWhenWarnAndNoKey matches the design note
// that warn mode also refuses to save unsigned bundles when a signing
// key is unset, so that the kernel does not later warn on bundles we
// know we could have signed.
func TestPutPolicyBundle_RefusesWhenWarnAndNoKey(t *testing.T) {
	clearTestSigningEnv(t)
	t.Setenv(policysign.EnvStrictMode, "warn")

	s, _, _ := newTestGateway(t)
	rec := putSignedBundle(t, s, "secops/warn-nokey", policyContent)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d want 503; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPutPolicyBundle_AllowsUnsignedInOffMode ensures developers can
// still save unsigned bundles locally with strict=off and no key.
func TestPutPolicyBundle_AllowsUnsignedInOffMode(t *testing.T) {
	clearTestSigningEnv(t)
	t.Setenv(policysign.EnvStrictMode, "off")

	s, _, _ := newTestGateway(t)
	if rec := putSignedBundle(t, s, "secops/off", policyContent); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}

	raw := loadRawBundle(t, s, "secops/off")
	if _, signed := raw[policyBundleSignatureKey]; signed {
		t.Error("unsigned save in off mode should not attach _signature")
	}
}

// TestSignatureFromMap_EmptyReturnsNotOK locks down the signal the
// kernel relies on to distinguish "unsigned bundle" from "malformed
// signature".
func TestSignatureFromMap_EmptyReturnsNotOK(t *testing.T) {
	if _, ok := signatureFromMap(nil); ok {
		t.Error("nil should not be a signature")
	}
	if _, ok := signatureFromMap(map[string]any{}); ok {
		t.Error("empty map should not be a signature")
	}
	if _, ok := signatureFromMap("string"); ok {
		t.Error("non-map should not be a signature")
	}
}

// TestGateway_Kernel_RoundTrip validates the full loop: the gateway
// signs content with key K, persists the bundle with _signature, and
// the bytes + signature round-trip through the kernel's in-process
// verifier logic using the same trust store.
func TestGateway_Kernel_RoundTrip(t *testing.T) {
	pub, _ := setTestSigningEnv(t, "enforce")

	s, _, _ := newTestGateway(t)
	if rec := putSignedBundle(t, s, "secops/e2e", policyContent); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	raw := loadRawBundle(t, s, "secops/e2e")
	content, _ := raw["content"].(string)
	sigAny := raw[policyBundleSignatureKey]
	sig, ok := signatureFromMap(sigAny)
	if !ok {
		t.Fatal("missing signature on persisted bundle")
	}

	// Verify using the same bytes + signature via policysign.Verify.
	if err := policysign.Verify(pub, []byte(content), sig); err != nil {
		t.Fatalf("kernel-style verify failed: %v", err)
	}
}

// TestGateway_EnforceWithKeyAllowsSave verifies that enforce mode + a
// signing key attached results in a successful save (regression guard
// against overzealous 503s).
func TestGateway_EnforceWithKeyAllowsSave(t *testing.T) {
	setTestSigningEnv(t, "enforce")

	s, _, _ := newTestGateway(t)
	if rec := putSignedBundle(t, s, "secops/enforce-ok", policyContent); rec.Code != http.StatusOK {
		t.Fatalf("enforce+key should allow save; got %d %s", rec.Code, rec.Body.String())
	}
}

// TestGateway_ReSignOnContentChange proves that a re-save replaces a
// stale signature rather than leaving the previous one on the map.
// This is the invariant the kernel relies on when verifying.
func TestGateway_ReSignOnContentChange(t *testing.T) {
	setTestSigningEnv(t, "warn")

	s, _, _ := newTestGateway(t)
	putSignedBundle(t, s, "secops/mutate", policyContent)
	raw1 := loadRawBundle(t, s, "secops/mutate")
	sig1, _ := signatureFromMap(raw1[policyBundleSignatureKey])
	content1, _ := raw1["content"].(string)

	// SanitizePolicyBundleYAML re-serialises YAML (strips comments), so
	// the mutation has to change the YAML structure — not just add a
	// comment — otherwise the second save ends up with bitwise-identical
	// content and ed25519 signatures (which are deterministic).
	altered := `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
  - id: deny-secret
    match:
      topics:
        - secrets.*
    decision: deny
`
	putSignedBundle(t, s, "secops/mutate", altered)
	raw2 := loadRawBundle(t, s, "secops/mutate")
	sig2, _ := signatureFromMap(raw2[policyBundleSignatureKey])
	content2, _ := raw2["content"].(string)

	if content1 == content2 {
		t.Fatal("test setup broken: content did not change after mutation")
	}

	if sig1.Value == sig2.Value {
		t.Error("signature value should change when content changes")
	}
	if sig1.Hash == sig2.Hash {
		t.Error("hash should change when content changes")
	}
}

// TestPutPolicyBundle_ResponseSurfacesSignatureEnvelope is the wire-
// contract test for the MCP cordum_update_policy_bundle tool (task-
// 2d989055 reopen #2 issue 2). The bridge reads the signature sub-
// object from the PUT response to surface signed=true + key_id to
// the LLM without a second GET; if the handler ever stops emitting
// the envelope, the LLM loses the signing confirmation silently.
// This test pins the exact response shape.
func TestPutPolicyBundle_ResponseSurfacesSignatureEnvelope(t *testing.T) {
	setTestSigningEnv(t, "enforce")

	s, _, _ := newTestGateway(t)
	rec := putSignedBundle(t, s, "secops/response-envelope", policyContent)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := resp["id"]; got != "secops/response-envelope" {
		t.Errorf("id = %v want secops/response-envelope", got)
	}
	if _, ok := resp["updated_at"].(string); !ok {
		t.Errorf("updated_at missing or not a string: %v", resp["updated_at"])
	}

	sig, ok := resp["signature"].(map[string]any)
	if !ok {
		t.Fatalf("signature envelope missing from response; got %v", resp)
	}
	if got := sig["algorithm"]; got != policysign.AlgorithmEd25519 {
		t.Errorf("signature.algorithm = %v want %s", got, policysign.AlgorithmEd25519)
	}
	if got, _ := sig["key_id"].(string); got != "test" {
		t.Errorf("signature.key_id = %q want test", got)
	}
	if v, _ := sig["value"].(string); v == "" {
		t.Error("signature.value empty")
	}
	if h, _ := sig["hash"].(string); h == "" {
		t.Error("signature.hash empty")
	}
}

// TestPutPolicyBundle_ResponseOmitsSignatureWhenUnsigned complements
// the envelope test: strict=off with no key configured must not put
// a signature field on the response (the bridge would otherwise
// report signed=true for an unsigned bundle).
func TestPutPolicyBundle_ResponseOmitsSignatureWhenUnsigned(t *testing.T) {
	clearTestSigningEnv(t)
	t.Setenv(policysign.EnvStrictMode, "off")

	s, _, _ := newTestGateway(t)
	rec := putSignedBundle(t, s, "secops/unsigned-response", policyContent)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["signature"]; ok {
		t.Errorf("signature field should be absent for unsigned save; got %v", resp["signature"])
	}
}

// setTestSigningEnv generates a fresh Ed25519 key pair, exports the
// private key into CORDUM_POLICY_SIGNING_KEY, and returns the public
// half for verification. Uses t.Setenv so cleanup is automatic.
func setTestSigningEnv(t *testing.T, mode string) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clearTestSigningEnv(t)
	t.Setenv(policysign.EnvSigningKey, base64.StdEncoding.EncodeToString(priv))
	t.Setenv(policysign.EnvSigningKeyID, "test")
	t.Setenv(policysign.EnvStrictMode, mode)
	return pub, priv
}

// clearTestSigningEnv wipes any policy-signing environment variables so
// tests see a clean slate regardless of the host env.
func clearTestSigningEnv(t *testing.T) {
	t.Helper()
	t.Setenv(policysign.EnvSigningKey, "")
	t.Setenv(policysign.EnvSigningKeyPath, "")
	t.Setenv(policysign.EnvSigningKeyID, "")
	t.Setenv(policysign.EnvStrictMode, "")
}
