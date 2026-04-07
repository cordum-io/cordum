package runtime

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"strings"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	capworker "github.com/cordum-io/cap/v2/sdk/go/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	SubjectSubmit        = capsdk.SubjectSubmit
	SubjectResult        = capsdk.SubjectResult
	SubjectHeartbeat     = capsdk.SubjectHeartbeat
	SubjectProgress      = "sys.job.progress"
	SubjectCancel        = "sys.job.cancel"
	SubjectDLQ           = "sys.job.dlq"
	SubjectWorkflowEvent = "sys.workflow.event"

	DefaultProtocolVersion   = capsdk.DefaultProtocolVersion
	DefaultHeartbeatInterval = capsdk.DefaultHeartbeatInterval
)

// Publisher publishes CAP envelopes to the message bus.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// HeartbeatOption mutates an outgoing heartbeat before it is encoded.
type HeartbeatOption func(*agentv1.Heartbeat) error

// WithAuthToken sets the optional worker attestation token on the heartbeat.
func WithAuthToken(token string) HeartbeatOption {
	token = strings.TrimSpace(token)
	return func(hb *agentv1.Heartbeat) error {
		if hb == nil || token == "" {
			return nil
		}
		hb.AuthToken = token
		return nil
	}
}

// DirectSubject returns the direct worker subject for a worker ID.
func DirectSubject(workerID string) string {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return ""
	}
	return "worker." + workerID + ".jobs"
}

// PublishCancel emits a JobCancel envelope to the cancel subject.
func PublishCancel(pub Publisher, cancel *agentv1.JobCancel, traceID, senderID string, key *ecdsa.PrivateKey) error {
	if pub == nil {
		return errors.New("publisher required")
	}
	if cancel == nil {
		return errors.New("cancel required")
	}
	cancel.JobId = strings.TrimSpace(cancel.JobId)
	if cancel.JobId == "" {
		return errors.New("job id required")
	}
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return errors.New("sender id required")
	}
	if strings.TrimSpace(traceID) == "" {
		traceID = cancel.JobId
	}
	packet := &agentv1.BusPacket{
		TraceId:         traceID,
		SenderId:        senderID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &agentv1.BusPacket_JobCancel{
			JobCancel: cancel,
		},
	}
	return publishEnvelope(pub, SubjectCancel, packet, key)
}

// HeartbeatPayload returns a protobuf-encoded heartbeat envelope.
func HeartbeatPayload(workerID, pool string, activeJobs, maxParallel int, cpuLoad float32, opts ...HeartbeatOption) ([]byte, error) {
	payload, err := capworker.HeartbeatPayload(workerID, pool, activeJobs, maxParallel, cpuLoad)
	if err != nil {
		return nil, err
	}
	return applyHeartbeatOptions(payload, opts...)
}

// HeartbeatPayloadWithMemory returns a heartbeat payload including memory utilization.
func HeartbeatPayloadWithMemory(workerID, pool string, activeJobs, maxParallel int, cpuLoad, memoryLoad float32, opts ...HeartbeatOption) ([]byte, error) {
	payload, err := capworker.HeartbeatPayloadWithMemory(workerID, pool, activeJobs, maxParallel, cpuLoad, memoryLoad)
	if err != nil {
		return nil, err
	}
	return applyHeartbeatOptions(payload, opts...)
}

// HeartbeatPayloadWithProgress returns a heartbeat payload including optional progress checkpoints.
func HeartbeatPayloadWithProgress(workerID, pool string, activeJobs, maxParallel int, cpuLoad, memoryLoad float32, progressPct int32, lastMemo string, opts ...HeartbeatOption) ([]byte, error) {
	payload, err := capworker.HeartbeatPayloadWithProgress(workerID, pool, activeJobs, maxParallel, cpuLoad, memoryLoad, progressPct, lastMemo)
	if err != nil {
		return nil, err
	}
	return applyHeartbeatOptions(payload, opts...)
}

// EmitHeartbeat publishes a heartbeat once. Call repeatedly on a ticker.
func EmitHeartbeat(nc *nats.Conn, payload []byte) error {
	return capworker.EmitHeartbeat(nc, payload)
}

// HeartbeatLoop emits heartbeats until ctx is done.
func HeartbeatLoop(ctx context.Context, nc *nats.Conn, payloadFn func() ([]byte, error)) {
	capworker.HeartbeatLoop(ctx, nc, payloadFn)
}

func publishEnvelope(pub Publisher, subject string, packet *agentv1.BusPacket, key *ecdsa.PrivateKey) error {
	if packet == nil {
		return errors.New("packet required")
	}
	if strings.TrimSpace(subject) == "" {
		return errors.New("subject required")
	}
	if key != nil {
		if err := capsdk.SignPacket(packet, key); err != nil {
			return fmt.Errorf("sign packet: %w", err)
		}
	}
	data, err := capsdk.MarshalDeterministic(packet)
	if err != nil {
		return fmt.Errorf("marshal packet: %w", err)
	}
	if err := pub.Publish(subject, data); err != nil {
		return fmt.Errorf("publish packet: %w", err)
	}
	return nil
}

func applyHeartbeatOptions(payload []byte, opts ...HeartbeatOption) ([]byte, error) {
	if len(opts) == 0 {
		return payload, nil
	}

	var packet agentv1.BusPacket
	if err := proto.Unmarshal(payload, &packet); err != nil {
		return nil, fmt.Errorf("decode heartbeat packet: %w", err)
	}

	heartbeat := packet.GetHeartbeat()
	if heartbeat == nil {
		return nil, errors.New("heartbeat payload missing heartbeat message")
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(heartbeat); err != nil {
			return nil, fmt.Errorf("apply heartbeat option: %w", err)
		}
	}

	encoded, err := capsdk.MarshalDeterministic(&packet)
	if err != nil {
		return nil, fmt.Errorf("marshal heartbeat packet: %w", err)
	}
	return encoded, nil
}

func setForwardCompatStringField(msg proto.Message, fieldNumber protowire.Number, value string) error {
	if msg == nil {
		return errors.New("message required")
	}
	if fieldNumber <= 0 {
		return fmt.Errorf("invalid field number %d", fieldNumber)
	}

	raw := msg.ProtoReflect().GetUnknown()
	filtered, err := removeUnknownField(raw, fieldNumber)
	if err != nil {
		return err
	}
	filtered = protowire.AppendTag(filtered, fieldNumber, protowire.BytesType)
	filtered = protowire.AppendString(filtered, value)
	msg.ProtoReflect().SetUnknown(filtered)
	return nil
}

func removeUnknownField(raw []byte, fieldNumber protowire.Number) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	filtered := make([]byte, 0, len(raw))
	for len(raw) > 0 {
		fieldStart := len(filtered)
		num, wireType, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return nil, protowire.ParseError(tagLen)
		}
		raw = raw[tagLen:]

		valueLen := protowire.ConsumeFieldValue(num, wireType, raw)
		if valueLen < 0 {
			return nil, protowire.ParseError(valueLen)
		}

		if num != fieldNumber {
			filtered = protowire.AppendTag(filtered, num, wireType)
			filtered = append(filtered, raw[:valueLen]...)
		} else {
			filtered = filtered[:fieldStart]
		}
		raw = raw[valueLen:]
	}
	return filtered, nil
}
