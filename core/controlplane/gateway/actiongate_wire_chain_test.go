package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
)

func TestWireActionGatePipeline_ValidApprovalAndAuditChainAllows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _, _ := newTestGateway(t)
	edgeStore := edgecore.NewRedisStoreFromClient(s.redisClient())
	s.edgeStore = edgeStore
	action := wirePipelineDestructiveAction("")
	approval := seedApprovedWirePipelineApproval(t, ctx, edgeStore, "tenant-chain-ok", "ok", action)
	action.ApprovalClaim.ApprovalRef = approval.ApprovalRef
	appendVerifierTestEvents(t, s.auditChainer, "tenant-chain-ok", 2)
	appendApprovalEvidenceEvent(t, s.auditChainer, approval, nil)

	s.wireActionGatePipeline()
	dec, fired := s.actionGatePipeline.Run(wirePipelineAuthContext("tenant-chain-ok"), &config.PolicyInput{
		Tenant: "tenant-chain-ok",
		Action: action,
	})

	if fired {
		t.Fatalf("pipeline fired blocking decision %v / %q / %q", dec.Decision, dec.Code, dec.SubReason)
	}
	if dec.Extra["sub_reason"] != "provenance_ok:ok" {
		t.Fatalf("merged sub_reason = %q, want provenance_ok:ok (extra=%v)", dec.Extra["sub_reason"], dec.Extra)
	}
}

func TestWireActionGatePipeline_NilAuditChainerIsServiceUnavailableAfterApproval(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _, _ := newTestGateway(t)
	edgeStore := edgecore.NewRedisStoreFromClient(s.redisClient())
	s.edgeStore = edgeStore
	s.auditChainer = nil
	action := wirePipelineDestructiveAction("")
	approval := seedApprovedWirePipelineApproval(t, ctx, edgeStore, "tenant-chain-nil", "nil", action)
	action.ApprovalClaim.ApprovalRef = approval.ApprovalRef

	s.wireActionGatePipeline()
	dec, fired := s.actionGatePipeline.Run(wirePipelineAuthContext("tenant-chain-nil"), &config.PolicyInput{
		Tenant: "tenant-chain-nil",
		Action: action,
	})

	if !fired || dec.GateID != actiongates.GateIDProvenance {
		t.Fatalf("got fired=%v gate=%q decision=%v; want provenance fail-closed after approval", fired, dec.GateID, dec.Decision)
	}
	if dec.Code != actiongates.CodeServiceUnavailable {
		t.Fatalf("code = %q, want %q (sub_reason=%q)", dec.Code, actiongates.CodeServiceUnavailable, dec.SubReason)
	}
	if dec.Code == actiongates.CodeInternalError {
		t.Fatal("nil audit verifier must not surface as surprise internal_error")
	}
	if !strings.HasPrefix(dec.SubReason, "audit_chain_verifier_unavailable") &&
		!strings.HasPrefix(dec.SubReason, "audit_chain_verify_failed") {
		t.Fatalf("sub_reason = %q, want deterministic audit-chain unavailable/failed reason", dec.SubReason)
	}
}

func wirePipelineDestructiveAction(approvalRef string) *config.ActionDescriptor {
	return &config.ActionDescriptor{
		Kind: config.ActionKindMutation,
		Verb: config.ActionVerbDelete,
		TargetResource: &config.ActionTargetResource{
			Type:        "secret",
			ID:          "secret-123",
			OwnerTenant: "",
		},
		ApprovalClaim: &config.ActionApprovalClaim{ApprovalRef: approvalRef},
	}
}

func seedApprovedWirePipelineApproval(
	t *testing.T,
	ctx context.Context,
	store *edgecore.RedisStore,
	tenantID string,
	suffix string,
	action *config.ActionDescriptor,
) *edgecore.EdgeApproval {
	t.Helper()
	started := time.Now().UTC().Add(-2 * time.Minute)
	sessionID, executionID, eventID := "sess-"+suffix, "exec-"+suffix, "event-"+suffix
	createGatewayApprovalParents(t, ctx, store, tenantID, sessionID, executionID, eventID, started)
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	pending, err := store.EnqueueApproval(ctx, edgecore.EdgeApprovalRequest{
		TenantID:       tenantID,
		SessionID:      sessionID,
		ExecutionID:    executionID,
		EventID:        eventID,
		PrincipalID:    "agent-requester",
		Requester:      "agent-requester",
		Reason:         "destructive action approval",
		RuleID:         "actiongate.pipeline.test",
		PolicySnapshot: "policy-v1",
		ActionHash:     actiongates.CanonicalActionHash(action),
		InputHash:      "sha256:" + eventID,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		t.Fatalf("EnqueueApproval: %v", err)
	}
	approved, err := store.ApproveApproval(ctx, edgecore.ApprovalResolution{
		TenantID:    tenantID,
		ApprovalRef: pending.ApprovalRef,
		ResolverID:  "human-reviewer",
		ResolvedBy:  "human-reviewer",
		Reason:      "approved for production pipeline test",
		ResolvedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("ApproveApproval: %v", err)
	}
	return approved
}

func wirePipelineAuthContext(tenant string) context.Context {
	return context.WithValue(context.Background(), auth.ContextKey{}, &auth.AuthContext{
		Tenant:      tenant,
		PrincipalID: "agent-requester",
		Role:        "user",
	})
}
