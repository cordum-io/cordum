package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func (e *Engine) acceptProductionDispatchEvent(
	packet *pb.BusPacket, dispatch *pb.DispatchIdentity, claims *SessionTokenClaims, kind string,
) (bool, error) {
	if packet == nil || dispatch == nil || claims == nil || e.jobStore == nil {
		return false, nil
	}
	messageID, digest, err := signedEventEvidence(packet)
	if err != nil {
		return false, nil
	}
	jobID := productionEventJobID(packet)
	if jobID == "" {
		return false, nil
	}
	durable, ok := e.jobStore.(model.DurableJobEventStore)
	if !ok {
		return false, RetryAfter(errors.New("production durable job event store unavailable"), retryDelayStore)
	}
	ctx, cancel := context.WithTimeout(e.ctx, storeOpTimeout)
	defer cancel()
	disposition, err := durable.AcceptSignedJobEvent(
		ctx, jobID, dispatch.GetDispatchId(), int(dispatch.GetAttempt()),
		strings.TrimSpace(claims.Subject), strings.TrimSpace(claims.Tenant), messageID, digest,
	)
	if err != nil {
		if errors.Is(err, store.ErrJobEventDigestConflict) {
			slog.Error("production job event rejected", "job_id", jobID, "kind", kind, "reason", "message_digest_conflict")
			return false, nil
		}
		return false, RetryAfter(err, retryDelayStore)
	}
	return disposition == model.JobEventApplied, nil
}

func productionEventJobID(packet *pb.BusPacket) string {
	if progress := packet.GetJobProgress(); progress != nil {
		return strings.TrimSpace(progress.GetJobId())
	}
	if cancel := packet.GetJobCancel(); cancel != nil {
		return strings.TrimSpace(cancel.GetJobId())
	}
	return ""
}

func (e *Engine) handleProductionProgress(
	packet *pb.BusPacket, progress *pb.JobProgress, claims *SessionTokenClaims,
) error {
	accepted, err := e.acceptProductionDispatchEvent(packet, progress.GetDispatch(), claims, "progress")
	if err != nil || !accepted {
		return err
	}
	return e.publishSchedulerAcceptedPacket(capsdk.SubjectAcceptedProgress, packet)
}

func (e *Engine) productionCancelAuthorized(
	packet *pb.BusPacket, cancel *pb.JobCancel, claims *SessionTokenClaims,
) (bool, error) {
	if claims != nil && servicetoken.IsReservedIdentity(strings.TrimSpace(claims.Subject)) {
		return true, nil
	}
	return false, nil
}

func (e *Engine) handleProductionWorkerCancel(
	packet *pb.BusPacket, cancel *pb.JobCancel, claims *SessionTokenClaims,
) error {
	if cancel.GetDispatch() == nil || claims == nil {
		return nil
	}
	durable, ok := e.jobStore.(model.DurableJobEventStore)
	if !ok {
		return RetryAfter(errors.New("production durable job event store unavailable"), retryDelayStore)
	}
	messageID, digest, err := signedEventEvidence(packet)
	if err != nil {
		return nil
	}
	result := &pb.JobResult{
		JobId: cancel.GetJobId(), WorkerId: claims.Subject, Status: pb.JobStatus_JOB_STATUS_CANCELLED,
		Dispatch: cancel.GetDispatch(), Identity: cancel.GetIdentity(), ErrorMessage: cancel.GetReason(),
	}
	topic, err := e.loadResultTopic(cancel.GetJobId())
	if err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	effect, err := marshalDurableResultEffect(
		result, model.JobStateCancelled, topic, "", claims.Subject,
	)
	if err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	dispatch := cancel.GetDispatch()
	apply := model.JobResultApply{
		JobID: cancel.GetJobId(), DispatchID: dispatch.GetDispatchId(), Attempt: int(dispatch.GetAttempt()),
		WorkerID: claims.Subject, Tenant: claims.Tenant, MessageID: messageID, Digest: digest,
		State: model.JobStateCancelled, Effect: effect,
	}
	disposition, err := durable.ApplyJobResult(e.ctx, apply)
	if err != nil {
		if errors.Is(err, store.ErrJobEventDigestConflict) {
			slog.Error("production worker cancel rejected", "job_id", cancel.GetJobId(), "reason", "message_digest_conflict")
			return nil
		}
		return RetryAfter(err, retryDelayStore)
	}
	if disposition == model.JobEventRejected {
		return nil
	}
	return e.drainPendingJobEffects(e.ctx, durable, 100)
}

func (e *Engine) handleProductionServiceCancel(cancel *pb.JobCancel) error {
	if cancel == nil || strings.TrimSpace(cancel.GetJobId()) == "" {
		return nil
	}
	durable, ok := e.jobStore.(model.DurableJobEventStore)
	if !ok {
		return RetryAfter(errors.New("production durable job event store unavailable"), retryDelayStore)
	}
	state, err := durable.CancelAllJobAttempts(e.ctx, cancel.GetJobId())
	if err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	if err := durable.ProjectJobResult(e.ctx, cancel.GetJobId(), state, "", ""); err != nil {
		return RetryAfter(err, retryDelayStore)
	}
	return nil
}

func (e *Engine) startDurableJobEffectReconciler(durable model.DurableJobEventStore) error {
	if err := e.drainPendingJobEffects(e.ctx, durable, 100); err != nil {
		return err
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-e.ctx.Done():
				return
			case <-ticker.C:
				if err := e.drainPendingJobEffects(e.ctx, durable, 100); err != nil {
					slog.Warn("durable job effect retry failed", "error", err)
				}
			}
		}
	}()
	return nil
}
