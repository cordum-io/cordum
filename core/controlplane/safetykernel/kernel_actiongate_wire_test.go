package safetykernel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestWireActionGatePipeline_BootSetsPipeline asserts that the
// production wiring function installs a non-nil pipeline on a freshly
// constructed server. This is the regression guard for the QA-flagged
// "gates are dead code at runtime" bug: a future refactor that drops
// the wireActionGatePipeline call site (or returns early before
// SetActionGatePipeline runs) MUST break this test.
func TestWireActionGatePipeline_BootSetsPipeline(t *testing.T) {
	t.Parallel()
	srv := &server{}
	if err := wireActionGatePipeline(srv, nil); err != nil {
		t.Fatalf("wireActionGatePipeline returned err: %v", err)
	}
	if srv.actionGatePipeline == nil {
		t.Fatal("server.actionGatePipeline still nil after boot wiring")
	}
	if got := len(srv.actionGatePipeline.Gates()); got == 0 {
		t.Fatalf("pipeline has no gates after boot wiring; len=%d", got)
	}
	if srv.actionExtractor == nil {
		t.Fatal("server.actionExtractor still nil after boot wiring")
	}
	if srv.actionGateAuditSink == nil {
		t.Fatal("server.actionGateAuditSink still nil after boot wiring")
	}
}

// TestWireActionGatePipeline_NilServerNoOp asserts the wiring function
// guards against a nil receiver and returns cleanly (so a misconfigured
// test fixture cannot mask the wiring bug by panicking on a nil pointer).
func TestWireActionGatePipeline_NilServerNoOp(t *testing.T) {
	t.Parallel()
	if err := wireActionGatePipeline(nil, nil); err != nil {
		t.Fatalf("expected nil-server wire to no-op, got err: %v", err)
	}
}

// TestActionDescriptorFromRequest_RoundTrip asserts the kernel extractor
// reads the gateway-encoded JSON label and reconstructs the original
// ActionDescriptor. The encoding contract (Labels key, JSON marshaling,
// size cap) is what allows the descriptor to traverse the gateway→kernel
// gRPC boundary without a proto schema change.
func TestActionDescriptorFromRequest_RoundTrip(t *testing.T) {
	t.Parallel()
	desc := &config.ActionDescriptor{
		Kind:       config.ActionKindMutation,
		Verb:       config.ActionVerbDelete,
		TargetPath: "/var/data/users.db",
		TargetResource: &config.ActionTargetResource{
			Type:        "user",
			ID:          "user_42",
			OwnerTenant: "tnt_a",
		},
		ApprovalClaim: &config.ActionApprovalClaim{
			ApprovalRef: "appr_42",
		},
	}
	encoded, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	req := &pb.PolicyCheckRequest{
		Labels: map[string]string{LabelActionDescriptorJSON: string(encoded)},
	}
	got := actionDescriptorFromRequest(context.Background(), req)
	if got == nil {
		t.Fatal("extractor returned nil on well-formed payload")
	}
	if got.Kind != desc.Kind || got.Verb != desc.Verb || got.TargetPath != desc.TargetPath {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", got, desc)
	}
	if got.ApprovalClaim == nil || got.ApprovalClaim.ApprovalRef != "appr_42" {
		t.Fatalf("approval claim not preserved across encoding: %+v", got.ApprovalClaim)
	}
	if got.TargetResource == nil || got.TargetResource.OwnerTenant != "tnt_a" {
		t.Fatalf("target resource not preserved across encoding: %+v", got.TargetResource)
	}
}

func TestActionDescriptorFromRequest_AbsentLabelReturnsNil(t *testing.T) {
	t.Parallel()
	if got := actionDescriptorFromRequest(context.Background(), &pb.PolicyCheckRequest{}); got != nil {
		t.Fatalf("expected nil for missing label, got %+v", got)
	}
	if got := actionDescriptorFromRequest(context.Background(), nil); got != nil {
		t.Fatalf("expected nil for nil request, got %+v", got)
	}
}

func TestActionDescriptorFromRequest_MalformedLabelReturnsNil(t *testing.T) {
	t.Parallel()
	req := &pb.PolicyCheckRequest{
		Labels: map[string]string{LabelActionDescriptorJSON: "{not valid json"},
	}
	if got := actionDescriptorFromRequest(context.Background(), req); got != nil {
		t.Fatalf("malformed JSON should drop to nil to avoid bypass; got %+v", got)
	}
}

func TestActionDescriptorFromRequest_OversizeLabelReturnsNil(t *testing.T) {
	t.Parallel()
	huge := make([]byte, config.ActionArgsMaxSerializedBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	req := &pb.PolicyCheckRequest{
		Labels: map[string]string{LabelActionDescriptorJSON: string(huge)},
	}
	if got := actionDescriptorFromRequest(context.Background(), req); got != nil {
		t.Fatalf("oversize payload should drop to nil; got %+v", got)
	}
}

// checkThroughWiredGate wires the production action-gate pipeline with
// exporter on a server whose policy allows the topic, sends one
// mutation/delete request with no auth context, and requires an action
// gate's DENY.
func checkThroughWiredGate(t *testing.T, exporter audit.AuditSender, jobID string) {
	t.Helper()
	srv := &server{policy: &config.SafetyPolicy{
		DefaultTenant:   "default",
		DefaultDecision: "allow",
		Tenants:         map[string]config.TenantPolicy{"default": {AllowTopics: []string{"job.*"}}},
	}}
	if err := wireActionGatePipeline(srv, exporter); err != nil {
		t.Fatalf("wireActionGatePipeline: %v", err)
	}
	resp, err := srv.Check(context.Background(), actionDescriptorCacheRequest(t, jobID))
	if err != nil || resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY ||
		!strings.HasPrefix(resp.GetRuleId(), "actiongate.") {
		t.Fatalf("Check = %v rule %q err %v; want an action gate's DENY", resp.GetDecision(), resp.GetRuleId(), err)
	}
}

// captureWarnLogs routes slog WARN and above into the returned buffer for
// the rest of the test.
func captureWarnLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &logs
}

