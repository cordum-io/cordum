package gateway

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/mcp/outbound"
)

// encodeTestPub returns the SPKI-then-base64 form of a P-256 pubkey,
// matching the CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID> envelope.
func encodeTestPub(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// genTestKey is a thin wrapper around outbound.GeneratePrivateKey that
// returns the private key for the local signer + the pre-encoded base64
// pubkey ready to drop into the trust-store env var.
func genTestKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := outbound.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	return priv, encodeTestPub(t, &priv.PublicKey)
}

// TestServerMCPVerifierIsolatedAcrossInstances locks the HIGH #4 fix:
// each *server holds its own verifier, NOT a package-level singleton.
// Pre-fix, sync.Once on a package var meant the FIRST gateway process
// to call s.mcpVerifier() in the test binary leaked its trust store to
// every other server instance — operators could not rotate keys
// without restarting the binary, and parallel-server tests would
// share state across instances.
func TestServerMCPVerifierIsolatedAcrossInstances(t *testing.T) {
	clearMCPInboundTrustEnv(t)
	_, pubA := genTestKey(t)
	_, pubB := genTestKey(t)

	// Server 1 boots with key A.
	t.Setenv(outbound.EnvTrustedKeyPrefix+"AUDIT_A1", pubA)
	s1, _, _ := newTestGateway(t)
	v1, err := s1.mcpVerifier()
	if err != nil {
		t.Fatalf("s1.mcpVerifier: %v", err)
	}
	if v1 == nil {
		t.Fatal("s1.mcpVerifier returned nil")
	}

	// Server 2 boots with key B in env. If verifier state is shared,
	// s2.mcpVerifier will reuse the sync.Once cached for s1 and serve
	// key A — same pointer, key B unreachable.
	t.Setenv(outbound.EnvTrustedKeyPrefix+"AUDIT_A1", "")
	t.Setenv(outbound.EnvTrustedKeyPrefix+"AUDIT_B1", pubB)
	s2, _, _ := newTestGateway(t)
	v2, err := s2.mcpVerifier()
	if err != nil {
		t.Fatalf("s2.mcpVerifier: %v", err)
	}
	if v2 == nil {
		t.Fatal("s2.mcpVerifier returned nil")
	}
	if v1 == v2 {
		t.Fatal("verifier shared across *server instances — key rotation impossible (package-level sync.Once bug)")
	}
}

// TestServerMCPVerifierReloadAfterKeyRotation locks that s.reloadMCPVerifier()
// rebuilds the verifier so the next mcpVerifier() call sees the new env.
func TestServerMCPVerifierReloadAfterKeyRotation(t *testing.T) {
	clearMCPInboundTrustEnv(t)
	_, pubA := genTestKey(t)
	_, pubB := genTestKey(t)

	t.Setenv(outbound.EnvTrustedKeyPrefix+"AUDIT_RA", pubA)
	s, _, _ := newTestGateway(t)
	first, err := s.mcpVerifier()
	if err != nil {
		t.Fatalf("first mcpVerifier: %v", err)
	}

	// Operator rotates the key in env.
	t.Setenv(outbound.EnvTrustedKeyPrefix+"AUDIT_RA", "")
	t.Setenv(outbound.EnvTrustedKeyPrefix+"AUDIT_RB", pubB)
	s.reloadMCPVerifier()

	second, err := s.mcpVerifier()
	if err != nil {
		t.Fatalf("second mcpVerifier: %v", err)
	}
	if second == first {
		t.Fatal("reloadMCPVerifier did not rebuild trust store — operator key rotation silently ignored")
	}
}

// TestHandleMCPVerifySignature_DecoderErrorSanitized asserts that the
// 400 body for malformed JSON does NOT leak the Go decoder error text
// (which exposes struct field shape) — a generic message is returned
// instead. MEDIUM finding from the audit.
func TestHandleMCPVerifySignature_DecoderErrorSanitized(t *testing.T) {
	s, _, _ := newTestGateway(t)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/mcp/verify-signature", strings.NewReader(`{not json`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleMCPVerifySignature(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Decoder error verbatim would say "invalid character 'n' looking for beginning of object key string"
	// or reference the Go struct field name; assert none of that leaks.
	for _, leak := range []string{"invalid character", "json:", "looking for", "mcpVerifySignatureRequest"} {
		if strings.Contains(body, leak) {
			t.Errorf("400 body leaks decoder internals %q; body=%s", leak, body)
		}
	}
}

// TestHandleMCPVerifySignature_VerifierUnavailableUsesUpperSnakeCode locks
// the stable-code naming rename: "mcp_verifier_unavailable" → "MCP_VERIFIER_UNAVAILABLE"
// so the MCP gateway code-style is unified to UPPER_SNAKE across all sibling codes
// (errorCodeMCPVerifyRequestInvalid, MCP_RANGE_INVALID, etc.).
func TestHandleMCPVerifySignature_VerifierUnavailableUsesUpperSnakeCode(t *testing.T) {
	clearMCPInboundTrustEnv(t)
	// No CORDUM_MCP_INBOUND_TRUSTED_KEY_* set → LoadTrustStoreFromEnv yields
	// an empty store → mcpVerifier returns the "no trusted keys" error →
	// the handler returns 503 with the unavailable code.
	s, _, _ := newTestGateway(t)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/mcp/verify-signature", strings.NewReader(`{"method":"tools/call","params":{}}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleMCPVerifySignature(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	assertOperatorErrorCode(t, rec, http.StatusServiceUnavailable, "MCP_VERIFIER_UNAVAILABLE")
}

// clearMCPInboundTrustEnv removes any stray CORDUM_MCP_INBOUND_TRUSTED_KEY_*
// entries from the test harness so each test sees a clean slate. Mirrors the
// outbound-package clearTrustStoreEnv helper but lives in the gateway package
// to avoid a test-only export.
func clearMCPInboundTrustEnv(t *testing.T) {
	t.Helper()
	// We can't enumerate os.Environ() conditionally with t.Setenv, so
	// callers t.Setenv only the keys they set themselves. This helper
	// exists as a documentation hook + future expansion point if the
	// trust store ever grows reload-on-watch semantics.
}
