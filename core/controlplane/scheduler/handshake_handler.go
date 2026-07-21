package scheduler

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"reflect"
	"strings"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	"github.com/cordum/cordum/core/audit"
)

const (
	EventWorkerHandshake               = "worker_handshake"
	WorkerHandshakeChallengeSubject    = "sys.worker.handshake.challenge"
	WorkerHandshakeAuthenticateSubject = "sys.worker.handshake.authenticate"
	WorkerHandshakeAudience            = "cordum-scheduler"
	WorkerHandshakeProtocolVersion     = int32(1)
	WorkerHandshakeNonceSize           = 32
	defaultHandshakeChallengeTTL       = 30 * time.Second
	maximumHandshakeLifetime           = time.Minute
)

// AuditSink receives security outcomes without proof or token material.
type AuditSink interface {
	Emit(context.Context, audit.SIEMEvent)
}

// HandshakeTrustIdentity is the server-derived worker authority used by the
// proof verifier. No field is copied from the request.
type HandshakeTrustIdentity struct {
	WorkerID      string
	AgentID       string
	TenantID      string
	ProofKeyID    string
	PublicKey     *ecdsa.PublicKey
	AllowedTopics []string
	AgentName     string
}

// HandshakeTrustResolver resolves a registered, active worker and proof key.
type HandshakeTrustResolver interface {
	Resolve(context.Context, string, string) (*HandshakeTrustIdentity, error)
}

// HandshakeConsumeStatus distinguishes replay from a binding mismatch.
type HandshakeConsumeStatus uint8

const (
	HandshakeConsumeMissing HandshakeConsumeStatus = iota
	HandshakeConsumeMatched
	HandshakeConsumeMismatch
)

// HandshakeChallengeStore owns one-time challenge state.
type HandshakeChallengeStore interface {
	Create(context.Context, *agentv1.WorkerHandshakeChallenge, time.Duration) (bool, error)
	Consume(context.Context, *agentv1.WorkerHandshakeChallenge) (HandshakeConsumeStatus, error)
}

// HandshakeServiceOptions configures the scheduler trust endpoint.
type HandshakeServiceOptions struct {
	Audience            string
	SchedulerID         string
	SchedulerKeyID      string
	SchedulerPrivateKey *ecdsa.PrivateKey
	Skew                time.Duration
	ChallengeTTL        time.Duration
	Now                 func() time.Time
}

// HandshakeService authenticates CAP trust packets and mints bound sessions.
type HandshakeService struct {
	issuer         *SessionTokenIssuer
	resolver       HandshakeTrustResolver
	challenges     HandshakeChallengeStore
	audit          AuditSink
	audience       string
	schedulerID    string
	schedulerKeyID string
	schedulerKey   *ecdsa.PrivateKey
	skew           time.Duration
	challengeTTL   time.Duration
	now            func() time.Time
}

// HandshakeError deliberately exposes only a coarse wire-safe reason.
type HandshakeError struct {
	reason agentv1.WorkerHandshakeRejectionReason
}

func (e *HandshakeError) Error() string { return "scheduler: worker handshake rejected" }

func (e *HandshakeError) Reason() agentv1.WorkerHandshakeRejectionReason {
	if e == nil {
		return agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_INTERNAL_ERROR
	}
	return e.reason
}

type handshakeFailure struct {
	reason agentv1.WorkerHandshakeRejectionReason
	detail string
}

func failHandshake(reason agentv1.WorkerHandshakeRejectionReason, detail string) *handshakeFailure {
	return &handshakeFailure{reason: reason, detail: detail}
}

