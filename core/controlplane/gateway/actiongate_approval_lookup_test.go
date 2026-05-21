package gateway

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/internal/testredis"
)

func TestEdgeStoreApprovalLookup_LookupByApprovalRefTenantScoped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	store := edgecore.NewRedisStoreFromClient(client, edgecore.WithClock(func() time.Time { return base }))
	approval := seedGatewayActionGateApproval(t, ctx, store, "tenant-a", "approval-ref")
	lookup := edgeStoreApprovalLookup{store: store}

	got, ok, err := lookup.LookupByApprovalRef(ctx, "tenant-a", approval.ApprovalRef)
	if err != nil {
		t.Fatalf("LookupByApprovalRef real ref err: %v", err)
	}
	if !ok || got == nil || got.ApprovalRef != approval.ApprovalRef {
		t.Fatalf("LookupByApprovalRef real ref = (%#v,%v), want ref %q", got, ok, approval.ApprovalRef)
	}

	missing, ok, err := lookup.LookupByApprovalRef(ctx, "tenant-a", "edge_appr_missing")
	if err != nil || ok || missing != nil {
		t.Fatalf("LookupByApprovalRef fake ref = (%#v,%v,%v), want nil,false,nil", missing, ok, err)
	}
	foreign, ok, err := lookup.LookupByApprovalRef(ctx, "tenant-b", approval.ApprovalRef)
	if err != nil || ok || foreign != nil {
		t.Fatalf("LookupByApprovalRef cross-tenant ref = (%#v,%v,%v), want nil,false,nil", foreign, ok, err)
	}
}

func seedGatewayActionGateApproval(
	t *testing.T,
	ctx context.Context,
	store *edgecore.RedisStore,
	tenantID string,
	suffix string,
) *edgecore.EdgeApproval {
	t.Helper()
	sessionID := "sess-" + suffix
	executionID := "exec-" + suffix
	eventID := "event-" + suffix
	started := time.Date(2026, 5, 21, 9, 1, 0, 0, time.UTC)
	createGatewayApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, started)
	approval, err := store.EnqueueApproval(ctx, edgecore.EdgeApprovalRequest{
		TenantID:       tenantID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		EventID:        eventID,
		PrincipalID:    "principal-requester",
		Requester:      "principal-requester",
		Reason:         "approval required",
		RuleID:         "actiongate.test",
		PolicySnapshot: "policy-v1",
		ActionHash:     "actionhash-" + suffix,
		InputHash:      "sha256:" + eventID,
		ExpiresAt:      started.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	return approval
}

func createGatewayApprovalParents(
	t *testing.T,
	ctx context.Context,
	store *edgecore.RedisStore,
	tenantID string,
	sessionID string,
	executionID string,
	eventID string,
	started time.Time,
) {
	t.Helper()
	if err := store.CreateSession(ctx, gatewayApprovalSession(tenantID, sessionID, started)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.CreateExecution(ctx, gatewayApprovalExecution(tenantID, sessionID, executionID, started)); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	event := gatewayApprovalEvent(tenantID, sessionID, executionID, eventID, started.Add(2*time.Second))
	if _, err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
}

func gatewayApprovalSession(tenantID string, sessionID string, started time.Time) edgecore.EdgeSession {
	return edgecore.EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       "principal-requester",
		PrincipalType:     edgecore.PrincipalTypeHuman,
		AgentProduct:      "Claude Code",
		Mode:              edgecore.SessionModeLocalDev,
		PolicyMode:        edgecore.PolicyModeEnforce,
		Status:            edgecore.SessionStatusRunning,
		RiskSummary:       edgecore.RiskSummary{MaxRisk: edgecore.RiskLevelLow},
		StartedAt:         started.UTC(),
		EnforcementLayers: edgecore.EnforcementLayers{"hook": true},
	}
}

func gatewayApprovalExecution(
	tenantID string,
	sessionID string,
	executionID string,
	started time.Time,
) edgecore.AgentExecution {
	return edgecore.AgentExecution{
		ExecutionID: executionID,
		SessionID:   sessionID,
		TenantID:    tenantID,
		Adapter:     edgecore.AdapterClaudeCodeHook,
		Mode:        edgecore.ExecutionModeLocalDev,
		Attempt:     1,
		Status:      edgecore.ExecutionStatusRunning,
		StartedAt:   started.Add(time.Second).UTC(),
		Metrics:     edgecore.ExecutionMetrics{Events: 1},
	}
}

func gatewayApprovalEvent(
	tenantID string,
	sessionID string,
	executionID string,
	eventID string,
	at time.Time,
) edgecore.AgentActionEvent {
	return edgecore.AgentActionEvent{
		EventID:        eventID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		TenantID:       tenantID,
		PrincipalID:    "principal-requester",
		Timestamp:      at.UTC(),
		Layer:          edgecore.LayerHook,
		Kind:           edgecore.EventKindApprovalRequested,
		InputHash:      "sha256:" + eventID,
		Decision:       edgecore.DecisionRequireApproval,
		PolicySnapshot: "policy-v1",
		Status:         edgecore.ActionStatusBlocked,
	}
}
