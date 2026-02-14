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
func HeartbeatPayload(workerID, pool string, activeJobs, maxParallel int, cpuLoad float32) ([]byte, error) {
	return capworker.HeartbeatPayload(workerID, pool, activeJobs, maxParallel, cpuLoad)
}

// HeartbeatPayloadWithMemory returns a heartbeat payload including memory utilization.
func HeartbeatPayloadWithMemory(workerID, pool string, activeJobs, maxParallel int, cpuLoad, memoryLoad float32) ([]byte, error) {
	return capworker.HeartbeatPayloadWithMemory(workerID, pool, activeJobs, maxParallel, cpuLoad, memoryLoad)
}

// HeartbeatPayloadWithProgress returns a heartbeat payload including optional progress checkpoints.
func HeartbeatPayloadWithProgress(workerID, pool string, activeJobs, maxParallel int, cpuLoad, memoryLoad float32, progressPct int32, lastMemo string) ([]byte, error) {
	return capworker.HeartbeatPayloadWithProgress(workerID, pool, activeJobs, maxParallel, cpuLoad, memoryLoad, progressPct, lastMemo)
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
