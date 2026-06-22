package scheduler

// Phase-2 boundary-hardening worker handshake handler.
//
// HandshakeService is the scheduler-side counterpart to the Agent.Start
// handshake exchange. It accepts a JSON-encoded HandshakeRequest (the
// payload the worker publishes on cap/sdk/go.WorkerHandshakeSubject),
// authoritatively validates it, mints a session token via
// SessionTokenIssuer, and returns a JSON-encoded HandshakeResponse for
// publication on the per-request reply inbox.
//
// The handler is bus-transport agnostic: tests exercise it directly via
// HandleHandshake/HandleRenew. A thin Bus subscription helper at the
// edge (next-step task) is responsible for unwrapping inbound BusPackets
// and publishing the reply on the worker's chosen reply subject.
//
// Trust boundary checks performed in order:
//
//   1. JSON parse + capsdk.ValidateHandshakeRequest (required fields,
//      nonce length).
//   2. Clock skew vs WorkerHandshakeMaxSkew. Outside the window →
//      HandshakeRejectClockSkew.
//   3. Nonce uniqueness via NonceStore.Claim — replays rejected with
//      HandshakeRejectReplay.
//   4. Identity lookup via AgentIdentityResolver.Get. Missing →
//      HandshakeRejectUnknownAgent. Tenant mismatch →
//      HandshakeRejectTenantMismatch. Status revoked/suspended →
//      HandshakeRejectCapabilityDenied.
//   5. SessionTokenIssuer.Issue mints a fresh token whose JTI invalidates
//      the prior single-active-token record (see session_token.go).
//
// Every terminal outcome — accept OR reject — emits a worker_handshake
// SIEMEvent so SOC2 audit is uniform across both paths. Internal errors
// produce a HandshakeRejectInternalError reply but never panic the
// subscription goroutine.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	"github.com/redis/go-redis/v9"
)

// EventWorkerHandshake is the SIEMEvent EventType emitted on every
// terminal handshake/renew/revoke outcome. SIEM joins on Extra.outcome
// (accepted | rejected | renewed | revoked) and Extra.reason for
// rejected events.
const EventWorkerHandshake = "worker_handshake"

// Subjects are mirrored from cap/sdk/go for code completion convenience.
// The cap constants remain the source of truth (publishers + subscribers
// must use the same string).
const (
	HandshakeSubject      = capsdk.WorkerHandshakeSubject
	HandshakeRenewSubject = capsdk.WorkerHandshakeRenewSubject
	defaultNonceTTL       = 2 * time.Minute
	nonceKeyPrefix        = "session:nonce:"
)

// AgentIdentityResolver is the narrow interface the handler depends on.
// store.AgentIdentityStore satisfies it directly.
type AgentIdentityResolver interface {
	Get(ctx context.Context, id string) (*store.AgentIdentity, error)
}

// NonceStore claims a (tenant, nonce) pair atomically. Returns true on
// first claim, false on replay. Errors are infrastructure faults (Redis
// outage); callers map them to HandshakeRejectInternalError.
type NonceStore interface {
	Claim(ctx context.Context, tenant, nonce string, ttl time.Duration) (bool, error)
}

// AuditSink is the narrow interface for emitting worker_handshake events.
// audit.Exporter satisfies a richer contract; we accept just the slice
// form so tests can record events without standing up the full pipeline.
type AuditSink interface {
	Emit(ctx context.Context, event audit.SIEMEvent)
}

// HandshakeService validates worker handshake requests and mints session
// tokens. It is safe for concurrent use; all dependencies must be
// concurrency-safe themselves.
type HandshakeService struct {
	issuer     *SessionTokenIssuer
	identities AgentIdentityResolver
	nonces     NonceStore
	audit      AuditSink
	skew       time.Duration
	nonceTTL   time.Duration
	now        func() time.Time
}

// HandshakeServiceOptions tunes a HandshakeService. Zero-valued fields
// fall back to documented defaults.
type HandshakeServiceOptions struct {
	// Skew tolerated between worker timestamp and scheduler clock.
	// Defaults to capsdk.WorkerHandshakeMaxSkew (60s).
	Skew time.Duration
	// NonceTTL bounds how long a nonce remains in the replay-protection
	// store. Should be at least 2x Skew so a request landing at the
	// edge of the skew window cannot be replayed seconds later.
	NonceTTL time.Duration
	// Now lets tests inject a deterministic clock.
	Now func() time.Time
}

