package llmingest

import (
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

// awsExampleKey matches edge.awsKeyPattern (\bAKIA[0-9A-Z]{16}\b) and is the
// canonical AWS docs example key — safe to embed in a test.
const awsExampleKey = "AKIAIOSFODNN7EXAMPLE"

func baseEnvelope() LLMEventEnvelope {
	return LLMEventEnvelope{
		TenantID:      "tenant-a",
		SessionID:     "sess-1",
		ExecutionID:   "exec-1",
		SourceEventID: "evt-1",
		ObservedAt:    time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		Kind:          KindRequestPre,
		Provider:      "anthropic",
		Model:         "claude-opus-4-8",
		Direction:     DirectionPrompt,
		Content:       "summarize the repo",
	}
}

func mapOne(t *testing.T, env LLMEventEnvelope) (edgecore.AgentActionEvent, EventDecision) {
	t.Helper()
	res, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "copilot-proxy"}, Events: []LLMEventEnvelope{env}})
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if len(res.Events) != 1 || len(res.Decisions) != 1 {
		t.Fatalf("want 1 event+decision, got %d/%d", len(res.Events), len(res.Decisions))
	}
	return res.Events[0], res.Decisions[0]
}

func TestMap_RedactsSecretInContent(t *testing.T) {
	env := baseEnvelope()
	env.Content = "deploy with key " + awsExampleKey + " now"
	event, decision := mapOne(t, env)

	if decision.Decision != DecisionRedact {
		t.Fatalf("decision = %q, want %q", decision.Decision, DecisionRedact)
	}
	if !decision.Redacted {
		t.Fatal("decision.Redacted should be true")
	}
	if strings.Contains(decision.RedactedContent, awsExampleKey) {
		t.Fatalf("redacted content leaked the secret: %q", decision.RedactedContent)
	}
	if !containsString(decision.Findings, "aws_credential") {
		t.Fatalf("findings missing aws_credential: %v", decision.Findings)
	}
	// The persisted event must never carry the raw secret anywhere.
	if strings.Contains(eventInputString(t, event), awsExampleKey) {
		t.Fatalf("event input leaked the secret: %v", event.InputRedacted)
	}
	if event.Labels["llm.redacted"] != "true" {
		t.Fatalf("event missing llm.redacted label: %v", event.Labels)
	}
	if event.Labels["llm.finding.aws_credential"] != "true" {
		t.Fatalf("event missing finding label: %v", event.Labels)
	}
}

func TestMap_NoSecretRecords(t *testing.T) {
	_, decision := mapOne(t, baseEnvelope())
	if decision.Decision != DecisionRecord {
		t.Fatalf("decision = %q, want %q", decision.Decision, DecisionRecord)
	}
	if decision.Redacted {
		t.Fatal("clean content should not be marked redacted")
	}
	if len(decision.Findings) != 0 {
		t.Fatalf("clean content should have no findings: %v", decision.Findings)
	}
	if decision.RedactedContent != "summarize the repo" {
		t.Fatalf("clean content should pass through: %q", decision.RedactedContent)
	}
}

func TestMap_ClassifiesAsLLMRequest(t *testing.T) {
	env := baseEnvelope()
	env.Tokens = &LLMTokens{Input: 100, Output: 20, Total: 120}
	env.CostUSD = 0.0012
	event, _ := mapOne(t, env)

	if event.Layer != edgecore.LayerLLM {
		t.Fatalf("layer = %q, want llm", event.Layer)
	}
	if event.ActionName != "llm.request" {
		t.Fatalf("action_name = %q, want llm.request", event.ActionName)
	}
	if event.Capability != "llm.request" {
		t.Fatalf("capability = %q, want llm.request", event.Capability)
	}
	for _, want := range []string{"llm", "provider_call", "data", "cost"} {
		if !containsString(event.RiskTags, want) {
			t.Fatalf("risk_tags missing %q: %v", want, event.RiskTags)
		}
	}
	if event.Labels["llm.provider"] != "anthropic" {
		t.Fatalf("missing llm.provider label: %v", event.Labels)
	}
	if event.Labels["llm.model"] != "claude-opus-4-8" {
		t.Fatalf("missing llm.model label: %v", event.Labels)
	}
	if event.Decision != edgecore.DecisionRecorded {
		t.Fatalf("event decision = %q, want RECORDED (policy decision is /evaluate's job)", event.Decision)
	}
}

