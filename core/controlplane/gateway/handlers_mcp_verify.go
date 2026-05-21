package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
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

// mcpVerifierState is the atomic-pointer payload stashed on *server.
// Holding both the verifier and the (possibly-non-nil) load error in
// one record lets the getter return either with a single atomic load,
// without races between "loaded" vs "loaded-with-error" bookkeeping.
type mcpVerifierState struct {
	verifier *outbound.Verifier
	err      error
}

// mcpVerifier lazily loads the trust store + builds a Verifier on first
// call and caches the result on the *server via atomic.Pointer. Pre-fix,
// the cache lived on a package-level sync.Once so the very first verify
// in any test or process pinned the trust store for the lifetime of the
// binary — operator key rotation was silently ignored and parallel
// server instances shared trust state. Operators trigger a re-read of
// the env via s.reloadMCPVerifier() (admin endpoint or SIGHUP).
//
// Nonce store defaults to in-memory — the verification endpoint is
// stateless from the caller's perspective so replay protection here
// applies per-gateway-process. For cross-replica HA, an operator can
// swap to RedisNonceStore via a future knob.
func (s *server) mcpVerifier() (*outbound.Verifier, error) {
	if s == nil {
		return nil, errors.New("mcp verify: server not initialized")
	}
	if loaded := s.mcpVerifierPtr.Load(); loaded != nil {
		return loaded.verifier, loaded.err
	}
	state := buildMCPVerifierFromEnv()
	// CompareAndSwap so concurrent first-callers don't trample each
	// other's freshly built verifier; the loser drops its copy and
	// returns the winner's published state instead.
	if s.mcpVerifierPtr.CompareAndSwap(nil, state) {
		return state.verifier, state.err
	}
	winner := s.mcpVerifierPtr.Load()
	return winner.verifier, winner.err
}

// reloadMCPVerifier clears the cached verifier so the next mcpVerifier()
// call rebuilds it from the current environment. Called after operator
// key rotation, fleet re-key, or any change to the
// CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID> environment vars.
func (s *server) reloadMCPVerifier() {
	if s == nil {
		return
	}
	s.mcpVerifierPtr.Store(nil)
}

// buildMCPVerifierFromEnv encapsulates the trust-store + verifier
// construction so the atomic.Pointer cache populates with a single
// composite value. Both fields are caller-visible: a nil verifier
// paired with a non-nil err drives the 503 with the remediation
// message; both non-nil never happens.
func buildMCPVerifierFromEnv() *mcpVerifierState {
	trust, err := outbound.LoadTrustStoreFromEnv()
	if err != nil {
		return &mcpVerifierState{err: err}
	}
	if len(trust) == 0 {
		return &mcpVerifierState{err: errors.New("mcp verify: no trusted keys configured (CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID>)")}
	}
	verifier, err := outbound.NewVerifier(trust, outbound.NewInMemoryNonceStore(), 5*time.Minute)
	if err != nil {
		return &mcpVerifierState{err: err}
	}
	return &mcpVerifierState{verifier: verifier}
}

// mcpVerifierPtrType is the atomic.Pointer field type — split out so the
// gateway.go server struct stays small. See server.go:130 for the field
// declaration.
type mcpVerifierPtrType = atomic.Pointer[mcpVerifierState]

// mcpVerifySignatureMaxBodyBytes caps the POST body the verify-signature
// endpoint accepts. The handler runs ecdsa.VerifyASN1 + sha256 on the
// claimed params; oversized bodies are a CPU-pin vector even from an
// admin-authenticated caller. 64 KiB is generous for a signed JSON-RPC
// envelope and well below the gateway's global maxBody middleware.
const mcpVerifySignatureMaxBodyBytes = int64(64 * 1024)

// handleMCPVerifySignature serves POST /api/v1/mcp/verify-signature.
func (s *server) handleMCPVerifySignature(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermMCPVerify, "admin") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpVerifySignatureMaxBodyBytes)
	var body mcpVerifySignatureRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// Sanitize: never echo the json decoder error verbatim. The Go
		// decoder text exposes struct field shape and the offending
		// byte position — both useful to an attacker probing the
		// handler. Log the detail server-side, return a generic 400.
		slog.Warn("mcp verify-signature: malformed JSON body", "error", err, "remote", r.RemoteAddr)
		writeJSONError(w, http.StatusBadRequest, errorCodeMCPVerifyRequestInvalid, "request body is not valid JSON")
		return
	}
	if body.Method == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeMCPVerifyRequestInvalid, "method required")
		return
	}
	verifier, err := s.mcpVerifier()
	if err != nil || verifier == nil {
		slog.Warn("mcp verify endpoint called without trust store", "error", err)
		writeJSONError(w, http.StatusServiceUnavailable, errorCodeMCPVerifierUnavailable, "no trusted public keys — set CORDUM_MCP_INBOUND_TRUSTED_KEY_<ID> and restart")
		return
	}
	verifyErr := verifier.VerifyRequest(r.Context(), body.Headers, body.Method, []byte(body.Params))
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