// NewHandshakeService constructs a HandshakeService.
//
// issuer must be non-nil; identities must be non-nil; nonces may be nil
// for in-memory tests but production callers MUST supply a Redis-backed
// store (replay protection is load-bearing). audit may be nil to
// silently drop events (warned at boot only — never the production path).
func NewHandshakeService(issuer *SessionTokenIssuer, identities AgentIdentityResolver, nonces NonceStore, sink AuditSink, opts HandshakeServiceOptions) (*HandshakeService, error) {
	if issuer == nil {
		return nil, errors.New("scheduler: handshake service requires a session token issuer")
	}
	if identities == nil {
		return nil, errors.New("scheduler: handshake service requires an identity resolver")
	}
	skew := opts.Skew
	if skew <= 0 {
		skew = capsdk.WorkerHandshakeMaxSkew
	}
	ttl := opts.NonceTTL
	if ttl <= 0 {
		ttl = defaultNonceTTL
	}
	if ttl < 2*skew {
		// A nonce TTL shorter than 2x skew window leaves a replay gap
		// at the edges — extend silently rather than failing the boot.
		ttl = 2 * skew
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &HandshakeService{
		issuer:     issuer,
		identities: identities,
		nonces:     nonces,
		audit:      sink,
		skew:       skew,
		nonceTTL:   ttl,
		now:        now,
	}, nil
}

// HandleHandshake processes a worker handshake request.
//
// raw is the JSON payload the worker published. The returned bytes are
// the marshalled HandshakeResponse the caller should publish on the
// reply subject; both accepted and rejected outcomes return non-nil
// bytes. err is non-nil only for marshal failures the worker cannot
// recover from (the caller logs and drops the message in that case).
func (s *HandshakeService) HandleHandshake(ctx context.Context, raw []byte) ([]byte, error) {
	req, validation := s.parseRequest(raw)
	if validation != nil {
		return s.reject(ctx, requestIDFor(req), tenantFor(req), agentIDFor(req), sdkVerFor(req), capsdk.HandshakeRejectMalformedRequest, validation.Error())
	}
	return s.process(ctx, req, false)
}

// HandleRenew processes a renew request. Wire format identical to
// initial handshake; the difference is the issuer.Renew path which
// validates the previous token via Capabilities[0] (carrier convention,
// see Agent.Renew).
//
// For now we treat renew as functionally identical to a fresh handshake
// when accompanied by a valid request — actual token-state rotation
// happens inside SessionTokenIssuer.Issue (which auto-revokes the prior
// JTI). Step-7 will add a token-bearing renew path that uses
// SessionTokenIssuer.Renew directly.
func (s *HandshakeService) HandleRenew(ctx context.Context, raw []byte) ([]byte, error) {
	req, validation := s.parseRequest(raw)
	if validation != nil {
		return s.reject(ctx, requestIDFor(req), tenantFor(req), agentIDFor(req), sdkVerFor(req), capsdk.HandshakeRejectMalformedRequest, validation.Error())
	}
	return s.process(ctx, req, true)
}

func (s *HandshakeService) parseRequest(raw []byte) (*capsdk.HandshakeRequest, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty handshake payload")
	}
	req, err := capsdk.UnmarshalHandshakeRequest(raw)
	if err != nil {
		// Try to extract request_id for correlation in the rejection
		// reply even when the request was malformed enough to fail
		// validation. Best-effort; ignore parse errors here.
		var partial capsdk.HandshakeRequest
		_ = json.Unmarshal(raw, &partial)
		if partial.RequestID != "" {
			return &partial, err
		}
		return nil, err
	}
	return req, nil
}

func (s *HandshakeService) process(ctx context.Context, req *capsdk.HandshakeRequest, isRenew bool) ([]byte, error) {
	// Reserve the control-plane service identities: no worker may handshake AS
	// a control-plane service (cordum-scheduler / api-gateway / workflow-engine)
	// and thereby obtain a real, signed session token whose Subject is a
	// reserved identity. Together with the typ assertion on the verify path,
	// this is what makes the service-token scheme non-spoofable: a worker can
	// neither mint a service token (no key) nor be issued a worker token whose
	// subject impersonates a control-plane service. Checked first so it never
	// consumes a nonce or an identity lookup.
	if servicetoken.IsReservedIdentity(req.AgentID) {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectCapabilityDenied, "agent id is reserved for control-plane service tokens")
	}
	now := s.now().UTC()
	skew := req.Timestamp.Sub(now)
	if skew > s.skew || -skew > s.skew {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectClockSkew, fmt.Sprintf("clock skew %s outside window %s", skew, s.skew))
	}
	if s.nonces != nil {
		ok, err := s.nonces.Claim(ctx, req.Tenant, req.Nonce, s.nonceTTL)
		if err != nil {
			return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectInternalError, fmt.Sprintf("nonce store: %v", err))
		}
		if !ok {
			return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectReplay, "nonce already claimed")
		}
	}
	identity, err := s.identities.Get(ctx, req.AgentID)
	if err != nil {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectInternalError, fmt.Sprintf("identity lookup: %v", err))
	}
	if identity == nil {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectUnknownAgent, "agent not registered")
	}
	if identity.Status != "" && identity.Status != "active" {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectCapabilityDenied, "identity status "+identity.Status)
	}
	// Tenant binding: identity Owner is the canonical owning tenant in
	// the current data model. Reject if the request claims a tenant
	// the identity does not own.
	if !tenantMatches(identity, req.Tenant) {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectTenantMismatch, "request tenant does not match identity owner")
	}
	token, claims, err := s.issuer.Issue(ctx, req.AgentID, req.Tenant, req.SDKVersion)
	if err != nil {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectInternalError, fmt.Sprintf("issue session token: %v", err))
	}
	resp := capsdk.HandshakeResponse{
		SessionToken: token,
		TokenExp:     claims.ExpiresAt,
		RequestID:    req.RequestID,
	}
	body, err := capsdk.MarshalHandshakeResponse(&resp)
	if err != nil {
		return s.reject(ctx, req.RequestID, req.Tenant, req.AgentID, req.SDKVersion, capsdk.HandshakeRejectInternalError, fmt.Sprintf("marshal response: %v", err))
	}
	outcome := "accepted"
	if isRenew {
		outcome = "renewed"
	}
	s.emit(ctx, audit.SIEMEvent{
		Timestamp: s.now().UTC(),
		EventType: EventWorkerHandshake,
		Severity:  audit.SeverityInfo,
		TenantID:  req.Tenant,
		AgentID:   req.AgentID,
		AgentName: identity.Name,
		Action:    "worker.handshake",
		Decision:  "accept",
		Extra: map[string]string{
			"outcome": outcome,
			"sdk_ver": req.SDKVersion,
			"jti":     claims.JTI,
		},
	})
	return body, nil
}

