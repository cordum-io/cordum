package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// fakePolicyDispatcher implements PolicyDispatcher for tests. Returns a
// canned decision and records the inputs the EvaluateToolCall path passed
// it so cases can assert dispatch happened with the right descriptor.
//
// PolicyDispatcher is a narrow mcp-local interface that the gateway
// implements over actiongates.Pipeline; this lets core/mcp tests stay
// free of the import cycle that an actiongates-typed dispatcher would
// require.
type fakePolicyDispatcher struct {
	decision PolicyDecision
	fired    bool
	calls    []*config.PolicyInput
}

func (f *fakePolicyDispatcher) Dispatch(_ context.Context, in *config.PolicyInput) (PolicyDecision, bool) {
	f.calls = append(f.calls, in)
	return f.decision, f.fired
}

// fakeEventEmitter records every emitted event. Tests assert on the
// sequence of EventKinds and the decision string carried on each one.
type fakeEventEmitter struct {
	events []*edge.AgentActionEvent
	err    error
}

func (f *fakeEventEmitter) Emit(_ context.Context, evt *edge.AgentActionEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, evt)
	return nil
}

// fakeArtifactStore records every Put. Tests assert that oversized inputs
// route to artifact storage instead of being inlined into events.
type fakeArtifactStore struct {
	puts []artifactPutCall
	err  error
}

type artifactPutCall struct {
	artifactType edge.ArtifactType
	bytes        int
	tenant       string
}

func (f *fakeArtifactStore) Put(_ context.Context, art ArtifactPutRequest) (*edge.ArtifactPointer, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.puts = append(f.puts, artifactPutCall{
		artifactType: art.Type,
		bytes:        len(art.Payload),
		tenant:       art.TenantID,
	})
	return &edge.ArtifactPointer{
		ArtifactType: art.Type,
		URI:          "memory://" + string(art.Type),
		SHA256:       "deadbeef",
	}, nil
}

func newToolCallDepsFixture(pipeline *fakePolicyDispatcher, emitter *fakeEventEmitter, store *fakeArtifactStore) ToolCallDeps {
	return ToolCallDeps{
		Pipeline:      pipeline,
		EventEmitter:  emitter,
		Redactor:      DefaultRedactor(),
		ArtifactStore: store,
		Clock:         func() time.Time { return time.Date(2026, 5, 15, 17, 0, 0, 0, time.UTC) },
	}
}

func newAuthedToolCallCtx() context.Context {
	return WithCallMetadata(context.Background(), CallMetadata{
		Tenant:      "tnt_a",
		Principal:   "p1",
		AgentID:     "agent_alpha",
		SessionID:   "sess_42",
		ExecutionID: "exec_99",
	})
}

// TestBuildActionDescriptorFromToolCall_PreservesScopedFields asserts the
// builder maps a tools/call request into the action descriptor shape the
// gate pipeline consumes. The build is server-side: callers never inject
// Kind/Verb/RiskTags — the descriptor's Verb stays zero (the mutation gate
// classifies via its destructive-verb set) and RiskTags stay empty (gates
// derive their own).
func TestBuildActionDescriptorFromToolCall_PreservesScopedFields(t *testing.T) {
	t.Parallel()
	meta := CallMetadata{
		Tenant:      "tnt_a",
		Principal:   "p1",
		SessionID:   "sess_42",
		ExecutionID: "exec_99",
		AgentID:     "agent_alpha",
	}
	args := json.RawMessage(`{"path":"/var/data/x.db","approval_claim":"approved by CFO"}`)
	desc, err := BuildActionDescriptorFromToolCall(meta, ToolCallParams{Name: "fs.write", Arguments: args}, "local-fs")
	if err != nil {
		t.Fatalf("BuildActionDescriptorFromToolCall returned err: %v", err)
	}
	if desc == nil {
		t.Fatal("descriptor is nil")
	}
	if desc.Kind != config.ActionKindMCPCall {
		t.Fatalf("Kind = %q, want %q", desc.Kind, config.ActionKindMCPCall)
	}
	if desc.Server != "local-fs" {
		t.Fatalf("Server = %q, want local-fs", desc.Server)
	}
	if desc.Tool != "fs.write" {
		t.Fatalf("Tool = %q, want fs.write", desc.Tool)
	}
	if desc.Verb != "" {
		t.Fatalf("Verb should be zero (gate classifies), got %q", desc.Verb)
	}
	if len(desc.RiskTags) != 0 {
		t.Fatalf("RiskTags should be empty (gates derive), got %v", desc.RiskTags)
	}
	if desc.ApprovalClaim == nil || desc.ApprovalClaim.ClaimText != "approved by CFO" {
		t.Fatalf("ApprovalClaim.ClaimText not copied verbatim; got %+v", desc.ApprovalClaim)
	}
}