func NewHandshakeService(issuer *SessionTokenIssuer, resolver HandshakeTrustResolver, challenges HandshakeChallengeStore, sink AuditSink, opts HandshakeServiceOptions) (*HandshakeService, error) {
	if issuer == nil || missingHandshakeDependency(reflect.ValueOf(resolver)) ||
		missingHandshakeDependency(reflect.ValueOf(challenges)) || missingHandshakeDependency(reflect.ValueOf(sink)) {
		return nil, errors.New("scheduler: handshake service dependencies required")
	}
	if err := validateHandshakeOptions(opts); err != nil {
		return nil, err
	}
	skew, ttl, now := opts.Skew, opts.ChallengeTTL, opts.Now
	if skew == 0 {
		skew = maximumHandshakeLifetime
	}
	if ttl == 0 {
		ttl = defaultHandshakeChallengeTTL
	}
	if now == nil {
		now = time.Now
	}
	return &HandshakeService{
		issuer: issuer, resolver: resolver, challenges: challenges, audit: sink,
		audience: opts.Audience, schedulerID: strings.TrimSpace(opts.SchedulerID),
		schedulerKeyID: strings.TrimSpace(opts.SchedulerKeyID), schedulerKey: opts.SchedulerPrivateKey,
		skew: skew, challengeTTL: ttl, now: now,
	}, nil
}

func missingHandshakeDependency(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func validateHandshakeOptions(opts HandshakeServiceOptions) error {
	if opts.Audience != WorkerHandshakeAudience {
		return errors.New("scheduler: handshake audience must be cordum-scheduler")
	}
	if strings.TrimSpace(opts.SchedulerID) == "" || strings.TrimSpace(opts.SchedulerKeyID) == "" {
		return errors.New("scheduler: handshake scheduler identity and key id required")
	}
	if !validHandshakePrivateKey(opts.SchedulerPrivateKey) {
		return errors.New("scheduler: handshake scheduler key must be P-256")
	}
	if opts.Skew < 0 || opts.Skew > maximumHandshakeLifetime {
		return errors.New("scheduler: handshake skew must be within one minute")
	}
	if opts.ChallengeTTL < 0 || opts.ChallengeTTL > maximumHandshakeLifetime {
		return errors.New("scheduler: handshake challenge ttl must be within one minute")
	}
	return nil
}

func validHandshakePrivateKey(key *ecdsa.PrivateKey) bool {
	// key.D/key.X/key.Y are read-only here: a nil guard before ECDH(), which
	// panics (rather than erroring) on zero-value coordinates/scalar.
	// Bytes()/ECDH() give no nil-safe alternative to guard with.
	if key == nil || key.D == nil || key.Curve != elliptic.P256() || key.X == nil || key.Y == nil { //nolint:staticcheck // SA1019: nil guard, see comment above
		return false
	}
	privateKey, err := key.ECDH()
	if err != nil {
		return false
	}
	publicKey, err := key.PublicKey.ECDH()
	return err == nil && bytes.Equal(privateKey.PublicKey().Bytes(), publicKey.Bytes())
}

// HandleChallenge verifies a signed request before creating shared state.
func (s *HandshakeService) HandleChallenge(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, error) {
	response, identity, failure := s.issueChallenge(ctx, packet)
	if failure != nil {
		s.emitHandshakeAudit(ctx, packet, identity, "challenge", "reject", failure)
		return nil, &HandshakeError{reason: failure.reason}
	}
	s.emitHandshakeAudit(ctx, packet, identity, "challenge", "accept", nil)
	return response, nil
}

// HandleAuthenticate consumes a valid proof before issuing or renewing.
func (s *HandshakeService) HandleAuthenticate(ctx context.Context, packet *agentv1.BusPacket) (*agentv1.BusPacket, error) {
	response, identity, failure := s.authenticate(ctx, packet)
	if failure == nil {
		s.emitHandshakeAudit(ctx, packet, identity, "authenticate", "accept", nil)
		return response, nil
	}
	s.emitHandshakeAudit(ctx, packet, identity, "authenticate", "reject", failure)
	challenge := s.challengeForSafeRejection(ctx, packet, identity)
	if challenge == nil {
		return nil, &HandshakeError{reason: failure.reason}
	}
	rejected, err := s.buildHandshakeResult(challenge, "", time.Time{}, failure.reason)
	if err != nil {
		return nil, &HandshakeError{reason: agentv1.WorkerHandshakeRejectionReason_WORKER_HANDSHAKE_REJECTION_REASON_INTERNAL_ERROR}
	}
	return rejected, nil
}
