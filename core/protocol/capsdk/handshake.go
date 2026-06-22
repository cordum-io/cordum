package capsdk

// Phase-2 boundary-hardening worker-handshake protocol, mirrored from
// cap/sdk/go/handshake.go. The cap v2.9.0 module that cordum pins does
// not yet contain these types; this file re-states the contract so the
// scheduler and Agent.Start path can consume it today. When a cap
// release shipping the types lands, this file may be deleted and the
// cap/sdk/go import restored.
//
// CONTRACT: the wire encoding here MUST remain byte-for-byte identical
// to the cap sibling. Tests in cap/sdk/go already exercise round-trip
// behaviour; the sibling tests in cordum's scheduler exercise the
// consumer side. Diverging field names or tags silently breaks cross-
// SDK decode.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WorkerHandshakeSubject is the NATS subject the worker publishes its
// HandshakeRequest on at Agent.Start.
const WorkerHandshakeSubject = "sys.worker.handshake"

// WorkerHandshakeRenewSubject is the NATS subject for renew requests.
const WorkerHandshakeRenewSubject = "sys.worker.handshake.renew"

// WorkerHandshakeMaxSkew bounds the clock-skew tolerance between the
// worker's HandshakeRequest.Timestamp and the scheduler's wall clock.
const WorkerHandshakeMaxSkew = 60 * time.Second

// WorkerHandshakeNonceLength is the minimum nonce byte count workers
// must supply.
const WorkerHandshakeNonceLength = 16

// HandshakeRequest is the worker's initial identity + capability
// assertion.
type HandshakeRequest struct {
	AgentID      string    `json:"agent_id"`
	Tenant       string    `json:"tenant"`
	SDKVersion   string    `json:"sdk_version"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Nonce        string    `json:"nonce"`
	RequestID    string    `json:"request_id"`
	Timestamp    time.Time `json:"timestamp"`
}

// HandshakeResponse is the scheduler's reply.
type HandshakeResponse struct {
	// SessionToken is a bearer secret. The `redact:"true"` struct tag marks
	// it for a future bus/log sanitiser to strip — there is no active
	// consumer of the tag today (it is a forward marker). Callers MUST
	// NEVER log this field raw regardless of the tag.
	SessionToken string    `json:"session_token,omitempty" redact:"true"`
	TokenExp     time.Time `json:"token_exp,omitempty"`
	RequestID    string    `json:"request_id"`
	Rejected     bool      `json:"rejected,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

// HandshakeReject* are the canonical rejection reasons.
const (
	HandshakeRejectUnknownAgent     = "unknown_agent"
	HandshakeRejectTenantMismatch   = "tenant_mismatch"
	HandshakeRejectReplay           = "replay_detected"
	HandshakeRejectClockSkew        = "clock_skew"
	HandshakeRejectCapabilityDenied = "capability_denied"
	HandshakeRejectSDKTooOld        = "sdk_too_old"
	HandshakeRejectInternalError    = "internal_error"
	HandshakeRejectMalformedRequest = "malformed_request"
	HandshakeRejectInvalidSignature = "invalid_signature"
)

// MarshalHandshakeRequest serialises the request as JSON bytes.
func MarshalHandshakeRequest(req *HandshakeRequest) ([]byte, error) {
	if err := ValidateHandshakeRequest(req); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal handshake request: %w", err)
	}
	return raw, nil
}

// UnmarshalHandshakeRequest parses JSON bytes into a HandshakeRequest.
func UnmarshalHandshakeRequest(raw []byte) (*HandshakeRequest, error) {
	if len(raw) == 0 {
		return nil, errors.New("handshake: empty payload")
	}
	var req HandshakeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("handshake: parse: %w", err)
	}
	if err := ValidateHandshakeRequest(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

// MarshalHandshakeResponse serialises the response.
func MarshalHandshakeResponse(resp *HandshakeResponse) ([]byte, error) {
	if resp == nil {
		return nil, errors.New("handshake: nil response")
	}
	if strings.TrimSpace(resp.RequestID) == "" {
		return nil, errors.New("handshake: response missing request_id")
	}
	if !resp.Rejected && strings.TrimSpace(resp.SessionToken) == "" {
		return nil, errors.New("handshake: accepted response missing session_token")
	}
	if resp.Rejected && strings.TrimSpace(resp.Reason) == "" {
		return nil, errors.New("handshake: rejected response missing reason")
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal handshake response: %w", err)
	}
	return raw, nil
}

// UnmarshalHandshakeResponse parses JSON bytes into a HandshakeResponse.
func UnmarshalHandshakeResponse(raw []byte) (*HandshakeResponse, error) {
	if len(raw) == 0 {
		return nil, errors.New("handshake: empty payload")
	}
	var resp HandshakeResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("handshake: parse response: %w", err)
	}
	if strings.TrimSpace(resp.RequestID) == "" {
		return nil, errors.New("handshake: response missing request_id")
	}
	return &resp, nil
}

// ValidateHandshakeRequest enforces the required-field contract.
func ValidateHandshakeRequest(req *HandshakeRequest) error {
	if req == nil {
		return errors.New("handshake: nil request")
	}
	var missing []string
	if strings.TrimSpace(req.AgentID) == "" {
		missing = append(missing, "agent_id")
	}
	if strings.TrimSpace(req.Tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(req.SDKVersion) == "" {
		missing = append(missing, "sdk_version")
	}
	if strings.TrimSpace(req.Nonce) == "" {
		missing = append(missing, "nonce")
	} else if len([]byte(req.Nonce)) < WorkerHandshakeNonceLength {
		return fmt.Errorf("handshake: nonce too short (got %d bytes, want >= %d)", len(req.Nonce), WorkerHandshakeNonceLength)
	}
	if strings.TrimSpace(req.RequestID) == "" {
		missing = append(missing, "request_id")
	}
	if req.Timestamp.IsZero() {
		missing = append(missing, "timestamp")
	}
	if len(missing) > 0 {
		return fmt.Errorf("handshake: missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}