// TestBuildActionDescriptor_ArgsTooLargeReturnsError asserts the builder
// rejects oversized argument blobs upfront. Silently truncating would let
// an attacker probe the gate with stripped args; failing closed forces
// the caller to handle the size violation explicitly.
func TestBuildActionDescriptor_ArgsTooLargeReturnsError(t *testing.T) {
	t.Parallel()
	huge := make([]byte, MaxToolCallArgsBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	args := json.RawMessage(`{"blob":"` + string(huge) + `"}`)
	meta := CallMetadata{Tenant: "tnt_a", Principal: "p1"}
	_, err := BuildActionDescriptorFromToolCall(meta, ToolCallParams{Name: "fs.write", Arguments: args}, "local-fs")
	if err == nil {
		t.Fatal("expected args_too_large error, got nil")
	}
	if !strings.Contains(err.Error(), "args_too_large") {
		t.Fatalf("expected args_too_large sentinel in error, got %q", err.Error())
	}
}

// TestEvaluateToolCall_AllowEmitsPreOnly asserts the happy path emits a
// single mcp.tool.pre event with decision=allow. The bridge layer is
// responsible for the matching post; EvaluateToolCall stops at pre.
func TestEvaluateToolCall_AllowEmitsPreOnly(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	result, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if result.Decision.Decision != pb.DecisionType_DECISION_TYPE_UNSPECIFIED {
		// Pipeline returned the zero decision (no gate fired) — treated as allow.
		t.Logf("decision: %v", result.Decision.Decision)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d: %+v", len(emitter.events), emitter.events)
	}
	pre := emitter.events[0]
	if pre.Kind != edge.EventKindMCPToolPre {
		t.Fatalf("kind = %q, want %q", pre.Kind, edge.EventKindMCPToolPre)
	}
	if pre.Decision != edge.DecisionAllow {
		t.Fatalf("decision = %q, want allow", pre.Decision)
	}
	if pre.ToolName != "fs.read_file" {
		t.Fatalf("tool_name = %q, want fs.read_file", pre.ToolName)
	}
}

// TestEvaluateToolCall_DenyEmitsFailedNotPre asserts that a gate-deny
// path emits an mcp.tool.failed event with decision=deny and a redacted
// reason. The upstream call MUST NOT be invoked.
func TestEvaluateToolCall_DenyEmitsFailedNotPre(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			GateID:    "actiongate.mcp",
			Code:      "access_denied",
			Reason:    "tool denylisted",
			SubReason: "tool_denylisted",
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	result, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.delete",
		Arguments: json.RawMessage(`{"path":"/var/data/x.db"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if result.Decision.Decision != pb.DecisionType_DECISION_TYPE_DENY {
		t.Fatalf("decision = %v, want DENY", result.Decision.Decision)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("expected exactly 1 event (failed), got %d", len(emitter.events))
	}
	failed := emitter.events[0]
	if failed.Kind != edge.EventKindMCPToolFailed {
		t.Fatalf("kind = %q, want %q", failed.Kind, edge.EventKindMCPToolFailed)
	}
	if failed.Decision != edge.DecisionDeny {
		t.Fatalf("decision = %q, want deny", failed.Decision)
	}
	if failed.DecisionReason == "" {
		t.Fatal("decision_reason should be populated with sanitized gate reason")
	}
}

func TestEvaluateToolCall_TaintLookupFailureStampsDescriptorFlag(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.TaintStore = &failingTaintStore{}

	_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "delete_item",
		Arguments: json.RawMessage(`{"item_id":123}`),
	}, "monday")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if len(pipeline.calls) != 1 {
		t.Fatalf("expected one policy dispatch, got %d", len(pipeline.calls))
	}
	desc := pipeline.calls[0].Action
	if desc == nil {
		t.Fatal("policy dispatch missing action descriptor")
	}
	if !desc.TaintLookupFailed {
		t.Fatal("TaintLookupFailed = false, want true when GetTaint errors")
	}
	if len(desc.RiskTags) != 0 {
		t.Fatalf("RiskTags = %v, want no prompt-injection tag on lookup error", desc.RiskTags)
	}
	if desc.SessionTaint != nil {
		t.Fatalf("SessionTaint = %+v, want nil because lookup error has no citation", desc.SessionTaint)
	}
}

// TestEvaluateToolCall_LargePayloadUsesArtifactPointer asserts the
// redaction-then-artifact-pointer hand-off. When redacted args exceed
// MaxInputRedactedBytes, the event carries a truncated summary plus an
// ArtifactPointer to the full redacted blob in the artifact store.
func TestEvaluateToolCall_LargePayloadUsesArtifactPointer(t *testing.T) {
	t.Parallel()
	bigField := strings.Repeat("a", 80*1024)
	args := json.RawMessage(`{"payload":"` + bigField + `"}`)
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	store := &fakeArtifactStore{}
	deps := newToolCallDepsFixture(pipeline, emitter, store)
	_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: args,
	}, "local-fs")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if len(store.puts) == 0 {
		t.Fatal("expected artifact store Put for oversized payload, got none")
	}
	if store.puts[0].artifactType != edge.ArtifactTypeMCPRequest {
		t.Fatalf("artifact type = %q, want %q", store.puts[0].artifactType, edge.ArtifactTypeMCPRequest)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(emitter.events))
	}
	pre := emitter.events[0]
	if len(pre.ArtifactPointers) == 0 {
		t.Fatal("event missing artifact pointer for oversized payload")
	}
	inlineBytes, _ := json.Marshal(pre.InputRedacted)
	if len(inlineBytes) > edge.MaxInputRedactedBytes {
		t.Fatalf("InputRedacted inline = %d bytes, want <= %d (must use artifact pointer)",
			len(inlineBytes), edge.MaxInputRedactedBytes)
	}
}

// TestEvaluateToolCall_TenantIsolation asserts the descriptor's
// TargetResource.OwnerTenant carries cross-tenant intent into the gate;
// a tenant gate denial propagates as an mcp.tool.failed event.
func TestEvaluateToolCall_TenantIsolation(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			GateID:    "actiongate.tenant",
			Code:      "access_denied",
			Reason:    "cross-tenant target",
			SubReason: "cross_tenant",
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "db.update",
		Arguments: json.RawMessage(`{"owner_tenant":"tnt_b","id":"42"}`),
	}, "remote-pg")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if len(emitter.events) != 1 || emitter.events[0].Kind != edge.EventKindMCPToolFailed {
		t.Fatalf("expected mcp.tool.failed event for tenant isolation deny, got %+v", emitter.events)
	}
	if emitter.events[0].Decision != edge.DecisionDeny {
		t.Fatalf("decision = %q, want deny", emitter.events[0].Decision)
	}
}

// TestEvaluateToolCall_MissingMetadataFailsClosed asserts the request is
// rejected when the calling middleware did not stash CallMetadata in
// context. No event is emitted (the caller has no tenant attribution, so
// recording the failure would create an unattributed audit row).
func TestEvaluateToolCall_MissingMetadataFailsClosed(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	_, err := EvaluateToolCall(context.Background(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "local-fs")
	if err == nil {
		t.Fatal("expected missing_mcp_metadata error, got nil")
	}
	if !strings.Contains(err.Error(), "missing_mcp_metadata") {
		t.Fatalf("expected missing_mcp_metadata sentinel, got %q", err.Error())
	}
	if len(emitter.events) != 0 {
		t.Fatalf("expected zero events on missing metadata, got %d", len(emitter.events))
	}
	if len(pipeline.calls) != 0 {
		t.Fatalf("pipeline should not be dispatched without metadata, got %d calls", len(pipeline.calls))
	}
}

// stubRedactor is a deliberately-broken redactor that returns the input
// unchanged. Used by TestEvaluateToolCall_DefenseInDepthRefusesPartialRedaction
// to simulate a misconfigured or hostile redactor and prove the post-redact
// completeness check fails closed.
type stubRedactor struct{}

func (stubRedactor) Redact(args json.RawMessage) json.RawMessage { return args }

// countingRedactor records every Redact call so tests can assert that
// oversized payloads short-circuit BEFORE the redactor runs. Returns
// the input unchanged otherwise (callers MUST NOT use it to test
// redaction itself — pair with DefaultRedactor for that).
type countingRedactor struct{ calls int }

func (c *countingRedactor) Redact(args json.RawMessage) json.RawMessage {
	c.calls++
	return args
}

// TestEvaluateToolCall_OversizedArgsRejectedBeforeRedactor asserts the
// hostile-large-payload guard: a 10 MB argument blob is rejected with
// args_too_large WITHOUT the redactor running. The cap is the cheap
// byte-length check; a regex walk over 10 MB would burn CPU on a
// payload whose only outcome is rejection. Defense against a DoS-shape
// abuse of the tool-call path.
func TestEvaluateToolCall_OversizedArgsRejectedBeforeRedactor(t *testing.T) {
	t.Parallel()
	huge := make([]byte, MaxToolCallArgsBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	args := json.RawMessage(`{"blob":"` + string(huge) + `"}`)
	redactor := &countingRedactor{}
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Redactor = redactor
	_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: args,
	}, "local-fs")
	if err == nil {
		t.Fatal("expected args_too_large error, got nil")
	}
	if !strings.Contains(err.Error(), "args_too_large") {
		t.Fatalf("expected args_too_large sentinel, got %q", err.Error())
	}
	if redactor.calls != 0 {
		t.Fatalf("redactor invoked %d times on oversized payload; want 0 (size check must be first)", redactor.calls)
	}
	if len(emitter.events) != 0 {
		t.Fatalf("emitter saw %d events on oversized payload; want 0", len(emitter.events))
	}
}

// TestEvaluateToolCall_DefenseInDepthRefusesPartialRedaction asserts that
// when the Redactor lets a known sensitive substring slip through (bug or
// rules-misconfig), EvaluateToolCall returns redaction_failed and emits
// zero events. The contract: no raw credential ever lands in a Redis event,
// even if an upstream rule set was incomplete. The pre-emit completeness
// check is the defense-in-depth backstop that catches such a slip.
func TestEvaluateToolCall_DefenseInDepthRefusesPartialRedaction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
	}{
		{"anthropic_sk_leak", `{"cmd":"export KEY=` + "sk-" + "ant" + "0123456789abcdef0123456789abcdef" + `"}`},
		{"github_pat_leak", `{"cmd":"login with ` + "ghp_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
		{"aws_key_leak", `{"cmd":"AWS_ACCESS_KEY_ID=` + "AKI" + "A" + "ABCDEFGHIJKLMNOP" + `"}`},
		{"jwt_leak", `{"cmd":"Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.abc"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipeline := &fakePolicyDispatcher{}
			emitter := &fakeEventEmitter{}
			deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
			deps.Redactor = stubRedactor{}
			_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
				Name:      "bash.exec",
				Arguments: json.RawMessage(tc.payload),
			}, "local-shell")
			if err == nil {
				t.Fatal("expected redaction_failed error from completeness check, got nil")
			}
			if !strings.Contains(err.Error(), "redaction_failed") {
				t.Fatalf("expected redaction_failed sentinel, got %q", err.Error())
			}
			if len(emitter.events) != 0 {
				t.Fatalf("must emit zero events when redaction completeness check fails; got %d", len(emitter.events))
			}
		})
	}
}

func TestEvaluateToolCall_DefenseInDepthRefusesPEMPrivateKeyFamilies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		labelPrefix string
	}{
		{name: "rsa", labelPrefix: "RSA "},
		{name: "pkcs8_bare", labelPrefix: ""},
		{name: "ec", labelPrefix: "EC "},
		{name: "openssh", labelPrefix: "OPENSSH "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipeline := &fakePolicyDispatcher{}
			emitter := &fakeEventEmitter{}
			deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
			deps.Redactor = stubRedactor{}
			_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
				Name: "bash.exec",
				Arguments: json.RawMessage(`{"cmd":` +
					mustMarshalStringForTest(syntheticPrivateKeyPEMForRedactionTest(tc.labelPrefix)) + `}`),
			}, "local-shell")
			if err == nil {
				t.Fatal("expected redaction_failed error from PEM completeness check, got nil")
			}
			if !strings.Contains(err.Error(), "redaction_failed") {
				t.Fatalf("expected redaction_failed sentinel, got %q", err.Error())
			}
			if len(emitter.events) != 0 {
				t.Fatalf("must emit zero events when PEM completeness check fails; got %d", len(emitter.events))
			}
		})
	}
}

func mustMarshalStringForTest(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestBuildActionDescriptor_NormalizesTargetPathFromArgs asserts that a
// Windows backslash path supplied in arg["path"] surfaces on
// descriptor.TargetPath as a normalized forward-slash form. Without this,
// the same logical path produces different canonical hashes on different
// platforms and the approval-lifecycle key splits.
func TestBuildActionDescriptor_NormalizesTargetPathFromArgs(t *testing.T) {
	t.Parallel()
	meta := CallMetadata{Tenant: "tnt_a"}
	args := json.RawMessage(`{"path":"C:\\Users\\alice\\data\\x.db"}`)
	desc, err := BuildActionDescriptorFromToolCall(meta, ToolCallParams{Name: "fs.write", Arguments: args}, "local-fs")
	if err != nil {
		t.Fatalf("build err: %v", err)
	}
	if desc.TargetPath != "C:/Users/alice/data/x.db" {
		t.Fatalf("TargetPath = %q, want forward-slash normalized %q", desc.TargetPath, "C:/Users/alice/data/x.db")
	}
}

// TestBuildActionDescriptor_PathBackslashAndForwardSlashSameHash asserts
// that two arg payloads identical except for backslash vs forward-slash in
// the path field produce the SAME canonical action hash. The hash binds
// the approval lifecycle to the same key regardless of how the caller
// spelled the path; without normalization a Windows caller and a POSIX
// caller hitting the same file would race to create two pending approvals.
func TestBuildActionDescriptor_PathBackslashAndForwardSlashSameHash(t *testing.T) {
	t.Parallel()
	meta := CallMetadata{Tenant: "tnt_a"}
	backslash := json.RawMessage(`{"path":"C:\\Users\\alice\\data\\x.db"}`)
	forward := json.RawMessage(`{"path":"C:/Users/alice/data/x.db"}`)
	descB, err := BuildActionDescriptorFromToolCall(meta, ToolCallParams{Name: "fs.write", Arguments: backslash}, "local-fs")
	if err != nil {
		t.Fatalf("backslash build err: %v", err)
	}
	descF, err := BuildActionDescriptorFromToolCall(meta, ToolCallParams{Name: "fs.write", Arguments: forward}, "local-fs")
	if err != nil {
		t.Fatalf("forward build err: %v", err)
	}
	if descB.TargetPath != descF.TargetPath {
		t.Fatalf("TargetPath divergence: %q vs %q", descB.TargetPath, descF.TargetPath)
	}
	hashB := ActionTupleHash("tnt_a", "local-fs", "fs.write", descB.TargetPath)
	hashF := ActionTupleHash("tnt_a", "local-fs", "fs.write", descF.TargetPath)
	if hashB != hashF {
		t.Fatalf("canonical hash diverged for equivalent paths: %s vs %s", hashB, hashF)
	}
}

// TestBuildActionDescriptor_ArgsLengthByByteNotRune asserts the
// MaxToolCallArgsBytes cap is enforced on JSON byte length, not rune
// count. UTF-8 multibyte characters in a tool arg could otherwise smuggle
// past the cap by reporting fewer runes than bytes — e.g. an emoji-heavy
// blob with 600K runes but 2.4 MB of bytes must still be rejected.
func TestBuildActionDescriptor_ArgsLengthByByteNotRune(t *testing.T) {
	t.Parallel()
	// Each "ñ" is 2 bytes in UTF-8; build a string whose bytes exceed the cap
	// while runes do not. (MaxToolCallArgsBytes / 2) + 1024 runes -> ~1MB+2KB
	// bytes once wrapped in JSON.
	runesCount := (MaxToolCallArgsBytes / 2) + 1024
	bigUTF8 := strings.Repeat("ñ", runesCount)
	args := json.RawMessage(`{"blob":"` + bigUTF8 + `"}`)
	if len(args) <= MaxToolCallArgsBytes {
		t.Fatalf("test setup wrong: args byte length %d not over cap %d", len(args), MaxToolCallArgsBytes)
	}
	meta := CallMetadata{Tenant: "tnt_a"}
	_, err := BuildActionDescriptorFromToolCall(meta, ToolCallParams{Name: "fs.write", Arguments: args}, "local-fs")
	if err == nil {
		t.Fatal("expected args_too_large error for byte-length violation, got nil")
	}
	if !strings.Contains(err.Error(), "args_too_large") {
		t.Fatalf("expected args_too_large sentinel, got %q", err.Error())
	}
}

// TestEvaluateToolCall_HighSeverityFindingTriggersArtifact asserts the
// second artifact-pointer trigger: even when the redacted payload is well
// under MaxInputRedactedBytes, the presence of a high-severity finding
// (api_key, token, secret, password, private_key, etc.) routes the event
// to artifact storage. The artifact carries the redacted payload (raw
// credentials never leave the redactor); persisting it gives investigators
// the full context for an incident review without inflating the inline
// event.
func TestEvaluateToolCall_HighSeverityFindingTriggersArtifact(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	store := &fakeArtifactStore{}
	deps := newToolCallDepsFixture(pipeline, emitter, store)
	// Small payload (well under 64 KiB) but the api_key field name triggers
	// a high-severity field-name redaction inside the default rules.
	args := json.RawMessage(`{"cmd":"connect","api_key":"abc-not-leaked-because-field-redacts"}`)
	_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "http.post",
		Arguments: args,
	}, "remote-http")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if len(store.puts) == 0 {
		t.Fatal("expected artifact store Put because high-severity finding present, got none")
	}
	if store.puts[0].artifactType != edge.ArtifactTypeMCPRequest {
		t.Fatalf("artifact type = %q, want %q", store.puts[0].artifactType, edge.ArtifactTypeMCPRequest)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(emitter.events))
	}
	if len(emitter.events[0].ArtifactPointers) == 0 {
		t.Fatal("event missing artifact pointer despite high-severity finding")
	}
}

// TestEvaluateToolCall_AllowWithConstraintsPreCarriesConstraints asserts
// that a gate returning ALLOW_WITH_CONSTRAINTS with a structured
// constraint map propagates the map through to the emitted mcp.tool.pre
// event AND that event.Decision is `constrain` (NOT `allow`). Without
// this propagation, downstream audit consumers can't distinguish an
// AWC-bounded allow from an unconstrained allow and the constraint
// metadata is silently dropped.
func TestEvaluateToolCall_AllowWithConstraintsPreCarriesConstraints(t *testing.T) {
	t.Parallel()
	constraints := map[string]any{
		"max_bytes":    float64(1024),
		"allowed_tags": []any{"safe", "preview"},
		"redaction":    "strict",
	}
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:    pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
			GateID:      "actiongate.mcp",
			Code:        "constrained",
			Reason:      "tier ceiling applied",
			Constraints: constraints,
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	result, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "local-fs")
	if err != nil {
		t.Fatalf("EvaluateToolCall returned err: %v", err)
	}
	if result.Decision.Decision != pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS {
		t.Fatalf("result.Decision = %v, want ALLOW_WITH_CONSTRAINTS", result.Decision.Decision)
	}
	if !reflect.DeepEqual(result.Decision.Constraints, constraints) {
		t.Fatalf("result.Decision.Constraints = %#v, want %#v (adapter must propagate AWC constraint map)", result.Decision.Constraints, constraints)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("expected exactly 1 pre event, got %d", len(emitter.events))
	}
	pre := emitter.events[0]
	if pre.Kind != edge.EventKindMCPToolPre {
		t.Fatalf("pre.Kind = %q, want %q", pre.Kind, edge.EventKindMCPToolPre)
	}
	if pre.Decision != edge.DecisionConstrain {
		t.Fatalf("pre.Decision = %q, want %q (AWC must NOT degrade to allow)", pre.Decision, edge.DecisionConstrain)
	}
	if !reflect.DeepEqual(pre.Constraints, constraints) {
		t.Fatalf("pre.Constraints = %#v, want %#v (event must carry the same constraint map the gate emitted)", pre.Constraints, constraints)
	}
}

// TestLogToolCallDecision_OmitsConstraintValues locks the security
// contract that operator-facing slog lines record constraint COUNTS
// (so deny/AWC spikes are greppable) but never the constraint VALUES
// themselves. Constraint values may carry sensitive policy detail
// (redaction levels, allowed hosts) that belong in the artifact-bound
// event, not the live log stream. Adversarial-self-review item (e).
func TestLogToolCallDecision_OmitsConstraintValues(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	event := &edge.AgentActionEvent{
		EventID:     "evt_42",
		TenantID:    "tnt_a",
		PrincipalID: "p1",
		SessionID:   "sess_1",
		ExecutionID: "exec_1",
		Decision:    edge.DecisionConstrain,
	}
	desc := &config.ActionDescriptor{Server: "local-fs", Tool: "fs.read_file"}
	dec := PolicyDecision{
		Decision: pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
		GateID:   "actiongate.mcp",
		Code:     "constrained",
		Reason:   "tier ceiling applied",
		Constraints: map[string]any{
			"redaction_level":   "extra_secret_redaction_value",
			"allowed_hostnames": []any{"super-secret-host.internal"},
		},
	}
	logToolCallDecision(context.Background(), event, desc, dec)

	out := buf.String()
	// VALUES must not appear at any log level.
	if strings.Contains(out, "extra_secret_redaction_value") {
		t.Fatalf("slog leaked constraint VALUE 'extra_secret_redaction_value': %s", out)
	}
	if strings.Contains(out, "super-secret-host.internal") {
		t.Fatalf("slog leaked constraint VALUE 'super-secret-host.internal': %s", out)
	}
	// COUNT must appear so operators can grep AWC bursts.
	if !strings.Contains(out, "constraint_count=2") {
		t.Fatalf("slog missing constraint_count=2 (operator must see AWC volume): %s", out)
	}
}

// TestEvaluateToolCall_ArtifactStoreFailureFailsClosed asserts that when
// the artifact store cannot accept an oversized payload, the call fails
// closed and no event is emitted. Allowing a silent inline-truncation
// fallback would defeat the contract that no raw redacted payload over
// MaxInputRedactedBytes is inlined into Redis events.
func TestEvaluateToolCall_ArtifactStoreFailureFailsClosed(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{}
	emitter := &fakeEventEmitter{}
	store := &fakeArtifactStore{err: errors.New("simulated artifact-store outage")}
	deps := newToolCallDepsFixture(pipeline, emitter, store)
	bigField := strings.Repeat("a", 80*1024)
	_, err := EvaluateToolCall(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"payload":"` + bigField + `"}`),
	}, "local-fs")
	if err == nil {
		t.Fatal("expected artifact store outage to propagate, got nil")
	}
	if len(emitter.events) != 0 {
		t.Fatalf("expected zero events when artifact store fails, got %d", len(emitter.events))
	}
}

func TestPolicy_AKIAPatternAlignment(t *testing.T) {
	redactor := awsUpstreamErrorPattern(t)
	completeness := awsCompletenessPattern(t)

	canonical := "AKIA" + "ABCDEFGHIJKLMNOP"
	truncated := "AKIA" + "ABCDEFGHIJKLM"
	for _, token := range []string{canonical, truncated} {
		if !redactor.MatchString(token) {
			t.Fatalf("upstream redactor AKIA pattern did not catch %q (%s)", token, redactor)
		}
		if !completeness.MatchString(token) {
			t.Fatalf("completeness AKIA pattern did not catch %q (%s)", token, completeness)
		}
	}
}

func awsUpstreamErrorPattern(t *testing.T) *regexp.Regexp {
	t.Helper()
	for _, rule := range upstreamErrorRedactPatterns {
		if rule.replacement == "[REDACTED:aws_key]" {
			return rule.pattern
		}
	}
	t.Fatal("upstream AKIA redaction pattern not found")
	return nil
}

func awsCompletenessPattern(t *testing.T) *regexp.Regexp {
	t.Helper()
	for _, pattern := range redactionCompletenessPatterns {
		if strings.Contains(pattern.String(), "AKIA") {
			return pattern
		}
	}
	t.Fatal("completeness AKIA pattern not found")
	return nil
}

// TestPolicyEvaluate_BlankLinkage_FailsClosed is the PR #276 Sub-E
// finding #26 regression: EvaluateToolCall MUST refuse the call when ANY
// of the (Tenant, SessionID, ExecutionID, AgentID) identity fields is
// blank. The audit row is keyed on that tuple; a single missing component
// produces unattributed events and silently breaks tenant-scoped audit
// filters. Fail-closed means: return errMissingMCPMetadata, emit zero
// events, never reach the gate pipeline.
func TestPolicyEvaluate_BlankLinkage_FailsClosed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		meta CallMetadata
	}{
		{"blank_tenant", CallMetadata{Tenant: "", Principal: "p1", AgentID: "a1", SessionID: "s1", ExecutionID: "e1"}},
		{"blank_session_id", CallMetadata{Tenant: "tnt_a", Principal: "p1", AgentID: "a1", SessionID: "", ExecutionID: "e1"}},
		{"blank_execution_id", CallMetadata{Tenant: "tnt_a", Principal: "p1", AgentID: "a1", SessionID: "s1", ExecutionID: ""}},
		{"blank_agent_id", CallMetadata{Tenant: "tnt_a", Principal: "p1", AgentID: "", SessionID: "s1", ExecutionID: "e1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pipeline := &fakePolicyDispatcher{}
			emitter := &fakeEventEmitter{}
			deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
			ctx := WithCallMetadata(context.Background(), tc.meta)
			_, err := EvaluateToolCall(ctx, deps, ToolCallParams{
				Name:      "fs.read_file",
				Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
			}, "local-fs")
			if err == nil {
				t.Fatal("expected fail-closed error on blank linkage; got nil")
			}
			if !errors.Is(err, errMissingMCPMetadata) {
				t.Fatalf("expected errMissingMCPMetadata, got %v", err)
			}
			if len(emitter.events) != 0 {
				t.Fatalf("blank-linkage path emitted %d events; want 0 (unattributed events forbidden)", len(emitter.events))
			}
			if len(pipeline.calls) != 0 {
				t.Fatalf("blank-linkage path dispatched %d gate calls; want 0 (must not reach gate)", len(pipeline.calls))
			}
		})
	}
}

