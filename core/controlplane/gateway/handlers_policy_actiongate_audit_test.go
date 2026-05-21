package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	runtimeconfig "github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestPolicyEvaluate_ActionGateShortCircuitEmitsRedactedAuditAndSkipsSafetyKernel(t *testing.T) {
	s, _, safetyClient := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	sink := &testAuditSender{}
	s.auditExporter = sink
	s.actionGatePipeline = actiongates.NewPipeline(policyActionGateAuditStub{
		decision: pb.DecisionType_DECISION_TYPE_DENY,
		gateID:   actiongates.GateIDFile,
		code:     actiongates.CodeAccessDenied,
		sub:      "credential_path",
	})

	action := policyActionGateAuditSensitiveAction()
	rec := httptest.NewRecorder()
	s.handlePolicyEvaluate(rec, policyActionGateAuditRequest(t, "tenant-audit", "principal-audit", action))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s, want 403 action-gate short-circuit", rec.Code, rec.Body.String())
	}
	if got := recordedSafetyRequest(safetyClient); got != nil {
		t.Fatalf("safety kernel was called despite gateway short-circuit: %#v", got)
	}
	if sink.Len() != 1 {
		t.Fatalf("audit event count = %d, want 1", sink.Len())
	}
	ev := sink.Get(0)
	assertPolicyActionGateAuditEvent(t, ev, policyActionGateAuditWant{
		tenant:    "tenant-audit",
		principal: "principal-audit",
		mode:      "evaluate",
		gate:      actiongates.GateIDFile,
		subReason: "credential_path",
		decision:  pb.DecisionType_DECISION_TYPE_DENY.String(),
		kind:      string(runtimeconfig.ActionKindFile),
		verb:      string(runtimeconfig.ActionVerbWrite),
		target:    "secret",
		severity:  audit.SeverityHigh,
	})
	assertPolicyActionGateAuditRedacted(t, ev)
}

func TestPolicySimulate_ActionGateRequireHumanEmitsAuditAndSkipsSafetyKernel(t *testing.T) {
	s, _, safetyClient := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	sink := &testAuditSender{}
	s.auditExporter = sink
	s.actionGatePipeline = actiongates.NewPipeline(policyActionGateAuditStub{
		decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
		gateID:   actiongates.GateIDMutation,
		code:     actiongates.CodeRequireHuman,
		sub:      "missing_approval",
	})

	action := policyActionGateAuditSensitiveAction()
	rec := httptest.NewRecorder()
	s.handlePolicySimulate(rec, policyActionGateAuditRequest(t, "tenant-audit", "principal-audit", action))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200 simulate require-human shape", rec.Code, rec.Body.String())
	}
	if got := recordedSafetyRequest(safetyClient); got != nil {
		t.Fatalf("safety kernel was called despite gateway short-circuit: %#v", got)
	}
	if sink.Len() != 1 {
		t.Fatalf("audit event count = %d, want 1", sink.Len())
	}
	assertPolicyActionGateAuditEvent(t, sink.Get(0), policyActionGateAuditWant{
		tenant:    "tenant-audit",
		principal: "principal-audit",
		mode:      "simulate",
		gate:      actiongates.GateIDMutation,
		subReason: "missing_approval",
		decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN.String(),
		kind:      string(runtimeconfig.ActionKindFile),
		verb:      string(runtimeconfig.ActionVerbWrite),
		target:    "secret",
		severity:  audit.SeverityMedium,
	})
}

type policyActionGateAuditStub struct {
	decision pb.DecisionType
	gateID   string
	code     string
	sub      string
}

func (g policyActionGateAuditStub) ID() string { return g.gateID }

