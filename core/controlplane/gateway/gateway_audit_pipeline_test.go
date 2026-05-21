package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/licensing"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

// requireChainHas waits up to 2s for the tenant stream to contain at least
// `minEvents` chain entries with a clean verify status. Polling absorbs any
// buffered-exporter async forwarding that happens after the synchronous
// chain append; the happy path returns on the first iteration.
func requireChainHas(t *testing.T, client redis.UniversalClient, chainer *audit.Chainer, tenant string, minEvents int) {
	t.Helper()
	streamKey := chainer.StreamKey(tenant)
	deadline := time.Now().Add(2 * time.Second)
	var last *audit.VerifyResult
	for time.Now().Before(deadline) {
		result, err := audit.VerifyChain(context.Background(), client, streamKey, audit.VerifyOptions{})
		if err != nil {
			t.Fatalf("VerifyChain: %v", err)
		}
		last = result
		if result != nil && result.Status == audit.VerifyStatusOK && result.TotalEvents >= minEvents && len(result.Gaps) == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("verify result = %+v, want status=ok total_events>=%d gaps=0", last, minEvents)
}

// TestInitAuditPipeline_ChainerSurvivesBlockedSIEMEntitlement pins the
// invariant that the audit chain stays live even when the plan's SIEM export
// entitlement is off. Today initAuditPipeline short-circuits with
// (nil, nil, nil) whenever NewExporterFromEnvWithEntitlements returns a nil
// buffered exporter, which happens the moment the SIEM entitlement is blocked
// for a non-discard export type. After the decoupling fix the chainer must be
// instantiated unconditionally and the sender must still accept writes.
func TestInitAuditPipeline_ChainerSurvivesBlockedSIEMEntitlement(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://test.local/hook")
	t.Setenv("AUDIT_TRANSPORT", "")

	s, _, _ := newTestGateway(t)
	s.entitlements.ForceState(
		licensing.PlanCommunity,
		licensing.DefaultEntitlements(licensing.PlanCommunity),
		nil,
	)

	sender, chainer, err := initAuditPipeline(s.redisClient(), nil, s.entitlements)
	if err != nil {
		t.Fatalf("initAuditPipeline: %v", err)
	}
	if chainer == nil {
		t.Fatal("chainer is nil; chain must stay live even when SIEM export is blocked by entitlement")
	}
	if sender == nil {
		t.Fatal("auditSender is nil; expected chain-only wrapper so downstream writes still reach the chain")
	}
	t.Cleanup(func() { _ = sender.Close() })

	sender.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSafetyApproval,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "blocked-siem-chain-only",
		JobID:     "job-blocked-siem",
	})

	requireChainHas(t, s.redisClient(), chainer, "default", 1)
}

// TestInitAuditPipeline_ChainerOnChainOnlyMode pins the invariant that an
// unset CORDUM_AUDIT_EXPORT_TYPE on an Enterprise deployment instantiates the
// chain and routes writes through it without requiring a DiscardExporter
// incantation. Today the direct path assigns `auditSender = bufExporter`, so
// the DiscardExporter swallows the event and the chain stream stays empty.
// After the fix the chain sees the event even with no downstream exporter.
func TestInitAuditPipeline_ChainerOnChainOnlyMode(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "")
	t.Setenv("AUDIT_TRANSPORT", "")

	s, _, _ := newTestGateway(t)
	s.entitlements.ForceState(
		licensing.PlanEnterprise,
		licensing.DefaultEntitlements(licensing.PlanEnterprise),
		nil,
	)

	sender, chainer, err := initAuditPipeline(s.redisClient(), nil, s.entitlements)
	if err != nil {
		t.Fatalf("initAuditPipeline: %v", err)
	}
	if chainer == nil {
		t.Fatal("chainer is nil; chain-only mode must still instantiate the chain")
	}
	if sender == nil {
		t.Fatal("auditSender is nil; chain-only mode must still accept writes")
	}
	t.Cleanup(func() { _ = sender.Close() })

	sender.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSafetyApproval,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "chain-only-mode",
		JobID:     "job-chain-only",
	})

	requireChainHas(t, s.redisClient(), chainer, "default", 1)
}

// TestInitAuditPipeline_DirectTransportFeedsChain pins the invariant that an
// Enterprise deployment using direct transport (AUDIT_TRANSPORT unset) with a
// real SIEM exporter configured still feeds the tamper-evident chain on the
// write path. Today the direct transport assigns bufExporter to auditSender
// without the chain wrapper, so only the NATS consumer path appends to the
// chain; the direct-mode operator sees an empty chain stream. After the fix
// every audit write goes through the chain wrapper first.
func TestInitAuditPipeline_DirectTransportFeedsChain(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://test.local/hook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET", "12345678901234567890123456789012")
	t.Setenv("AUDIT_TRANSPORT", "")

	s, _, _ := newTestGateway(t)
	s.entitlements.ForceState(
		licensing.PlanEnterprise,
		licensing.DefaultEntitlements(licensing.PlanEnterprise),
		nil,
	)
	run := &wf.WorkflowRun{
		ID:         "run-direct-audit",
		WorkflowID: "wf-direct-audit",
		Status:     wf.RunStatusRunning,
		Steps: map[string]*wf.StepRun{
			"step-1": {StepID: "step-1", Status: wf.StepStatusRunning, JobID: "run-direct-audit:step-1@1"},
		},
	}
	if err := s.workflowStore.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	sender, chainer, err := initAuditPipeline(s.redisClient(), nil, s.entitlements, s.workflowStore)
	if err != nil {
		t.Fatalf("initAuditPipeline: %v", err)
	}
	if chainer == nil {
		t.Fatal("chainer is nil; direct transport must still instantiate the chain")
	}
	if sender == nil {
		t.Fatal("auditSender is nil; direct transport must still accept writes")
	}
	t.Cleanup(func() { _ = sender.Close() })

	sender.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSafetyApproval,
		Severity:  audit.SeverityInfo,
		TenantID:  "default",
		Action:    "direct-transport-chain",
		JobID:     run.Steps["step-1"].JobID,
	})

	requireChainHas(t, s.redisClient(), chainer, "default", 1)
	updated, err := s.workflowStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Steps["step-1"].AuditHash == "" {
		t.Fatal("expected direct audit chain sender to populate workflow step audit hash")
	}
}