// TestInvokeToolWithPolicy_ApprovalHandoffUsesCaller is the PR #276 Sub-E
// finding #27 regression: when REQUIRE_HUMAN routes through ApprovalHandoff,
// the ToolCallApprovalContext MUST carry caller identity sourced from
// CallMetadata (AgentID + Tenant), NOT the MCP server identity. Keying
// the approval handoff on server name would let two distinct agents share
// an approval slot (cross-agent approval bypass).
func TestInvokeToolWithPolicy_ApprovalHandoffUsesCaller(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			GateID:    "actiongate.mutation",
			Code:      "require_human",
			Reason:    "needs human approval",
			SubReason: "require_human",
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{}
	handoff := &capturingApprovalHandoff{ref: "appr_pending_42"}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	deps.ApprovalHandoff = handoff
	const server = "cordum.builtin"
	_, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.delete",
		Arguments: json.RawMessage(`{"path":"/var/data/x.db"}`),
	}, server)
	if err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if handoff.calls != 1 {
		t.Fatalf("approval handoff invoked %d times; want 1", handoff.calls)
	}
	captured := handoff.lastCtx
	// Caller identity: AgentID from CallMetadata, tenant from CallMetadata.
	if captured.AgentID != "agent_alpha" {
		t.Errorf("ToolCallApprovalContext.AgentID = %q; want %q (CallMetadata.AgentID — caller identity)",
			captured.AgentID, "agent_alpha")
	}
	if captured.Tenant != "tnt_a" {
		t.Errorf("ToolCallApprovalContext.Tenant = %q; want %q (CallMetadata.Tenant)",
			captured.Tenant, "tnt_a")
	}
	// Server: the MCP server identity, NOT the caller identity.
	if captured.Server != server {
		t.Errorf("ToolCallApprovalContext.Server = %q; want %q", captured.Server, server)
	}
	// Cross-check: the caller identity field MUST NOT be the server name
	// (otherwise the gateway adapter could mis-key the approval handoff
	// on server identity and let two agents share an approval slot).
	if captured.AgentID == captured.Server {
		t.Errorf("ToolCallApprovalContext.AgentID equals Server (%q) — caller identity must not collapse onto server identity",
			captured.AgentID)
	}
	// Tool: forwarded from the call.
	if captured.Tool != "fs.delete" {
		t.Errorf("ToolCallApprovalContext.Tool = %q; want fs.delete", captured.Tool)
	}
	// ApprovalHandoff is required: zero handoff means we never minted —
	// finding #27 includes "Should be required". Locked by the fakePolicyHandoff=nil
	// branch returning a clear error; covered separately in
	// TestInvokeToolWithPolicy_RequireApproval_RequiresHandoff below.
}

