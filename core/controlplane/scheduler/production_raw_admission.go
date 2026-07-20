package scheduler

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/infra/bus"
	"google.golang.org/protobuf/proto"
)

const (
	defaultProductionMaxRawBytes = 1 << 20
	defaultProductionMaxLifetime = 5 * time.Minute
	defaultProductionClockSkew   = 30 * time.Second
)

var (
	ErrProductionAdmissionUnavailable = errors.New("scheduler: production admission unavailable")
	ErrProductionPacketTooLarge       = errors.New("scheduler: production packet exceeds size limit")
	ErrProductionSessionIdentity      = errors.New("scheduler: authenticated production identity mismatch")
)

// AuthenticatedProductionSession is transport-derived authority. None of its
// fields may be populated from the packet being admitted.
type AuthenticatedProductionSession struct {
	Subject  string
	Identity *agentv1.IdentityBinding
}

// ProductionRawBoundary verifies exact wire bytes before invoking a stateful
// scheduler handler. ResolveKey must use only locally configured trust.
type ProductionRawBoundary struct {
	ResolveKey  func(tenant, sender, keyID string) (*ecdsa.PublicKey, error)
	Replay      capsdk.ReplayStore
	Now         func() time.Time
	MaxLifetime time.Duration
	ClockSkew   time.Duration
	MaxRawBytes int
}

// ProductionSessionResolver derives session authority without trusting packet
// fields. Implementations may strictly extract a session credential from raw
// wire bytes, but MUST validate it against local authenticated state.
type ProductionSessionResolver func(
	ctx context.Context,
	actualSubject string,
	raw []byte,
) (AuthenticatedProductionSession, error)

// InstallProductionRawAdmission configures a bus before subscriptions exist.
// It does not select a runtime profile or activate production mode; binaries
// must make that explicit after all mandatory dependencies are initialized.
func InstallProductionRawAdmission(
	target *bus.NatsBus,
	boundary *ProductionRawBoundary,
	resolveSession ProductionSessionResolver,
) error {
	if target == nil || boundary == nil || resolveSession == nil ||
		boundary.ResolveKey == nil || boundary.Replay == nil {
		return ErrProductionAdmissionUnavailable
	}
	return target.SetRawPacketAdmission(NewProductionRawAdmissionHook(boundary, resolveSession))
}

// NewProductionRawAdmissionHook adapts the production boundary to NatsBus.
// Binary/config activation is intentionally separate so compatibility mode
// cannot accidentally advertise CAP-PRODUCTION.
func NewProductionRawAdmissionHook(
	boundary *ProductionRawBoundary,
	resolveSession ProductionSessionResolver,
) bus.RawPacketAdmission {
	if boundary == nil || resolveSession == nil {
		return unavailableProductionRawAdmission
	}
	frozenBoundary := *boundary
	return func(ctx context.Context, subject string, raw []byte) bus.RawAdmissionResult {
		session, err := resolveSession(ctx, subject, raw)
		if err != nil {
			return productionRawAdmissionError(err)
		}
		session = snapshotAuthenticatedProductionSession(session)
		var packet *agentv1.BusPacket
		outcome, err := frozenBoundary.Handle(ctx, subject, session, raw, func(_ context.Context, admitted *agentv1.BusPacket) error {
			packet = admitted
			return nil
		})
		if err != nil {
			return productionRawAdmissionError(err)
		}
		if outcome == capsdk.ReplayOutcomeDuplicate {
			return bus.RawAdmissionResult{Disposition: bus.RawAdmissionDuplicate}
		}
		if outcome != capsdk.ReplayOutcomeFirst || packet == nil {
			return bus.RawAdmissionResult{Disposition: bus.RawAdmissionRetry}
		}
		return bus.RawAdmissionResult{Disposition: bus.RawAdmissionAccepted, Packet: packet}
	}
}

func unavailableProductionRawAdmission(context.Context, string, []byte) bus.RawAdmissionResult {
	return bus.RawAdmissionResult{Disposition: bus.RawAdmissionRetry}
}
func snapshotAuthenticatedProductionSession(session AuthenticatedProductionSession) AuthenticatedProductionSession {
	if session.Identity != nil {
		session.Identity = proto.Clone(session.Identity).(*agentv1.IdentityBinding)
	}
	return session
}

func productionRawAdmissionError(err error) bus.RawAdmissionResult {
	if errors.Is(err, capsdk.ErrReplayStoreUnavailable) ||
		errors.Is(err, ErrProductionAdmissionUnavailable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return bus.RawAdmissionResult{Disposition: bus.RawAdmissionRetry}
	}
	return bus.RawAdmissionResult{Disposition: bus.RawAdmissionRejected}
}