func (s *HandshakeService) reject(ctx context.Context, requestID, tenant, agentID, sdkVer, reason, detail string) ([]byte, error) {
	resp := capsdk.HandshakeResponse{
		RequestID: orPlaceholder(requestID),
		Rejected:  true,
		Reason:    reason,
	}
	body, err := capsdk.MarshalHandshakeResponse(&resp)
	if err != nil {
		// Fall back to a hand-rolled JSON reply rather than dropping
		// the worker entirely — the worker still needs *some* parseable
		// signal to fail loud.
		fallback, _ := json.Marshal(map[string]any{
			"request_id": orPlaceholder(requestID),
			"rejected":   true,
			"reason":     capsdk.HandshakeRejectInternalError,
		})
		body = fallback
	}
	s.emit(ctx, audit.SIEMEvent{
		Timestamp: s.now().UTC(),
		EventType: EventWorkerHandshake,
		Severity:  audit.SeverityMedium,
		TenantID:  tenant,
		AgentID:   agentID,
		Action:    "worker.handshake",
		Decision:  "reject",
		Reason:    detail,
		Extra: map[string]string{
			"outcome": "rejected",
			"reason":  reason,
			"sdk_ver": sdkVer,
		},
	})
	return body, nil
}

func (s *HandshakeService) emit(ctx context.Context, ev audit.SIEMEvent) {
	if s == nil || s.audit == nil {
		return
	}
	s.audit.Emit(ctx, ev)
}

// RedisNonceStore is the production NonceStore using Redis SETNX with
// a per-(tenant, nonce) key. The key is namespaced so two tenants can
// reuse the same nonce value without colliding.
type RedisNonceStore struct {
	client redis.UniversalClient
}

// NewRedisNonceStore returns a Redis-backed NonceStore.
func NewRedisNonceStore(client redis.UniversalClient) *RedisNonceStore {
	return &RedisNonceStore{client: client}
}

// Claim atomically inserts (tenant, nonce) with TTL. Returns true if
// this was the first time the nonce was seen, false on replay.
func (s *RedisNonceStore) Claim(ctx context.Context, tenant, nonce string, ttl time.Duration) (bool, error) {
	if s == nil || s.client == nil {
		return false, ErrSessionTokenStoreUnready
	}
	if strings.TrimSpace(nonce) == "" {
		return false, errors.New("scheduler: nonce empty")
	}
	key := nonceKeyPrefix + tenant + ":" + nonce
	return s.client.SetNX(ctx, key, "1", ttl).Result()
}

func tenantMatches(identity *store.AgentIdentity, tenant string) bool {
	if identity == nil {
		return false
	}
	t := strings.TrimSpace(tenant)
	if t == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(identity.Owner), t) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(identity.Team), t) {
		return true
	}
	return false
}

func orPlaceholder(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func requestIDFor(req *capsdk.HandshakeRequest) string {
	if req == nil {
		return ""
	}
	return req.RequestID
}

func tenantFor(req *capsdk.HandshakeRequest) string {
	if req == nil {
		return ""
	}
	return req.Tenant
}

func agentIDFor(req *capsdk.HandshakeRequest) string {
	if req == nil {
		return ""
	}
	return req.AgentID
}

func sdkVerFor(req *capsdk.HandshakeRequest) string {
	if req == nil {
		return ""
	}
	return req.SDKVersion
}