// TestInvokeToolWithPolicy_RequireApproval_RequiresHandoff locks the
// other half of finding #27: REQUIRE_HUMAN with no ApprovalHandoff wired
// MUST return an error rather than silently bypassing the approval step.
func TestInvokeToolWithPolicy_RequireApproval_RequiresHandoff(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{
			Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			GateID:   "actiongate.mutation",
			Code:     "require_human",
		},
		fired: true,
	}
	emitter := &fakeEventEmitter{}
	upstream := &fakeUpstreamToolCaller{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.Upstream = upstream
	// ApprovalHandoff deliberately nil.
	_, err := InvokeToolWithPolicy(newAuthedToolCallCtx(), deps, ToolCallParams{
		Name:      "fs.delete",
		Arguments: json.RawMessage(`{"path":"/x"}`),
	}, "local-fs")
	if err == nil {
		t.Fatal("expected error when ApprovalHandoff is nil on REQUIRE_HUMAN; got nil (would silently bypass approval)")
	}
	if !strings.Contains(err.Error(), "ApprovalHandoff") {
		t.Errorf("error %q should mention ApprovalHandoff", err)
	}
	if upstream.calls != 0 {
		t.Fatalf("upstream invoked %d times; want 0 (no handoff means no upstream)", upstream.calls)
	}
}