// TestActionGateAuditPublishesThroughKernelPublisher pins that a kernel
// gate denial reaches sys.audit.export as one actiongate.denied packet,
// signed as the safety kernel, through the shared audit publisher.
func TestActionGateAuditPublishesThroughKernelPublisher(t *testing.T) {
	blankAuditPublisherEnv(t)
	t.Setenv(audit.EnvPublisherDevSigningSeed, "task-ad7391d0-test-seed")
	rec := &recordingAuditBus{}
	sender, err := newShadowAuditSender(rec)
	checkThroughWiredGate(t, actionGateAuditSender(sender, err), "job-actiongate-export")
	packets := rec.published()
	if len(packets) != 1 {
		t.Fatalf("published packets = %d, want 1", len(packets))
	}
	alert := packets[0].GetAlert()
	if got := alert.GetDetails()["event_type"]; got != audit.EventActionGateDenied {
		t.Fatalf("event_type = %q, want %q", got, audit.EventActionGateDenied)
	}
	if !strings.Contains(alert.GetMessage(), `"job-actiongate-export"`) {
		t.Fatalf("message %q does not carry the job id", alert.GetMessage())
	}
	trust, err := audit.LoadPublisherTrustFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	pub := trust[servicetoken.IdentitySafetyKernel]
	lookup := func(string) (ed25519.PublicKey, bool) { return pub, pub != nil }
	claims, typ, err := servicetoken.Verify(packets[0].GetAuthToken(), lookup, time.Now())
	if err != nil || typ != servicetoken.TypService || claims.Subject != servicetoken.IdentitySafetyKernel {
		t.Fatalf("token subject %q typ %q err %v, want subject %q", claims.Subject, typ, err, servicetoken.IdentitySafetyKernel)
	}
}

// TestActionGateAuditFallsBackToLogOnly pins the fallback when no publisher
// can be built: one boot WARN naming the key variable, the gate still
// denies and is logged, and nothing is published.
func TestActionGateAuditFallsBackToLogOnly(t *testing.T) {
	cases := []struct {
		name  string
		build func(*recordingAuditBus) (audit.AuditSender, error)
	}{
		{"production without key", func(rec *recordingAuditBus) (audit.AuditSender, error) { return newShadowAuditSender(rec) }},
		{"no NATS", func(*recordingAuditBus) (audit.AuditSender, error) { return chooseShadowAuditSender(nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blankAuditPublisherEnv(t)
			t.Setenv(env.EnvMode, "production")
			logs := captureWarnLogs(t)
			rec := &recordingAuditBus{}
			exporter := actionGateAuditSender(tc.build(rec))
			if exporter != nil {
				t.Fatalf("actionGateAuditSender = %T, want nil", exporter)
			}
			checkThroughWiredGate(t, exporter, "job-actiongate-fallback")
			bootWarns := 0
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(line, "action-gate decisions are not exported") && strings.Contains(line, audit.EnvPublisherSigningKey) {
					bootWarns++
				}
			}
			if bootWarns != 1 || !strings.Contains(logs.String(), "action-gate fired with no audit exporter wired") {
				t.Fatalf("boot warnings = %d, want 1, and a per-fire warning; logs %q", bootWarns, logs.String())
			}
			if n := len(rec.published()); n != 0 {
				t.Fatalf("published packets = %d, want 0", n)
			}
		})
	}
}

// TestSetupShadowEvaluationKeepsSenderError pins shadow evaluation's boot
// rule: the shared sender's error refuses boot while shadow evaluation is
// on, and is ignored when it is disabled.
func TestSetupShadowEvaluationKeepsSenderError(t *testing.T) {
	senderErr := errors.New("task-ad7391d0 sender error")
	loader := fragmentLoaderForTest(t, map[string]any{})
	for _, tc := range []struct {
		disabled string
		wantErr  error
	}{{"", senderErr}, {"true", nil}} {
		t.Setenv(envShadowEvalDisabled, tc.disabled)
		shadowLoader, evaluator, err := setupShadowEvaluation(&server{}, loader, nil, senderErr)
		if evaluator != nil {
			evaluator.Close()
		}
		if shadowLoader != nil {
			shadowLoader.Close()
		}
		if !errors.Is(err, tc.wantErr) || evaluator != nil || shadowLoader != nil {
			t.Fatalf("disabled=%q: err %v evaluator %v loader %v; want err %v and nothing built",
				tc.disabled, err, evaluator != nil, shadowLoader != nil, tc.wantErr)
		}
	}
}
