package scheduler

// SessionTokenMiddleware is the scheduler-side companion to the
// Agent's outbound-token attachment (cap/sdk/go/runtime/session_attach.go).
// Every inbound packet the middleware inspects is checked against the
// SessionTokenIssuer; the outcome is gated by CORDUM_SDK_HANDSHAKE via
// the HandshakeMode enum so operators can stage the rollout:
//
//   HandshakeModeOff     — middleware is a no-op; every packet passes.
//   HandshakeModeWarn    — token verified when present; a single
//                          ERROR per worker per hour fires for
//                          handshakeless packets; dispatch still
//                          proceeds.
//   HandshakeModeEnforce — token MUST verify; missing/invalid/revoked
//                          tokens are rejected.
//
// The middleware never writes — it reads SessionTokenIssuer state and
// returns a verdict. Callers decide what to do with the verdict: the
// engine's heartbeat path drops a rejected packet, the job-result
// path rejects the result as unauthorised, etc. Keeping the
// middleware as a pure function means the engine integration can
// evolve without this file moving.

import (
	"context"
	"errors"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// TokenVerdict is the decision the middleware returns for an inbound
// packet. Callers pattern-match on the verdict to decide whether to
// admit, log-and-admit, or reject the packet.
type TokenVerdict int

const (
	// TokenVerdictPass means the token verified, or the mode is Off
	// and no verification was performed. Caller admits the packet.
	TokenVerdictPass TokenVerdict = iota
	// TokenVerdictWarnMissing means the mode is Warn and no token was
	// present. Caller admits the packet; the middleware has already
	// fired a rate-limited ERROR log.
	TokenVerdictWarnMissing
	// TokenVerdictRejectMissing means the mode is Enforce and no
	// token was present. Caller MUST drop the packet.
	TokenVerdictRejectMissing
	// TokenVerdictRejectInvalid means a token was present but failed
	// signature, expiry, or revocation checks. Caller MUST drop the
	// packet regardless of mode — a tampered token is always a hard
	// fail.
	TokenVerdictRejectInvalid
)

// String renders the verdict for logs and audit events.
func (v TokenVerdict) String() string {
	switch v {
	case TokenVerdictPass:
		return "pass"
	case TokenVerdictWarnMissing:
		return "warn_missing"
	case TokenVerdictRejectMissing:
		return "reject_missing"
	case TokenVerdictRejectInvalid:
		return "reject_invalid"
	default:
		return "unknown"
	}
}

// TokenVerificationResult bundles the verdict with the verified
// claims (when available) and the error (when any). Callers that
// need to log "why" (audit events, error surfaces) use the non-nil
// Err; callers that need to correlate with a workflow use Claims.
type TokenVerificationResult struct {
	Verdict TokenVerdict
	Claims  *SessionTokenClaims
	Err     error
}

// SessionTokenMiddleware wires the issuer + mode + rate-limit tracker
// into a single verify call. All fields are required in production;
// nil issuer degrades the middleware to a no-op (Off mode semantics)
// so unit tests don't need Redis.
type SessionTokenMiddleware struct {
	issuer  *SessionTokenIssuer
	mode    HandshakeMode
	missing *HandshakeMissingTracker
}

// NewSessionTokenMiddleware builds a middleware. A nil issuer makes
// every call return TokenVerdictPass (the middleware is effectively
// disabled); pass a real *SessionTokenIssuer in production.
func NewSessionTokenMiddleware(issuer *SessionTokenIssuer, mode HandshakeMode, missing *HandshakeMissingTracker) *SessionTokenMiddleware {
	return &SessionTokenMiddleware{issuer: issuer, mode: mode, missing: missing}
}

// Mode returns the active mode. Useful for boot logs and test assertions.
func (m *SessionTokenMiddleware) Mode() HandshakeMode {
	if m == nil {
		return HandshakeModeOff
	}
	return m.mode
}

// Verify inspects the inbound packet. The packet's unknown field 18
// (BytesType) is the session token — parsed via authTokenFromPacket
// so we reuse the exact wire contract the scheduler's heartbeat path
// already consumes.
//
// The workerID argument scopes the rate-limit tracker in warn mode so
// a noisy worker doesn't flood the logs.
func (m *SessionTokenMiddleware) Verify(ctx context.Context, workerID string, packet *pb.BusPacket) TokenVerificationResult {
	if m == nil || m.issuer == nil || m.mode == HandshakeModeOff {
		return TokenVerificationResult{Verdict: TokenVerdictPass}
	}
	token := strings.TrimSpace(extractSessionToken(packet))
	if token == "" {
		return m.handleMissing(workerID)
	}
	claims, err := m.issuer.Verify(ctx, token, true)
	if err != nil {
		return TokenVerificationResult{
			Verdict: TokenVerdictRejectInvalid,
			Err:     err,
		}
	}
	return TokenVerificationResult{
		Verdict: TokenVerdictPass,
		Claims:  &claims,
	}
}

// handleMissing picks the verdict for a token-less packet. Enforce:
// reject. Warn: rate-limited log, then pass. Off: impossible, short-
// circuited upstream.
func (m *SessionTokenMiddleware) handleMissing(workerID string) TokenVerificationResult {
	switch m.mode {
	case HandshakeModeEnforce:
		return TokenVerificationResult{
			Verdict: TokenVerdictRejectMissing,
			Err:     errors.New("scheduler: session token missing in enforce mode"),
		}
	case HandshakeModeWarn:
		// ShouldLog returns true the first time this worker is seen
		// or when the per-worker interval has elapsed. The verdict
		// carries the bool back to the caller via the Err field —
		// nil Err means "admit silently", non-nil means "admit but
		// log the reason".
		if m.missing.ShouldLog(workerID) {
			return TokenVerificationResult{
				Verdict: TokenVerdictWarnMissing,
				Err:     errors.New("scheduler: session token missing in warn mode"),
			}
		}
		return TokenVerificationResult{Verdict: TokenVerdictWarnMissing}
	default:
		return TokenVerificationResult{Verdict: TokenVerdictPass}
	}
}

// extractSessionToken is the scheduler-side mirror of the cap/sdk/go
// runtime.ExtractSessionToken. Keeping a local copy avoids a module
// cycle (scheduler can't import cap/sdk/go/runtime).
func extractSessionToken(packet *pb.BusPacket) string {
	if packet == nil {
		return ""
	}
	// Typed field first: cap/v2 SDK workers set the declared
	// BusPacket.auth_token (field 18) via runtime.attachSessionToken, so
	// after a marshal/reparse off the bus the token lives here and the
	// unknown set is empty. Mirrors cap/sdk/go runtime.ExtractSessionToken.
	if token := packet.GetAuthToken(); token != "" {
		return token
	}
	// Legacy fallback: older senders encoded the token as an unknown
	// field. Retained so un-upgraded workers keep verifying.
	raw := packet.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		fieldNum, wireType, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return ""
		}
		raw = raw[tagLen:]
		if fieldNum == sessionTokenPacketField && wireType == protowire.BytesType {
			value, valueLen := protowire.ConsumeBytes(raw)
			if valueLen < 0 {
				return ""
			}
			return string(value)
		}
		valueLen := protowire.ConsumeFieldValue(fieldNum, wireType, raw)
		if valueLen < 0 {
			return ""
		}
		raw = raw[valueLen:]
	}
	return ""
}

// sessionTokenPacketField is the protobuf field number carrying the
// session token on BusPacket unknown bytes. Mirrored from cap/sdk/go
// — any change here must land in both sides simultaneously or packets
// go dark on the wire.
const sessionTokenPacketField = 18