// TestSanitizeUpstreamError_GitHubTokenFamilies is the PR #276 Sub-E
// finding #25 regression: the failed-event upstream-error sanitizer
// MUST redact every GitHub token family before the error message lands
// in a Redis-persisted audit row. Coverage:
//   - classic PAT (ghp_)
//   - OAuth user-server (gho_)
//   - user-server (ghu_)
//   - server-server (ghs_)
//   - refresh (ghr_)
//   - fine-grained PAT (github_pat_)
//   - Enterprise (ghe_)
//
// Without this, an upstream that returns an error embedding the token
// (common for failed git/github.com requests) leaks the raw credential
// into the failed-event ErrorMessage column.
func TestSanitizeUpstreamError_GitHubTokenFamilies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		raw       string
		sensitive string
	}{
		// Fixtures assembled from fragments so GitHub secret-scanning push
		// protection does not flag the source as a leaked token.
		{"classic_pat", "auth failed with " + "ghp_" + "0123456789abcdef0123456789abcdef0123", "ghp_" + "0123456789abcdef"},
		{"oauth", "auth failed with " + "gho_" + "0123456789abcdef0123456789abcdef0123", "gho_" + "0123456789abcdef"},
		{"user_server", "auth failed with " + "ghu_" + "0123456789abcdef0123456789abcdef0123", "ghu_" + "0123456789abcdef"},
		{"server_server", "auth failed with " + "ghs_" + "0123456789abcdef0123456789abcdef0123", "ghs_" + "0123456789abcdef"},
		{"refresh", "auth failed with " + "ghr_" + "0123456789abcdef0123456789abcdef0123", "ghr_" + "0123456789abcdef"},
		{"fine_grained_pat", "auth failed with " + "github_pat_" + "11A0123456789_0123456789abcdef0123456789abcdef0123456789abcdef0123", "github_pat_" + "11A"},
		{"enterprise", "auth failed with " + "ghe_" + "0123456789abcdef0123456789abcdef0123", "ghe_" + "0123456789abcdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := sanitizeUpstreamError(errors.New(tc.raw))
			if strings.Contains(out, tc.sensitive) {
				t.Errorf("sanitized output leaks %s token: %q", tc.name, out)
			}
			if !strings.Contains(out, "[REDACTED:github_token]") {
				t.Errorf("sanitized output missing [REDACTED:github_token] marker for %s: %q", tc.name, out)
			}
		})
	}
}

