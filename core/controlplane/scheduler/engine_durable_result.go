package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

type durableResultEffect struct {
	State  model.JobState `json:"state"`
	Topic  string         `json:"topic"`
	Packet []byte         `json:"packet"`
}

func (e *Engine) handleProductionJobResult(
	packet *pb.BusPacket, result *pb.JobResult, claims *SessionTokenClaims,
) error {
	durable, ok := e.jobStore.(model.DurableJobEventStore)
	if !ok {
		return RetryAfter(errors.New("production durable job event store unavailable"), retryDelayStore)
	}
	apply, err := e.buildDurableResultApply(packet, result, claims)
	if err != nil {
		return err
	}
	disposition, err := durable.ApplyJobResult(e.ctx, apply)
	if err != nil {
		if errors.Is(err, store.ErrJobEventDigestConflict) {
			slog.Error("production job result rejected", "job_id", result.GetJobId(), "reason", "message_digest_conflict")
			return nil
		}
		return RetryAfter(err, retryDelayStore)
	}
	if disposition == model.JobEventRejected {
		slog.Warn("production job result fence rejected", "job_id", result.GetJobId())
		return nil
	}
	return e.drainPendingJobEffects(e.ctx, durable, 100)
}

func (e *Engine) buildDurableResultApply(
	packet *pb.BusPacket, result *pb.JobResult, claims *SessionTokenClaims,
) (model.JobResultApply, error) {
	if packet == nil || result == nil || result.GetDispatch() == nil || claims == nil {
		return model.JobResultApply{}, nilDispatchFenceError(result)
	}
	messageID, digest, err := signedEventEvidence(packet)
	if err != nil {
		return model.JobResultApply{}, err
	}
	topic, err := e.loadResultTopic(result.GetJobId())
	if err != nil {
		return model.JobResultApply{}, RetryAfter(err, retryDelayStore)
	}
	state, resultPtr, err := e.productionResultState(result, topic)
	if err != nil {
		return model.JobResultApply{}, err
	}
	effect, err := marshalDurableResultEffect(result, state, topic, resultPtr, claims.Subject)
	if err != nil {
		return model.JobResultApply{}, RetryAfter(err, retryDelayStore)
	}
	dispatch := result.GetDispatch()
	return model.JobResultApply{
		JobID: result.GetJobId(), DispatchID: dispatch.GetDispatchId(), Attempt: int(dispatch.GetAttempt()),
		WorkerID: strings.TrimSpace(claims.Subject), Tenant: strings.TrimSpace(claims.Tenant),
		MessageID: messageID, Digest: digest, State: state, ResultPtr: resultPtr, Effect: effect,
	}, nil
}

func nilDispatchFenceError(result *pb.JobResult) error {
	jobID := ""
	if result != nil {
		jobID = result.GetJobId()
	}
	return fmt.Errorf("production result %s missing authenticated dispatch evidence", jobID)
}

func signedEventEvidence(packet *pb.BusPacket) ([]byte, []byte, error) {
	metadata := packet.GetSignatureMetadata()
	if metadata == nil || len(metadata.GetMessageId()) != 16 || len(packet.GetSignature()) == 0 {
		return nil, nil, errors.New("production result missing signed message evidence")
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal signed result evidence: %w", err)
	}
	digest := sha256.Sum256(wire)
	return append([]byte(nil), metadata.GetMessageId()...), digest[:], nil
}