// Handle admits a packet for the actual transport subject. Identical
// redelivery is acknowledged without invoking handler a second time.
func (b *ProductionRawBoundary) Handle(
	ctx context.Context,
	actualSubject string,
	session AuthenticatedProductionSession,
	raw []byte,
	handler func(context.Context, *agentv1.BusPacket) error,
) (capsdk.ReplayOutcome, error) {
	if err := b.validateInputs(ctx, actualSubject, session, raw, handler); err != nil {
		return 0, err
	}
	trust := b.trust(actualSubject, session)
	packet, err := capsdk.VerifyProductionPacket(raw, trust)
	if err != nil {
		return 0, err
	}
	if err := capsdk.ValidateBusPacket(packet); err != nil {
		return 0, err
	}
	if err := validateAuthenticatedProductionIdentity(packet, session); err != nil {
		return 0, err
	}
	digest, err := capsdk.ProductionSignedBodyDigest(raw)
	if err != nil {
		return 0, err
	}
	metadata := packet.GetSignatureMetadata()
	replayExpiry := metadata.GetExpiresAt().AsTime().Add(trust.ClockSkew)
	outcome, err := b.Replay.Admit(
		session.Identity.GetTenantId(), actualSubject, session.Subject,
		metadata.GetMessageId(), digest[:], replayExpiry,
	)
	if err != nil {
		if errors.Is(err, capsdk.ErrReplayConflict) {
			return 0, capsdk.ErrReplayConflict
		}
		return 0, capsdk.ErrReplayStoreUnavailable
	}
	if outcome == capsdk.ReplayOutcomeDuplicate {
		return outcome, nil
	}
	if outcome != capsdk.ReplayOutcomeFirst {
		return 0, ErrProductionAdmissionUnavailable
	}
	return outcome, handler(ctx, packet)
}

func (b *ProductionRawBoundary) validateInputs(
	ctx context.Context,
	actualSubject string,
	session AuthenticatedProductionSession,
	raw []byte,
	handler func(context.Context, *agentv1.BusPacket) error,
) error {
	if b == nil || b.ResolveKey == nil || b.Replay == nil || handler == nil || actualSubject == "" {
		return ErrProductionAdmissionUnavailable
	}
	if ctx == nil {
		return ErrProductionAdmissionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if session.Subject == "" || !completeProductionIdentity(session.Identity) {
		return ErrProductionAdmissionUnavailable
	}
	limit := b.MaxRawBytes
	if limit <= 0 {
		limit = defaultProductionMaxRawBytes
	}
	if len(raw) == 0 {
		return capsdk.ErrMalformedProductionWire
	}
	if len(raw) > limit {
		return ErrProductionPacketTooLarge
	}
	if b.MaxLifetime < 0 || b.ClockSkew < 0 || b.MaxRawBytes < 0 {
		return ErrProductionAdmissionUnavailable
	}
	return nil
}

func (b *ProductionRawBoundary) trust(actualSubject string, session AuthenticatedProductionSession) capsdk.ProductionTrustStore {
	maxLifetime := b.MaxLifetime
	if maxLifetime <= 0 {
		maxLifetime = defaultProductionMaxLifetime
	}
	clockSkew := b.ClockSkew
	if clockSkew <= 0 {
		clockSkew = defaultProductionClockSkew
	}
	return capsdk.ProductionTrustStore{
		Audience: actualSubject,
		Tenant:   session.Identity.GetTenantId(),
		Sender:   session.Subject,
		ResolveKey: func(_, _, keyID string) (*ecdsa.PublicKey, error) {
			return b.ResolveKey(session.Identity.GetTenantId(), session.Subject, keyID)
		},
		Now: b.Now, MaxLifetime: maxLifetime, ClockSkew: clockSkew,
	}
}

func validateAuthenticatedProductionIdentity(packet *agentv1.BusPacket, session AuthenticatedProductionSession) error {
	if packet.GetSenderId() != session.Subject || !sameProductionIdentity(packet.GetIdentity(), session.Identity) {
		return ErrProductionSessionIdentity
	}
	switch payload := packet.GetPayload().(type) {
	case *agentv1.BusPacket_JobRequest:
		if err := capsdk.ValidateIdentityBinding(payload.JobRequest, session.Identity); err != nil {
			return ErrProductionSessionIdentity
		}
		return validateProductionCompensationIdentity(payload.JobRequest.GetCompensation(), session.Identity)
	case *agentv1.BusPacket_JobResult:
		return requireProductionPayloadIdentity(payload.JobResult.GetIdentity(), session.Identity)
	case *agentv1.BusPacket_JobProgress:
		return requireProductionPayloadIdentity(payload.JobProgress.GetIdentity(), session.Identity)
	case *agentv1.BusPacket_JobCancel:
		return requireProductionPayloadIdentity(payload.JobCancel.GetIdentity(), session.Identity)
	default:
		return nil
	}
}

func validateProductionCompensationIdentity(compensation *agentv1.Compensation, identity *agentv1.IdentityBinding) error {
	if compensation == nil {
		return nil
	}
	if compensation.GetIdentity() != nil && !sameProductionIdentity(compensation.GetIdentity(), identity) {
		return ErrProductionSessionIdentity
	}
	if compensation.GetTenantId() != "" && compensation.GetTenantId() != identity.GetTenantId() {
		return ErrProductionSessionIdentity
	}
	if compensation.GetPrincipalId() != "" && compensation.GetPrincipalId() != identity.GetPrincipalId() {
		return ErrProductionSessionIdentity
	}
	return nil
}

func requireProductionPayloadIdentity(actual, expected *agentv1.IdentityBinding) error {
	if !sameProductionIdentity(actual, expected) {
		return ErrProductionSessionIdentity
	}
	return nil
}

func completeProductionIdentity(identity *agentv1.IdentityBinding) bool {
	return identity != nil && identity.GetTenantId() != "" && identity.GetPrincipalId() != "" && identity.GetActorId() != ""
}

func sameProductionIdentity(left, right *agentv1.IdentityBinding) bool {
	return left != nil && right != nil &&
		left.GetTenantId() == right.GetTenantId() &&
		left.GetPrincipalId() == right.GetPrincipalId() &&
		left.GetActorId() == right.GetActorId() &&
		left.GetDelegationId() == right.GetDelegationId()
}
