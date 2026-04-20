package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRunGovernanceBackfillIsIdempotent(t *testing.T) {
	h := newGovernanceTestHarness(t)
	defer func() { _ = h.runtime.Close() }()
	defer func() { _ = h.jobStore.Close() }()
	defer func() { _ = h.decisionLogStore.Close() }()

	now := time.Now().UTC().Add(-1 * time.Hour).Truncate(time.Millisecond)
	seedGovernanceJob(t, h, &pb.JobRequest{
		JobId:    "job-backfill-idempotent",
		Topic:    "payments.transfer",
		TenantId: "tenant-a",
		Labels:   map[string]string{"agent_id": "agent-a"},
	}, model.SafetyDecisionRecord{
		Decision:       model.SafetyRequireApproval,
		RuleID:         "payments-high-value-approval",
		Reason:         "manual review required",
		PolicySnapshot: "snap-2026-04-20|abc123",
		CheckedAt:      now.UnixMicro(),
		ApprovalStatus: model.ApprovalStatusPending,
	})

	first, err := runGovernanceBackfill(context.Background(), h.runtime, governanceBackfillConfig{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if first.Appended != 1 {
		t.Fatalf("first appended = %d, want 1", first.Appended)
	}

	second, err := runGovernanceBackfill(context.Background(), h.runtime, governanceBackfillConfig{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if second.SkippedExisting != 1 {
		t.Fatalf("second skipped_existing = %d, want 1", second.SkippedExisting)
	}

	page, err := h.decisionLogStore.QueryDecisions(context.Background(), model.DecisionQuery{Tenant: "tenant-a", Since: now.Add(-time.Hour).UnixMilli(), Until: now.Add(time.Hour).UnixMilli(), Limit: 10})
	if err != nil {
		t.Fatalf("query decisions: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("decision count = %d, want 1", len(page.Items))
	}
	if got := page.Items[0].JobID; got != "job-backfill-idempotent" {
		t.Fatalf("job id = %q, want job-backfill-idempotent", got)
	}
}

func TestRunGovernanceBackfillDryRunEmitsNoWrites(t *testing.T) {
	h := newGovernanceTestHarness(t)
	defer func() { _ = h.runtime.Close() }()
	defer func() { _ = h.jobStore.Close() }()
	defer func() { _ = h.decisionLogStore.Close() }()

	now := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	seedGovernanceJob(t, h, &pb.JobRequest{JobId: "job-backfill-dry-run", Topic: "govern.audit", TenantId: "tenant-a"}, model.SafetyDecisionRecord{Decision: model.SafetyDeny, RuleID: "govern-block", Reason: "denied by policy", CheckedAt: now.UnixMicro()})

	summary, err := runGovernanceBackfill(context.Background(), h.runtime, governanceBackfillConfig{DryRun: true}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("dry-run backfill: %v", err)
	}
	if summary.WouldAppend != 1 {
		t.Fatalf("would_append = %d, want 1", summary.WouldAppend)
	}
	if summary.Appended != 0 {
		t.Fatalf("appended = %d, want 0", summary.Appended)
	}

	page, err := h.decisionLogStore.QueryDecisions(context.Background(), model.DecisionQuery{Tenant: "tenant-a", Since: now.Add(-time.Hour).UnixMilli(), Until: now.Add(time.Hour).UnixMilli(), Limit: 10})
	if err != nil {
		t.Fatalf("query decisions after dry-run: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("decision count after dry-run = %d, want 0", len(page.Items))
	}
}

func TestRunGovernanceBackfillRespectsTimeWindow(t *testing.T) {
	h := newGovernanceTestHarness(t)
	defer func() { _ = h.runtime.Close() }()
	defer func() { _ = h.jobStore.Close() }()
	defer func() { _ = h.decisionLogStore.Close() }()

	base := time.Now().UTC().Truncate(time.Millisecond)
	inWindow := base.Add(-2 * time.Hour)
	outOfWindow := base.Add(-48 * time.Hour)

	seedGovernanceJob(t, h, &pb.JobRequest{JobId: "job-window-in", Topic: "topic.in", TenantId: "tenant-a"}, model.SafetyDecisionRecord{Decision: model.SafetyAllowWithConstraints, RuleID: "rule-in", Reason: "constrained", CheckedAt: inWindow.UnixMicro()})
	seedGovernanceJob(t, h, &pb.JobRequest{JobId: "job-window-out", Topic: "topic.out", TenantId: "tenant-a"}, model.SafetyDecisionRecord{Decision: model.SafetyThrottle, RuleID: "rule-out", Reason: "too fast", CheckedAt: outOfWindow.UnixMicro()})

	since := base.Add(-6 * time.Hour)
	until := base.Add(-1 * time.Hour)
	summary, err := runGovernanceBackfill(context.Background(), h.runtime, governanceBackfillConfig{Since: &since, Until: &until}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("windowed backfill: %v", err)
	}
	if summary.Appended != 1 {
		t.Fatalf("appended = %d, want 1", summary.Appended)
	}
	if summary.OutOfRange != 1 {
		t.Fatalf("out_of_range = %d, want 1", summary.OutOfRange)
	}

	page, err := h.decisionLogStore.QueryDecisions(context.Background(), model.DecisionQuery{Tenant: "tenant-a", Since: since.UnixMilli(), Until: until.UnixMilli(), Limit: 10})
	if err != nil {
		t.Fatalf("query decisions in window: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("decision count = %d, want 1", len(page.Items))
	}
	if got := page.Items[0].JobID; got != "job-window-in" {
		t.Fatalf("windowed job id = %q, want job-window-in", got)
	}
}

func TestRunGovernanceTailAppendsAndSkipsExisting(t *testing.T) {
	h := newGovernanceTestHarness(t)
	defer func() { _ = h.runtime.Close() }()
	defer func() { _ = h.jobStore.Close() }()
	defer func() { _ = h.decisionLogStore.Close() }()

	now := time.Now().UTC().Truncate(time.Millisecond)
	packet := newGovernancePacket(t, governanceAuditEvent{
		Timestamp:     now,
		EventType:     "safety.decision",
		TenantID:      "tenant-tail",
		AgentID:       "agent-tail",
		JobID:         "job-tail",
		Decision:      "deny",
		MatchedRule:   "tail-rule",
		Reason:        "tail replay",
		PolicyVersion: "snap-tail",
		Extra:         map[string]string{"topic": "topic.tail"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	subscriber := fakeGovernanceSubscriber{packets: [][]byte{packet, packet}, done: cancel}
	summary, err := runGovernanceTail(ctx, subscriber, h.runtime, &bytes.Buffer{})
	if err != context.Canceled {
		t.Fatalf("tail err = %v, want context.Canceled", err)
	}
	if summary.Appended != 1 {
		t.Fatalf("tail appended = %d, want 1", summary.Appended)
	}
	if summary.SkippedExisting != 1 {
		t.Fatalf("tail skipped_existing = %d, want 1", summary.SkippedExisting)
	}

	page, err := h.decisionLogStore.QueryDecisions(context.Background(), model.DecisionQuery{Tenant: "tenant-tail", Since: now.Add(-time.Hour).UnixMilli(), Until: now.Add(time.Hour).UnixMilli(), Limit: 10})
	if err != nil {
		t.Fatalf("query tail decisions: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("tail decision count = %d, want 1", len(page.Items))
	}
}

type fakeGovernanceSubscriber struct {
	packets [][]byte
	done    func()
}

func (f fakeGovernanceSubscriber) Subscribe(handler func([]byte) error) error {
	go func() {
		defer f.done()
		for _, packet := range f.packets {
			_ = handler(packet)
		}
	}()
	return nil
}

type governanceTestHarness struct {
	runtime          *governanceRuntime
	jobStore         *infraStore.RedisJobStore
	decisionLogStore *infraStore.RedisDecisionLogStore
}

func newGovernanceTestHarness(t *testing.T) *governanceTestHarness {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	redisURL := "redis://" + srv.Addr()
	runtime, err := newGovernanceRuntime(redisURL)
	if err != nil {
		t.Fatalf("new governance runtime: %v", err)
	}
	jobStore, err := infraStore.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("new job store: %v", err)
	}
	decisionLogStore, err := infraStore.NewRedisDecisionLogStore(redisURL)
	if err != nil {
		t.Fatalf("new decision log store: %v", err)
	}
	return &governanceTestHarness{runtime: runtime, jobStore: jobStore, decisionLogStore: decisionLogStore}
}

func seedGovernanceJob(t *testing.T, h *governanceTestHarness, req *pb.JobRequest, safety model.SafetyDecisionRecord) {
	t.Helper()
	if err := h.jobStore.SetJobRequest(context.Background(), req); err != nil {
		t.Fatalf("set job request %s: %v", req.GetJobId(), err)
	}
	if err := h.jobStore.SetSafetyDecision(context.Background(), req.GetJobId(), safety); err != nil {
		t.Fatalf("set safety decision %s: %v", req.GetJobId(), err)
	}
}

func newGovernancePacket(t *testing.T, event governanceAuditEvent) []byte {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal audit event: %v", err)
	}
	packet := &pb.BusPacket{
		SenderId:        "governance-test",
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload:         &pb.BusPacket_Alert{Alert: &pb.SystemAlert{SourceComponent: "audit-export", Message: string(payload)}},
	}
	encoded, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal bus packet: %v", err)
	}
	return encoded
}