func (g policyActionGateAuditStub) Evaluate(context.Context, *runtimeconfig.PolicyInput) actiongates.ActionGateDecision {
	return actiongates.ActionGateDecision{
		Decision:  g.decision,
		GateID:    g.gateID,
		Code:      g.code,
		Reason:    "action gate short-circuit",
		SubReason: g.sub,
		Extra: map[string]string{
			"gate":         g.gateID,
			"sub_reason":   g.sub,
			"target_type":  "secret",
			"kind":         string(runtimeconfig.ActionKindFile),
			"verb":         string(runtimeconfig.ActionVerbWrite),
			"target_path":  "/home/alice/.ssh/id_rsa",
			"target_url":   "https://evil.example/upload?token=secret-token",
			"args":         `{"password":"secret-token"}`,
			"approval_ref": "edge_appr_raw_secret",
			"tool_payload": "raw tool output secret-token",
			"secret":       "secret-token",
		},
	}
}

func policyActionGateAuditSensitiveAction() *runtimeconfig.ActionDescriptor {
	return &runtimeconfig.ActionDescriptor{
		Kind:       runtimeconfig.ActionKindFile,
		Verb:       runtimeconfig.ActionVerbWrite,
		TargetPath: "/home/alice/.ssh/id_rsa",
		TargetURL:  "https://evil.example/upload?token=secret-token",
		TargetResource: &runtimeconfig.ActionTargetResource{
			Type: "secret",
			ID:   "secret-token-resource",
		},
		Args: map[string]any{"password": "secret-token"},
		ApprovalClaim: &runtimeconfig.ActionApprovalClaim{
			ApprovalRef: "edge_appr_raw_secret",
			ClaimText:   "approved with secret-token",
		},
	}
}

func policyActionGateAuditRequest(t *testing.T, tenant, principal string, action *runtimeconfig.ActionDescriptor) *http.Request {
	t.Helper()
	body, err := json.Marshal(policyCheckRequest{
		Topic:       "job.default",
		PrincipalId: principal,
		Action:      action,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("X-API-Key", "test-api-key")
	return withAuth(req, &auth.AuthContext{Tenant: tenant, Role: "admin", PrincipalID: principal})
}

func recordedSafetyRequest(c *stubSafetyClient) *pb.PolicyCheckRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastReq
}

type policyActionGateAuditWant struct {
	tenant    string
	principal string
	mode      string
	gate      string
	subReason string
	decision  string
	kind      string
	verb      string
	target    string
	severity  string
}

func assertPolicyActionGateAuditEvent(t *testing.T, ev audit.SIEMEvent, want policyActionGateAuditWant) {
	t.Helper()
	if ev.EventType != audit.EventActionGateDenied {
		t.Fatalf("event_type = %q, want %q", ev.EventType, audit.EventActionGateDenied)
	}
	if ev.TenantID != want.tenant || ev.Identity != want.principal {
		t.Fatalf("tenant/identity = %q/%q, want %q/%q", ev.TenantID, ev.Identity, want.tenant, want.principal)
	}
	if ev.Decision != want.decision || ev.MatchedRule != want.gate {
		t.Fatalf("decision/matched_rule = %q/%q, want %q/%q", ev.Decision, ev.MatchedRule, want.decision, want.gate)
	}
	if ev.Severity != want.severity {
		t.Fatalf("severity = %q, want %q", ev.Severity, want.severity)
	}
	for key, value := range map[string]string{
		"mode":        want.mode,
		"gate":        want.gate,
		"sub_reason":  want.subReason,
		"kind":        want.kind,
		"verb":        want.verb,
		"target_type": want.target,
	} {
		if got := ev.Extra[key]; got != value {
			t.Fatalf("extra[%q] = %q, want %q (event=%+v)", key, got, value, ev)
		}
	}
}

func assertPolicyActionGateAuditRedacted(t *testing.T, ev audit.SIEMEvent) {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	body := string(data)
	for _, banned := range []string{
		"target_path",
		"target_url",
		"args",
		"approval_ref",
		"tool_payload",
		"secret-token",
		"/home/alice/.ssh/id_rsa",
		"evil.example",
	} {
		if strings.Contains(body, banned) {
			t.Fatalf("audit event leaked banned value %q in %s", banned, body)
		}
	}
}