// TestVerifyRedactionCompleteness_GitHubTokenFamilies locks the
// defense-in-depth backstop for finding #25: even if a custom redactor
// were misconfigured and let a GitHub token survive its pass,
// verifyRedactionCompleteness MUST trip and refuse to emit the event.
// The completeness check is the last line of defense before persistence.
func TestVerifyRedactionCompleteness_GitHubTokenFamilies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"classic_pat", `{"note":"` + "ghp_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
		{"oauth", `{"note":"` + "gho_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
		{"user_server", `{"note":"` + "ghu_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
		{"server_server", `{"note":"` + "ghs_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
		{"refresh", `{"note":"` + "ghr_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
		{"fine_grained_pat", `{"note":"` + "github_pat_" + "11A0123456789_0123456789abcdef0123456789abcdef0123456789abcdef0123" + `"}`},
		{"enterprise", `{"note":"` + "ghe_" + "0123456789abcdef0123456789abcdef0123" + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyRedactionCompleteness([]byte(tc.raw))
			if err == nil {
				t.Fatalf("verifyRedactionCompleteness allowed %s token to survive: %s", tc.name, tc.raw)
			}
			if !strings.Contains(err.Error(), "redaction_failed") {
				t.Errorf("expected redaction_failed sentinel in error, got %q", err)
			}
		})
	}
}

// capturingApprovalHandoff is a fakeApprovalHandoff that also records
// the ToolCallApprovalContext it received so tests can assert the
// caller identity routing contract (finding #27).
type capturingApprovalHandoff struct {
	calls   int
	ref     string
	err     error
	lastCtx ToolCallApprovalContext
}

func (f *capturingApprovalHandoff) ConsumeActionGateDecision(_ context.Context, _ PolicyDecision, ctxData ToolCallApprovalContext) (string, error) {
	f.calls++
	f.lastCtx = ctxData
	return f.ref, f.err
}