func (e *Engine) loadResultTopic(jobID string) (string, error) {
	if e.jobStore == nil {
		return "", errors.New("job store unavailable")
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	topic, err := e.jobStore.GetTopic(ctx, jobID)
	if err != nil {
		return "", fmt.Errorf("topic lookup %s: %w", jobID, err)
	}
	if strings.TrimSpace(topic) == "" {
		return "unknown", nil
	}
	return topic, nil
}

func (e *Engine) productionResultState(
	result *pb.JobResult, topic string,
) (model.JobState, string, error) {
	state := stateForJobResult(result.GetStatus())
	if state == "" {
		state = model.JobStateFailed
	}
	if state != model.JobStateSucceeded || !e.outputSafetyEnabled.Load() || e.outputSafety == nil {
		return state, result.GetResultPtr(), nil
	}
	req, err := e.loadDurableJobRequest(result.GetJobId())
	if err != nil {
		return "", "", RetryAfter(err, retryDelayStore)
	}
	record := e.checkOutputSafety(e.ctx, result.GetJobId(), topic, result, req)
	if record.Decision == OutputQuarantine || record.Decision == OutputDeny {
		return model.JobStateQuarantined, result.GetResultPtr(), nil
	}
	if record.Decision == OutputRedact {
		record = e.materializeRedaction(result.GetJobId(), topic, result, req, record)
		e.persistOutputSafety(e.ctx, result.GetJobId(), record)
		if strings.TrimSpace(record.RedactedPtr) == "" {
			return model.JobStateQuarantined, result.GetResultPtr(), nil
		}
		return state, record.RedactedPtr, nil
	}
	return state, result.GetResultPtr(), nil
}

func stateForJobResult(status pb.JobStatus) model.JobState {
	switch status {
	case pb.JobStatus_JOB_STATUS_SUCCEEDED:
		return model.JobStateSucceeded
	case pb.JobStatus_JOB_STATUS_FAILED, pb.JobStatus_JOB_STATUS_FAILED_FATAL:
		return model.JobStateFailed
	case pb.JobStatus_JOB_STATUS_FAILED_RETRYABLE:
		return model.JobStateRetrying
	case pb.JobStatus_JOB_STATUS_TIMEOUT:
		return model.JobStateTimeout
	case pb.JobStatus_JOB_STATUS_DENIED:
		return model.JobStateDenied
	case pb.JobStatus_JOB_STATUS_CANCELLED:
		return model.JobStateCancelled
	default:
		return ""
	}
}

func marshalDurableResultEffect(
	result *pb.JobResult, state model.JobState, topic, resultPtr, workerID string,
) ([]byte, error) {
	cloned := proto.Clone(result).(*pb.JobResult)
	cloned.ResultPtr = resultPtr
	cloned.WorkerId = strings.TrimSpace(workerID)
	if cloned.Dispatch != nil {
		cloned.Dispatch.AssignedWorkerId = strings.TrimSpace(workerID)
	}
	packet := &pb.BusPacket{
		TraceId: cloned.GetJobId(), SenderId: defaultSenderID, Identity: cloned.GetIdentity(),
		ProtocolVersion: protocolVersionV1,
		Payload:         &pb.BusPacket_JobResult{JobResult: cloned},
	}
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		return nil, err
	}
	return json.Marshal(durableResultEffect{State: state, Topic: topic, Packet: wire})
}

func (e *Engine) loadDurableJobRequest(jobID string) (*pb.JobRequest, error) {
	getter, ok := e.jobStore.(jobRequestGetter)
	if !ok {
		return nil, errors.New("job request store unavailable")
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	req, err := getter.GetJobRequest(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("job request lookup %s: %w", jobID, err)
	}
	if req == nil {
		return nil, fmt.Errorf("job request lookup %s: missing request", jobID)
	}
	return req, nil
}

func (e *Engine) drainPendingJobEffects(ctx context.Context, durable model.DurableJobEventStore, limit int64) error {
	effects, err := durable.PendingJobEffects(ctx, limit)
	if err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	for _, effect := range effects {
		if err := e.processDurableJobEffect(ctx, durable, effect); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) processDurableJobEffect(
	ctx context.Context, durable model.DurableJobEventStore, effect model.JobEffect,
) error {
	var envelope durableResultEffect
	if err := json.Unmarshal(effect.Payload, &envelope); err != nil {
		return RetryAfter(fmt.Errorf("decode durable job effect: %w", err), retryDelayStore)
	}
	packet := &pb.BusPacket{}
	if err := proto.Unmarshal(envelope.Packet, packet); err != nil {
		return RetryAfter(fmt.Errorf("decode accepted result: %w", err), retryDelayStore)
	}
	result := packet.GetJobResult()
	if result == nil {
		return RetryAfter(errors.New("durable effect missing result"), retryDelayStore)
	}
	if err := durable.ProjectJobResult(
		ctx, effect.JobID, envelope.State, result.GetResultPtr(), result.GetWorkerId(),
	); err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	e.setAgentInfoFromWorker(ctx, effect.JobID, strings.TrimSpace(result.GetWorkerId()))
	if err := e.processDurableSaga(ctx, result); err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	if err := e.publishSchedulerAcceptedPacket(capsdk.SubjectAcceptedResult, packet); err != nil {
		return err
	}
	if err := e.processDurableFailure(effect.JobID, envelope, result); err != nil {
		return err
	}
	if err := e.startDurableAsyncOutputCheck(effect.JobID, envelope, result); err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	acked, err := durable.AckJobEffect(ctx, effect)
	if err != nil {
		return RetryAfter(fmt.Errorf("ack durable result effect: %w", err), retryDelayStore)
	}
	if !acked {
		return RetryAfter(errors.New("ack durable result effect rejected"), retryDelayStore)
	}
	e.recordDurableCompletion(envelope, result)
	return nil
}
