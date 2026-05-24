package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

func lastSIEMEvent(t *testing.T, sink *recordingAuditSender) audit.SIEMEvent {
	t.Helper()
	if len(sink.events) == 0 {
		t.Fatalf("expected at least one emitted SIEM event, got none")
	}
	return sink.events[len(sink.events)-1]
}

// TestAppendSubmitSafetyDecisionAudit_AttachesKeyIdentity verifies the submit
// governance event (shared by the HTTP and gRPC submit paths) carries the
// resolved key identity plus identity_source/identity_label in Extra.
func TestAppendSubmitSafetyDecisionAudit_AttachesKeyIdentity(t *testing.T) {
	s, _, _ := newTestGateway(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	s.appendSubmitSafetyDecisionAudit(context.Background(), "submit", "job-1", "topic.demo",
		"mk_x", "operator", "api_key:mk_x", "ci", "submit job job-1",
		submitPolicyDecision{}, nil, "", "", "")

	ev := lastSIEMEvent(t, sink)
	if ev.Identity != "mk_x" {
		t.Fatalf("Identity = %q, want mk_x", ev.Identity)
	}
	if got := ev.Extra["identity_source"]; got != "api_key:mk_x" {
		t.Fatalf("Extra[identity_source] = %q, want api_key:mk_x", got)
	}
	if got := ev.Extra["identity_label"]; got != "ci" {
		t.Fatalf("Extra[identity_label] = %q, want ci", got)
	}
}

// TestAppendApprovalDecisionAudit_AttachesKeyIdentity verifies the approval
// governance event carries identity + identity_source/identity_label.
func TestAppendApprovalDecisionAudit_AttachesKeyIdentity(t *testing.T) {
	s, _, _ := newTestGateway(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	s.appendApprovalDecisionAudit(context.Background(), "approve", "job-1", "topic.demo",
		"mk_x", "operator", "api_key:mk_x", "ci", "approved", "looks good", "", "", "")

	ev := lastSIEMEvent(t, sink)
	if ev.Identity != "mk_x" {
		t.Fatalf("Identity = %q, want mk_x", ev.Identity)
	}
	if got := ev.Extra["identity_source"]; got != "api_key:mk_x" {
		t.Fatalf("Extra[identity_source] = %q, want api_key:mk_x", got)
	}
	if got := ev.Extra["identity_label"]; got != "ci" {
		t.Fatalf("Extra[identity_label] = %q, want ci", got)
	}
}

// TestPolicyActorID_KeyOnlyRequest_ReturnsStableIdentity locks the intended
// SOC2 behavior change: a key-only actor (no bound principal) now resolves to a
// stable key identity through PolicyActorID, so approval handlers record the
// key id ("mk_x") instead of falling back to "system/unknown".
func TestPolicyActorID_KeyOnlyRequest_ReturnsStableIdentity(t *testing.T) {
	ac := &auth.AuthContext{KeyID: "mk_x", KeyName: "ci"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/job-1/approve", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, ac))

	if got := policybundles.PolicyActorID(req); got != "mk_x" {
		t.Fatalf("PolicyActorID(key-only request) = %q, want mk_x (not system/unknown)", got)
	}
}