func TestMap_RedactsSecretInMessages(t *testing.T) {
	env := baseEnvelope()
	env.Content = ""
	env.Messages = []LLMMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "here is my key " + awsExampleKey},
	}
	event, decision := mapOne(t, env)
	if decision.Decision != DecisionRedact {
		t.Fatalf("decision = %q, want redact", decision.Decision)
	}
	if !containsString(decision.Findings, "aws_credential") {
		t.Fatalf("findings missing aws_credential: %v", decision.Findings)
	}
	if strings.Contains(eventInputString(t, event), awsExampleKey) {
		t.Fatalf("event leaked secret in messages: %v", event.InputRedacted)
	}
	// Finding 1 regression: a message-array-shaped redact decision must return
	// usable, role-preserving sanitized text, not just a flattened transcript.
	// Without RedactedMessages, a proxy has no safe way to reconstruct the
	// per-message shape it must forward.
	if len(decision.RedactedMessages) != 2 {
		t.Fatalf("want 2 redacted messages, got %d: %+v", len(decision.RedactedMessages), decision.RedactedMessages)
	}
	if decision.RedactedMessages[0].Role != "system" || decision.RedactedMessages[0].Content != "you are helpful" {
		t.Fatalf("redacted_messages[0] mismatch: %+v", decision.RedactedMessages[0])
	}
	if decision.RedactedMessages[1].Role != "user" {
		t.Fatalf("redacted_messages[1] role mismatch: %+v", decision.RedactedMessages[1])
	}
	if strings.Contains(decision.RedactedMessages[1].Content, awsExampleKey) {
		t.Fatalf("redacted_messages leaked the secret: %+v", decision.RedactedMessages)
	}
	// Note: this test exercises the FALLBACK (no injected redactor) path,
	// whose generic edge redactor replaces the whole tripped string rather
	// than masking in place — that in-place, prose-preserving behavior is the
	// injected Safety Kernel scanner's job and is covered separately by
	// TestMap_MultiMessageRedactedMessagesPopulated in redactor_test.go.
	//
	// A proxy must also be able to fall back to the flattened transcript.
	if decision.RedactedContent == "" {
		t.Fatal("redacted_content should still be populated for message-array envelopes")
	}
	if strings.Contains(decision.RedactedContent, awsExampleKey) {
		t.Fatalf("redacted_content leaked the secret: %q", decision.RedactedContent)
	}
}

// TestMap_NoSecretMessagesStillPopulatesRedactedMessages proves clean
// message-array content also gets RedactedMessages (not just the redact
// path), so a proxy can always prefer the structured field for chat-shaped
// envelopes regardless of decision.
func TestMap_NoSecretMessagesStillPopulatesRedactedMessages(t *testing.T) {
	env := baseEnvelope()
	env.Content = ""
	env.Messages = []LLMMessage{
		{Role: "user", Content: "summarize the repo"},
	}
	_, decision := mapOne(t, env)
	if decision.Decision != DecisionRecord {
		t.Fatalf("decision = %q, want record", decision.Decision)
	}
	if len(decision.RedactedMessages) != 1 {
		t.Fatalf("want 1 redacted message, got %d: %+v", len(decision.RedactedMessages), decision.RedactedMessages)
	}
	if decision.RedactedMessages[0].Role != "user" || decision.RedactedMessages[0].Content != "summarize the repo" {
		t.Fatalf("redacted_messages[0] mismatch: %+v", decision.RedactedMessages[0])
	}
}

// TestMap_ContentOnlyEnvelopeHasNoRedactedMessages proves the single-string
// shape does not spuriously populate RedactedMessages (omitempty should drop
// the field from the wire response for that shape).
func TestMap_ContentOnlyEnvelopeHasNoRedactedMessages(t *testing.T) {
	_, decision := mapOne(t, baseEnvelope())
	if decision.RedactedMessages != nil {
		t.Fatalf("content-only envelope should not populate redacted_messages: %+v", decision.RedactedMessages)
	}
}

func TestMap_RejectsUnknownKind(t *testing.T) {
	env := baseEnvelope()
	env.Kind = "llm.policy.denied" // gateway-internal, not source-supplied
	_, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}})
	if err == nil {
		t.Fatal("expected rejection of unsupported kind")
	}
}

func TestMap_RequiresCoreFields(t *testing.T) {
	for _, mut := range []func(*LLMEventEnvelope){
		func(e *LLMEventEnvelope) { e.SessionID = "" },
		func(e *LLMEventEnvelope) { e.ExecutionID = "" },
		func(e *LLMEventEnvelope) { e.SourceEventID = "" },
		func(e *LLMEventEnvelope) { e.ObservedAt = time.Time{} },
		func(e *LLMEventEnvelope) { e.TenantID = "" },
	} {
		env := baseEnvelope()
		mut(&env)
		if _, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}}); err == nil {
			t.Fatalf("expected error for missing required field on %+v", env)
		}
	}
}

func TestMap_BatchTooLarge(t *testing.T) {
	events := make([]LLMEventEnvelope, MaxLLMBatchEvents+1)
	for i := range events {
		e := baseEnvelope()
		e.SourceEventID = "evt-" + time.Duration(i).String()
		events[i] = e
	}
	_, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: events})
	if err == nil {
		t.Fatal("expected ErrLLMBatchTooLarge")
	}
}

