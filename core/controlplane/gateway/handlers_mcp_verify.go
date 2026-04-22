package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp/outbound"
)

// handlers_mcp_verify.go exposes POST /api/v1/mcp/verify-signature for
// external MCP servers (or adjacent Cordum clusters) that want to
// verify a signature without shipping their own ECDSA verifier. This
// satisfies DoD item #4 of task-ba236f62.
//
// The endpoint is admin-gated because the trust store is shared by
// the whole cluster and operators should not expose it to unauthenticated
// callers. Inbound verification of the gateway's OWN MCP SSE/Message
// endpoints lives in handlers_mcp.go (CORDUM_MCP_VERIFY_INBOUND gate).

// mcpVerifySignatureRequest is the POST body shape. Callers submit the
// same fields the Signer emitted: method + params + the 6 signed
// headers. Server rebuilds the canonical message and calls
// Verifier.VerifyRequest.
type mcpVerifySignatureRequest struct {
	Method  string            `json:"method"`
	Params  json.RawMessage   `json:"params"`
	Headers map[string]string `json:"headers"`
}

// mcpVerifySignatureResponse is the uniform reply. ok=true when the
// signature verified against the trust store; otherwise a stable
// sub_reason tells the caller exactly which check failed.
type mcpVerifySignatureResponse struct {
	OK        bool   `json:"ok"`
	SubReason string `json:"sub_reason,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
	Tenant    string `json:"tenant,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// mcpVerifierBuilder lazy-builds the Verifier from env on first use.
// Exposed as a field on server via lazy getter so dev deploys without
// the trust-store env vars never construct one — and the handler
// returns a 503 with a clear remediation hint instead of 500.
type mcpVerifierBuilder struct {
	once     sync.Once
	verifier *outbound.Verifier
	err      error
}

var mcpVerifierSingleton mcpVerifierBuilder

// mcpVerifier lazily loads the trust store + builds a Verifier.
// Nonce store defaults to in-memory — the verification endpoint is
// stateless from the caller's perspective so replay protection here
// applies per-gateway-process. For cross-replica HA, an operator can
// swap to RedisNonceStore via a future knob.
func (s *server) mcpVerifier() (*outbound.Verifier, error) {
	mcpVerifierSingleton.once.Do(func() {
		trust, err := outbound.LoadTrustStoreFromEnv()
		if err != nil {
			mcpVerifierSingleton.err = err
			return
		}
		if len(trust) == 0 {
			mcpVerifierSingleton.err = errors.New("mcp verify: no trusted keys configured (CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>)")
			return
		}
		verifier, err := outbound.NewVerifier(trust, outbound.NewInMemoryNonceStore(), 5*time.Minute)
		if err != nil {
			mcpVerifierSingleton.err = err
			return
		}
		mcpVerifierSingleton.verifier = verifier
	})
	return mcpVerifierSingleton.verifier, mcpVerifierSingleton.err
}

// handleMCPVerifySignature serves POST /api/v1/mcp/verify-signature.
func (s *server) handleMCPVerifySignature(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermMCPVerify, "admin") {
		return
	}
	var body mcpVerifySignatureRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid json body: "+err.Error())
		return
	}
	if body.Method == "" {
		writeErrorJSON(w, http.StatusBadRequest, "method required")
		return
	}
	verifier, err := s.mcpVerifier()
	if err != nil || verifier == nil {
		slog.Warn("mcp verify endpoint called without trust store", "error", err)
		writeJSONError(w, http.StatusServiceUnavailable, "mcp_verifier_unavailable", "no trusted public keys — set CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID> and restart")
		return
	}
	verifyErr := verifier.VerifyRequest(body.Headers, body.Method, []byte(body.Params))
	resp := mcpVerifySignatureResponse{
		KeyID:   body.Headers[outbound.HeaderKeyID],
		Tenant:  body.Headers[outbound.HeaderTenant],
		AgentID: body.Headers[outbound.HeaderAgentID],
	}
	if verifyErr == nil {
		resp.OK = true
		writeJSONObject(w, http.StatusOK, resp)
		return
	}
	resp.SubReason = verifySubReason(verifyErr)
	// Epic rail "All MCP tool invocations must produce audit events" —
	// every failed verify lands a mcp.signature_invalid SIEMEvent in
	// the tenant audit chain. Sub_reason is the stable machine-readable
	// sentinel; reason carries the wrapped error detail.
	s.emitMCPSignatureInvalid(resp, verifyErr)
	writeJSONObject(w, http.StatusOK, resp)
}

// emitMCPSignatureInvalid fires the SIEM event on a verify failure.
// Nil-safe if s.auditExporter is not yet wired (dev mode) — operators
// see the endpoint-level 200 response with sub_reason regardless.
func (s *server) emitMCPSignatureInvalid(resp mcpVerifySignatureResponse, verifyErr error) {
	if s == nil || s.auditExporter == nil {
		return
	}
	extra := map[string]string{
		"sub_reason": resp.SubReason,
		"key_id":     resp.KeyID,
	}
	if verifyErr != nil {
		extra["detail"] = verifyErr.Error()
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventMCPSignatureInvalid,
		Severity:  audit.SeverityMedium,
		TenantID:  resp.Tenant,
		AgentID:   resp.AgentID,
		Action:    "verify_failed",
		Reason:    resp.SubReason,
		Extra:     extra,
	})
}

// verifySubReason maps a verify error to a stable string the client
// can pattern-match. Uses errors.Is so wrapped errors still classify
// correctly.
func verifySubReason(err error) string {
	switch {
	case errors.Is(err, outbound.ErrMissingHeaders):
		return "missing"
	case errors.Is(err, outbound.ErrMalformedHeader):
		return "malformed"
	case errors.Is(err, outbound.ErrTimestampExpired):
		return "expired"
	case errors.Is(err, outbound.ErrNonceReplayed):
		return "replayed"
	case errors.Is(err, outbound.ErrUntrustedKey):
		return "untrusted_key"
	case errors.Is(err, outbound.ErrSignatureInvalid):
		return "bad_signature"
	default:
		return "error"
	}
}
