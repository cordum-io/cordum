package bus

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// RawAdmissionDisposition tells the bus how to settle an admitted delivery.
type RawAdmissionDisposition uint8

const (
	RawAdmissionInvalid RawAdmissionDisposition = iota
	RawAdmissionAccepted
	RawAdmissionDuplicate
	RawAdmissionRejected
	RawAdmissionRetry
)

// RawAdmissionOutcome is how an accepted delivery ended.
type RawAdmissionOutcome uint8

const (
	// RawAdmissionSucceeded means the message was processed.
	RawAdmissionSucceeded RawAdmissionOutcome = iota + 1
	// RawAdmissionFailed means the message ended without success and will not
	// be delivered again.
	RawAdmissionFailed
	// RawAdmissionReleased means the message was not processed and is
	// redelivered for another attempt.
	RawAdmissionReleased
)

// RawAdmissionResult is returned after inspecting the exact transport bytes.
// Accepted results must include the already decoded and validated packet.
// Settle, when set on an accepted result, is called at most once with how
// the delivery ended, so admission can tell a completed message from one
// that must run again.
type RawAdmissionResult struct {
	Disposition RawAdmissionDisposition
	Packet      *pb.BusPacket
	Authority   *RawAdmissionAuthority
	RetryAfter  time.Duration
	Settle      func(RawAdmissionOutcome)
}

func (r RawAdmissionResult) settle(outcome RawAdmissionOutcome) {
	if r.Settle != nil {
		r.Settle(outcome)
	}
}

// RawPacketAdmission verifies raw bytes against the authenticated NATS subject.
// Implementations must treat data as read-only and return only locally trusted
// decoded packets.
type RawPacketAdmission func(ctx context.Context, actualSubject string, data []byte) RawAdmissionResult

// ErrRawAdmissionFrozen is returned when admission is changed after the first
// subscription starts. A live production boundary must not be disabled or
// replaced while existing callbacks still enforce the previously captured hook.
var ErrRawAdmissionFrozen = errors.New("raw packet admission is frozen")

// SetRawPacketAdmission installs admission before the first subscription.
// Passing nil selects compatibility decoding only while configuration is mutable.
func (b *NatsBus) SetRawPacketAdmission(admit RawPacketAdmission) error {
	if b == nil {
		return errNilBus
	}
	b.hooksMu.Lock()
	defer b.hooksMu.Unlock()
	if b.rawAdmissionFrozen {
		return ErrRawAdmissionFrozen
	}
	b.rawPacketAdmission = admit
	if admit == nil {
		busCompatibilityTotal.WithLabelValues(compatReasonConfiguredFailOpen).Inc()
	}
	return nil
}

func (b *NatsBus) rawPacketAdmissionHook() RawPacketAdmission {
	if b == nil {
		return nil
	}
	b.hooksMu.RLock()
	admit := b.rawPacketAdmission
	b.hooksMu.RUnlock()
	return admit
}

func (b *NatsBus) freezeRawPacketAdmission() RawPacketAdmission {
	b.hooksMu.Lock()
	b.rawAdmissionFrozen = true
	admit := b.rawPacketAdmission
	b.hooksMu.Unlock()
	return admit
}

// FreezePacketSecurity makes the configured admission and encoder immutable.
// Production boot calls this before any subscription or publish can occur.
func (b *NatsBus) FreezePacketSecurity() {
	if b == nil {
		return
	}
	b.hooksMu.Lock()
	b.rawAdmissionFrozen = true
	b.hooksMu.Unlock()
}

func (b *NatsBus) processInboundMsgCtx(
	ctx context.Context,
	subject string,
	data []byte,
	handler func(context.Context, *pb.BusPacket) error,
	numDelivered uint64,
) (msgAction, time.Duration) {
	return processInboundWithAdmission(
		ctx, b.rawPacketAdmissionHook(), subject, data, handler, numDelivered,
	)
}

func processInboundWithAdmission(
	ctx context.Context,
	admit RawPacketAdmission,
	subject string,
	data []byte,
	handler func(context.Context, *pb.BusPacket) error,
	numDelivered uint64,
) (msgAction, time.Duration) {
	admission, action, delay, proceed := preAdmitRawPacket(ctx, admit, subject, data)
	if !proceed {
		return action, delay
	}
	return processAfterRawAdmission(ctx, admit != nil, admission, subject, data, handler, numDelivered)
}

func preAdmitRawPacket(
	ctx context.Context,
	admit RawPacketAdmission,
	subject string,
	data []byte,
) (RawAdmissionResult, msgAction, time.Duration, bool) {
	if admit == nil {
		return RawAdmissionResult{}, msgActionAck, 0, true
	}
	admission := admit(ctx, subject, data)
	action, delay, proceed := evaluateRawAdmission(admission)
	return admission, action, delay, proceed
}

func processAfterRawAdmission(
	ctx context.Context,
	admitted bool,
	admission RawAdmissionResult,
	subject string,
	data []byte,
	handler func(context.Context, *pb.BusPacket) error,
	numDelivered uint64,
) (msgAction, time.Duration) {
	if !admitted {
		return processBusMsgCtx(ctx, subject, data, handler, numDelivered)
	}
	err := handler(withRawAdmissionAuthority(ctx, admission.Authority), admission.Packet)
	admission.settle(handlerOutcome(subject, err))
	return packetHandlerAction(err)
}

// handlerOutcome maps a handler result to the admitted delivery's outcome. The
// bus acks a non-retryable error, so that error ends the message for good.
func handlerOutcome(subject string, err error) RawAdmissionOutcome {
	if err == nil {
		return RawAdmissionSucceeded
	}
	if _, ok := RetryDelay(err); ok {
		return RawAdmissionReleased
	}
	slog.Warn("bus: admitted message failed without retry", "subject", subject, "err", err)
	return RawAdmissionFailed
}

func evaluateRawAdmission(result RawAdmissionResult) (msgAction, time.Duration, bool) {
	switch result.Disposition {
	case RawAdmissionAccepted:
		if result.Packet == nil {
			result.settle(RawAdmissionFailed)
			return msgActionTerm, 0, false
		}
		return msgActionAck, 0, true
	case RawAdmissionDuplicate:
		return msgActionAck, 0, false
	case RawAdmissionRetry:
		if result.RetryAfter > 0 {
			return msgActionNakDelay, result.RetryAfter, false
		}
		return msgActionNak, 0, false
	default:
		return msgActionTerm, 0, false
	}
}

func packetHandlerAction(err error) (msgAction, time.Duration) {
	if err == nil {
		return msgActionAck, 0
	}
	if delay, ok := RetryDelay(err); ok {
		if delay > 0 {
			return msgActionNakDelay, delay
		}
		return msgActionNak, 0
	}
	return msgActionAck, 0
}

func contextualHandler(handler func(*pb.BusPacket) error) func(context.Context, *pb.BusPacket) error {
	return func(_ context.Context, packet *pb.BusPacket) error {
		return handler(packet)
	}
}
