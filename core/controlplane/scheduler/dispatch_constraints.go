package scheduler

import (
	"context"
	"fmt"
	"slices"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var schedulerConstraintKinds = map[string]struct{}{
	"budgets.max_runtime_ms":      {},
	"budgets.max_retries":         {},
	"budgets.max_concurrent_jobs": {},
}

// requiredWorkerConstraintKinds returns populated leaves not handled by the
// scheduler. Empty sub-messages carry no constraints, including those emitted
// by the kernel for budget-only decisions.
func requiredWorkerConstraintKinds(constraints *pb.PolicyConstraints) []string {
	if constraints == nil {
		return nil
	}
	var kinds []string
	var visit func(protoreflect.Message, string)
	visit = func(message protoreflect.Message, prefix string) {
		message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			kind := prefix + string(field.Name())
			if field.Message() != nil && !field.IsList() && !field.IsMap() {
				visit(value.Message(), kind+".")
			} else if _, handled := schedulerConstraintKinds[kind]; !handled {
				kinds = append(kinds, kind)
			}
			return true
		})
	}
	visit(constraints.ProtoReflect(), "")
	slices.Sort(kinds)
	return kinds
}

func missingWorkerConstraintKinds(kinds []string, worker *pb.Heartbeat) []string {
	var missing []string
	for _, kind := range kinds {
		if !slices.Contains(worker.GetCapabilities(), "enforces."+kind) {
			missing = append(missing, kind)
		}
	}
	return missing
}

func workersDeclaringConstraints(workers map[string]*pb.Heartbeat, kinds []string) map[string]*pb.Heartbeat {
	if len(kinds) == 0 {
		return workers
	}
	declared := make(map[string]*pb.Heartbeat)
	for id, worker := range workers {
		if len(missingWorkerConstraintKinds(kinds, worker)) == 0 {
			declared[id] = worker
		}
	}
	if len(declared) > 0 {
		return declared
	}
	// Keep existing scheduling retries when no worker is available. A selected
	// target without declarations is denied separately before dispatch.
	return workers
}

func (e *Engine) denyMissingDispatchConstraints(ctx context.Context, req *pb.JobRequest, subject string, workers map[string]*pb.Heartbeat, kinds []string) (bool, error) {
	workerID := extractWorkerFromSubject(subject)
	var worker *pb.Heartbeat
	if workerID != "" {
		worker = workers[workerID]
	}
	missing := missingWorkerConstraintKinds(kinds, worker)
	if len(missing) == 0 {
		return false, nil
	}
	reason := fmt.Sprintf("worker %q on subject %q lacks constraint declarations for: %s", workerID, subject, strings.Join(missing, ", "))
	if err := e.setJobState(ctx, req.GetJobId(), JobStateDenied); err != nil {
		return true, RetryAfter(err, retryDelayStore)
	}
	e.incSafetyDenied(req.GetTopic())
	if err := e.emitDLQWithRetry(req.GetJobId(), req.GetTopic(), pb.JobStatus_JOB_STATUS_DENIED, reason, "constraint_unenforced"); err != nil {
		return true, RetryAfter(err, retryDelayPublish)
	}
	return true, nil
}
