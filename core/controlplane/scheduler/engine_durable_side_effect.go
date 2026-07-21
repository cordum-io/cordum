package scheduler

import (
	"context"
	"strings"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func (e *Engine) processDurableSaga(ctx context.Context, result *pb.JobResult) error {
	if e.saga == nil || result == nil {
		return nil
	}
	req, err := e.loadDurableJobRequest(result.GetJobId())
	if err != nil {
		return err
	}
	switch result.GetStatus() {
	case pb.JobStatus_JOB_STATUS_SUCCEEDED:
		return e.saga.RecordCompensation(ctx, req)
	case pb.JobStatus_JOB_STATUS_FAILED_FATAL:
		workflowID := strings.TrimSpace(req.GetWorkflowId())
		if workflowID == "" {
			workflowID = strings.TrimSpace(req.GetLabels()["workflow_id"])
		}
		if workflowID != "" {
			return e.saga.Rollback(ctx, workflowID)
		}
	}
	return nil
}

func (e *Engine) processDurableFailure(
	jobID string, effect durableResultEffect, result *pb.JobResult,
) error {
	if effect.State == model.JobStateSucceeded || effect.State == model.JobStateRetrying {
		return nil
	}
	reason, code := result.GetErrorMessage(), result.GetErrorCode()
	status := result.GetStatus()
	if effect.State == model.JobStateQuarantined {
		status, code = pb.JobStatus_JOB_STATUS_DENIED, outputPolicyReason
		if strings.TrimSpace(reason) == "" {
			reason = "output quarantined by policy"
		}
		e.emitOutputAuditEvent(jobID, effect.Topic, code, reason, OutputQuarantine)
	}
	if err := e.emitDLQWithRetry(jobID, effect.Topic, status, reason, code); err != nil {
		return RetryAfter(err, retryDelayPublish)
	}
	return nil
}

func (e *Engine) startDurableAsyncOutputCheck(
	jobID string, effect durableResultEffect, result *pb.JobResult,
) error {
	if effect.State != model.JobStateSucceeded || !e.outputSafetyEnabled.Load() || e.outputSafety == nil {
		return nil
	}
	req, err := e.loadDurableJobRequest(jobID)
	if err != nil {
		return err
	}
	e.startAsyncOutputCheck(jobID, effect.Topic, result, req)
	return nil
}

func (e *Engine) recordDurableCompletion(effect durableResultEffect, result *pb.JobResult) {
	status := result.GetStatus().String()
	if effect.State == model.JobStateQuarantined {
		status = string(model.JobStateQuarantined)
	}
	e.incJobsCompleted(effect.Topic, status)
	if e.otelMetrics != nil {
		e.otelMetrics.RecordJobCompleted(context.Background(), effect.Topic, status)
	}
}
