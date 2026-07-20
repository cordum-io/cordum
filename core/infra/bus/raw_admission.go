package bus

import (
	"context"
	"errors"
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

// RawAdmissionResult is returned after inspecting the exact transport bytes.
// Accepted results must include the already decoded and validated packet.
type RawAdmissionResult struct {
	Disposition RawAdmissionDisposition
	Packet      *pb.BusPacket
	Authority   *RawAdmissionAuthority
	RetryAfter  time.Duration
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
	packet, authority, action, delay, proceed := preAdmitRawPacket(ctx, admit, subject, data)
	if !proceed {
		return action, delay
	}
	return processAfterRawAdmission(ctx, admit != nil, packet, authority, subject, data, handler, numDelivered)
}

func preAdmitRawPacket(
	ctx context.Context,
	admit RawPacketAdmission,
	subject string,
	data []byte,
) (*pb.BusPacket, *RawAdmissionAuthority, msgAction, time.Duration, bool) {
	if admit == nil {
		return nil, nil, msgActionAck, 0, true
	}
	return evaluateRawAdmission(admit(ctx, subject, data))
}

func processAfterRawAdmission(
	ctx context.Context,
	admitted bool,
	packet *pb.BusPacket,
	authority *RawAdmissionAuthority,
	subject string,
	data []byte,
	handler func(context.Context, *pb.BusPacket) error,
	numDelivered uint64,
) (msgAction, time.Duration) {
	if !admitted {
		return processBusMsgCtx(ctx, subject, data, handler, numDelivered)
	}
	return packetHandlerAction(handler(withRawAdmissionAuthority(ctx, authority), packet))
}

func evaluateRawAdmission(result RawAdmissionResult) (*pb.BusPacket, *RawAdmissionAuthority, msgAction, time.Duration, bool) {
	switch result.Disposition {
	case RawAdmissionAccepted:
		if result.Packet == nil {
			return nil, nil, msgActionTerm, 0, false
		}
		return result.Packet, result.Authority, msgActionAck, 0, true
	case RawAdmissionDuplicate:
		return nil, nil, msgActionAck, 0, false
	case RawAdmissionRetry:
		if result.RetryAfter > 0 {
			return nil, nil, msgActionNakDelay, result.RetryAfter, false
		}
		return nil, nil, msgActionNak, 0, false
	default:
		return nil, nil, msgActionTerm, 0, false
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
