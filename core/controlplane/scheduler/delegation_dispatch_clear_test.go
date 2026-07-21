package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/infra/config"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// transientLineageJobStore wraps the real job store but fails the
// SetDelegationLineage write with an injectable (retryable) error, so we can
// exercise the dispatch-token clearing logic on a transient failure path.
type transientLineageJobStore struct {
	*infraStore.RedisJobStore
	lineageErr error
}

func (s *transientLineageJobStore) SetDelegationLineage(context.Context, string, model.DelegationLineage) error {
	return s.lineageErr
}

// TestVerifyDelegationBeforeDispatch_PreservesTokenOnTransientLineageFailure
// locks the HIGH fix: ClearDelegationDispatchToken used to run unconditionally
// before SetDelegationLineage. A transient lineage-write error returns a
// RETRYABLE error → the job is redelivered, but the raw token had already been
// wiped, so the redelivery hit "delegation dispatch token missing" and forced
// a valid delegated job to FAILED. The token must survive a retryable failure.
func TestVerifyDelegationBeforeDispatch_PreservesTokenOnTransientLineageFailure(t *testing.T) {
	signingKey := setSchedulerDelegationKeys(t)
	jobStore, agentStore, _ := newDelegationDispatchTestStore(t)
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-a", []string{"read", "write"}, []string{"job.default"})
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-b", []string{"read"}, []string{"job.default"})

	token, verified := issueSchedulerDelegationToken(t, jobStore, agentStore, signingKey, "default", "agent-a", "agent-b")
	jobID := "job-delegation-transient-lineage"
	if err := jobStore.SetDelegationDispatchToken(context.Background(), jobID, model.DelegationDispatchToken{
		Token:    token,
		Audience: "agent-b",
	}); err != nil {
		t.Fatalf("SetDelegationDispatchToken() error = %v", err)
	}

	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.default",
		TenantId: "default",
		Labels: map[string]string{
			config.LabelDelegationDepth:       "1",
			config.LabelDelegationIssuerChain: "agent-a",
			config.LabelDelegationJTI:         verified.JTI,
		},
	}

	failing := &transientLineageJobStore{
		RedisJobStore: jobStore,
		lineageErr:    errors.New("redis: connection reset by peer"),
	}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), failing, nil)

	_, _, err := engine.verifyDelegationBeforeDispatch(testCtx(t), req)
	if err == nil {
		t.Fatalf("verifyDelegationBeforeDispatch() error = nil, want transient lineage error")
	}
	if _, _, retry := classifyDelegationDispatchError(err); !retry {
		t.Fatalf("classifyDelegationDispatchError(%v) retry = false, want true (transient store error)", err)
	}

	got, getErr := jobStore.GetDelegationDispatchToken(context.Background(), jobID)
	if getErr != nil {
		t.Fatalf("GetDelegationDispatchToken() error = %v", getErr)
	}
	if got.Token != token {
		t.Fatalf("dispatch token after transient failure = %q, want it preserved for redelivery", got.Token)
	}
}

// TestVerifyDelegationBeforeDispatch_ClearsTokenOnSuccess is the positive
// control: on a successful verify + lineage persist, the raw bearer token MUST
// still be wiped from the 7-day metadata (the security guarantee the original
// unconditional clear was protecting).
func TestVerifyDelegationBeforeDispatch_ClearsTokenOnSuccess(t *testing.T) {
	signingKey := setSchedulerDelegationKeys(t)
	jobStore, agentStore, _ := newDelegationDispatchTestStore(t)
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-a", []string{"read", "write"}, []string{"job.default"})
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-b", []string{"read"}, []string{"job.default"})

	token, verified := issueSchedulerDelegationToken(t, jobStore, agentStore, signingKey, "default", "agent-a", "agent-b")
	jobID := "job-delegation-success-clears"
	if err := jobStore.SetDelegationDispatchToken(context.Background(), jobID, model.DelegationDispatchToken{
		Token:    token,
		Audience: "agent-b",
	}); err != nil {
		t.Fatalf("SetDelegationDispatchToken() error = %v", err)
	}

	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.default",
		TenantId: "default",
		Labels: map[string]string{
			config.LabelDelegationDepth:       "1",
			config.LabelDelegationIssuerChain: "agent-a",
			config.LabelDelegationJTI:         verified.JTI,
		},
	}

	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil)
	if _, _, err := engine.verifyDelegationBeforeDispatch(testCtx(t), req); err != nil {
		t.Fatalf("verifyDelegationBeforeDispatch() error = %v, want success", err)
	}

	got, getErr := jobStore.GetDelegationDispatchToken(context.Background(), jobID)
	if getErr != nil {
		t.Fatalf("GetDelegationDispatchToken() error = %v", getErr)
	}
	if got.Token != "" {
		t.Fatalf("dispatch token after success = %q, want cleared", got.Token)
	}
}