func TestMap_StableEventIDIdempotent(t *testing.T) {
	e1, _ := mapOne(t, baseEnvelope())
	e2, _ := mapOne(t, baseEnvelope())
	if e1.EventID != e2.EventID {
		t.Fatalf("event id not idempotent: %q vs %q", e1.EventID, e2.EventID)
	}
	if !strings.HasPrefix(e1.EventID, "llm-") {
		t.Fatalf("event id missing llm- prefix: %q", e1.EventID)
	}
	// A different source_event_id must produce a different id.
	other := baseEnvelope()
	other.SourceEventID = "evt-2"
	e3, _ := mapOne(t, other)
	if e3.EventID == e1.EventID {
		t.Fatal("different source_event_id should yield a different event id")
	}
}

func TestMap_SanitizesLabels(t *testing.T) {
	env := baseEnvelope()
	env.Labels = map[string]string{
		"repo":          "cordum",
		"authorization": "Bearer should-be-dropped",  // sensitive KEY → dropped
		"note":          "token is " + awsExampleKey, // sensitive VALUE → redacted
		"llm.provider":  "attacker-controlled",       // reserved prefix → dropped
	}
	event, _ := mapOne(t, env)
	if event.Labels["repo"] != "cordum" {
		t.Fatalf("benign label dropped: %v", event.Labels)
	}
	if _, ok := event.Labels["authorization"]; ok {
		t.Fatalf("sensitive-keyed label not dropped: %v", event.Labels)
	}
	if v := event.Labels["note"]; strings.Contains(v, awsExampleKey) {
		t.Fatalf("sensitive label value not redacted: %q", v)
	}
	// Reserved llm.* prefix from the source must not override classifier labels.
	if event.Labels["llm.provider"] != "anthropic" {
		t.Fatalf("source overrode reserved llm.provider: %v", event.Labels)
	}
}

func TestMap_ArtifactPointerTenantMismatchRejected(t *testing.T) {
	env := baseEnvelope()
	env.ArtifactPtrs = []edgecore.ArtifactPointer{{
		ArtifactType: edgecore.ArtifactTypeLLMPromptRedacted,
		TenantID:     "tenant-b", // mismatch
		SessionID:    env.SessionID,
		ExecutionID:  env.ExecutionID,
	}}
	if _, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}}); err == nil {
		t.Fatal("expected artifact tenant-mismatch rejection")
	}
}

func TestMap_ArtifactPointerReboundToEventScope(t *testing.T) {
	env := baseEnvelope()
	env.ArtifactPtrs = []edgecore.ArtifactPointer{{
		ArtifactType:   edgecore.ArtifactTypeLLMPromptRedacted,
		TenantID:       env.TenantID,
		SessionID:      env.SessionID,
		ExecutionID:    env.ExecutionID,
		RetentionClass: edgecore.RetentionClassStandard,
		RedactionLevel: edgecore.RedactionLevelStandard,
		URI:            "s3://bucket/prompt",
		SHA256:         "abc",
		CreatedAt:      time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
	}}
	event, _ := mapOne(t, env)
	if len(event.ArtifactPointers) != 1 {
		t.Fatalf("want 1 artifact pointer, got %d", len(event.ArtifactPointers))
	}
	if event.ArtifactPointers[0].EventID != event.EventID {
		t.Fatalf("artifact pointer not rebound to event id: %q", event.ArtifactPointers[0].EventID)
	}
}

func TestDecodeBatch_RejectsUnknownField(t *testing.T) {
	body := `{"source":{"source_id":"p"},"events":[{"tenant_id":"t","authorization":"Bearer x"}]}`
	if _, err := DecodeBatch(strings.NewReader(body)); err == nil {
		t.Fatal("expected strict decode to reject smuggled key")
	}
}

func TestDecodeBatch_RejectsTrailingData(t *testing.T) {
	body := `{"source":{"source_id":"p"},"events":[]}{"extra":1}`
	if _, err := DecodeBatch(strings.NewReader(body)); err == nil {
		t.Fatal("expected trailing-data rejection")
	}
}

func TestDecodeBatch_NonceOptionalByDefault(t *testing.T) {
	t.Setenv("CORDUM_EDGE_LLM_REPLAY_REQUIRED", "")
	body := `{"source":{"source_id":"p"},"events":[]}`
	if _, err := DecodeBatch(strings.NewReader(body)); err != nil {
		t.Fatalf("nonce should be optional by default: %v", err)
	}
}

func TestDecodeBatch_RejectsBadNonce(t *testing.T) {
	t.Setenv("CORDUM_EDGE_LLM_REPLAY_REQUIRED", "")
	body := `{"source":{"source_id":"p"},"nonce":"short","events":[]}`
	if _, err := DecodeBatch(strings.NewReader(body)); err == nil {
		t.Fatal("expected rejection of too-short nonce")
	}
}

func eventInputString(t *testing.T, event edgecore.AgentActionEvent) string {
	t.Helper()
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch vv := v.(type) {
		case string:
			b.WriteString(vv)
			b.WriteByte('\n')
		case map[string]any:
			for _, e := range vv {
				walk(e)
			}
		case []any:
			for _, e := range vv {
				walk(e)
			}
		}
	}
	walk(event.InputRedacted)
	for _, v := range event.Labels {
		b.WriteString(v)
		b.WriteByte('\n')
	}
	return b.String()
}

func containsString(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
