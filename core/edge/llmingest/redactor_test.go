package llmingest

import (
	"strings"
	"testing"
	"time"
)

func redactorEnvelope(content string) LLMEventEnvelope {
	return LLMEventEnvelope{
		TenantID:      "tenant-a",
		SessionID:     "sess-1",
		ExecutionID:   "exec-1",
		SourceEventID: "evt-1",
		ObservedAt:    time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		Kind:          KindRequestPre,
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Direction:     DirectionPrompt,
		Content:       content,
	}
}

// TestMap_UsesInjectedRedactor proves the adapter delegates content redaction to
// the injected (shared Safety Kernel) redactor rather than any edge-local PII
// logic — content is masked in place, findings surface, and the event is labeled.
func TestMap_UsesInjectedRedactor(t *testing.T) {
	called := false
	fake := func(content string) (string, []string) {
		called = true
		if strings.Contains(content, "alice@example.com") {
			return strings.ReplaceAll(content, "alice@example.com", "<redacted:pii>"), []string{"pii"}
		}
		return content, nil
	}
	a := NewAdapter(AdapterOptions{Redactor: fake})
	res, err := a.Map(LLMBatch{
		Source: SourceIdentity{ID: "openai-proxy"},
		Events: []LLMEventEnvelope{redactorEnvelope("Summarize 1:1; ping alice@example.com.")},
	})
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	if !called {
		t.Fatal("injected redactor was not used")
	}
	d := res.Decisions[0]
	if d.Decision != DecisionRedact {
		t.Fatalf("decision = %q, want redact", d.Decision)
	}
	if strings.Contains(d.RedactedContent, "alice@example.com") {
		t.Fatalf("redacted content leaked PII: %q", d.RedactedContent)
	}
	if !strings.Contains(d.RedactedContent, "Summarize 1:1") {
		t.Fatalf("prose not preserved: %q", d.RedactedContent)
	}
	if len(d.Findings) != 1 || d.Findings[0] != "pii" {
		t.Fatalf("findings = %v, want [pii]", d.Findings)
	}
	if res.Events[0].Labels["llm.finding.pii"] != "true" {
		t.Fatalf("event missing llm.finding.pii label: %v", res.Events[0].Labels)
	}
}

// TestMap_CleanContentViaInjectedRedactor: no findings → record, content intact.
func TestMap_CleanContentViaInjectedRedactor(t *testing.T) {
	fake := func(content string) (string, []string) { return content, nil }
	a := NewAdapter(AdapterOptions{Redactor: fake})
	res, err := a.Map(LLMBatch{
		Source: SourceIdentity{ID: "openai-proxy"},
		Events: []LLMEventEnvelope{redactorEnvelope("summarize the standup")},
	})
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	d := res.Decisions[0]
	if d.Decision != DecisionRecord || d.Redacted {
		t.Fatalf("clean content should record: %+v", d)
	}
	if d.RedactedContent != "summarize the standup" {
		t.Fatalf("clean content should pass through: %q", d.RedactedContent)
	}
}

// TestMap_MultiMessageRedactedContentPopulated proves that a chat-style
// envelope (env.Messages, not env.Content) still populates RedactedContent
// via the injected ContentRedactor when Decision == DecisionRedact — the
// proxy contract for DecisionRedact says it "SHOULD forward RedactedContent
// instead of the original", so this field must not stay empty just because
// the envelope used the multi-message shape.
func TestMap_MultiMessageRedactedContentPopulated(t *testing.T) {
	fake := func(content string) (string, []string) {
		if strings.Contains(content, "alice@example.com") {
			return strings.ReplaceAll(content, "alice@example.com", "<redacted:pii>"), []string{"pii"}
		}
		return content, nil
	}
	a := NewAdapter(AdapterOptions{Redactor: fake})
	env := LLMEventEnvelope{
		TenantID:      "tenant-a",
		SessionID:     "sess-1",
		ExecutionID:   "exec-1",
		SourceEventID: "evt-1",
		ObservedAt:    time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		Kind:          KindRequestPre,
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Direction:     DirectionPrompt,
		Messages: []LLMMessage{
			{Role: "system", Content: "Be concise."},
			{Role: "user", Content: "Summarize 1:1; ping alice@example.com."},
		},
	}
	res, err := a.Map(LLMBatch{
		Source: SourceIdentity{ID: "openai-proxy"},
		Events: []LLMEventEnvelope{env},
	})
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	d := res.Decisions[0]
	if d.Decision != DecisionRedact {
		t.Fatalf("decision = %q, want redact", d.Decision)
	}
	if d.RedactedContent == "" {
		t.Fatal("RedactedContent must be populated for a multi-message envelope when Decision == redact")
	}
	if strings.Contains(d.RedactedContent, "alice@example.com") {
		t.Fatalf("redacted content leaked PII: %q", d.RedactedContent)
	}
	if !strings.Contains(d.RedactedContent, "Be concise.") || !strings.Contains(d.RedactedContent, "Summarize 1:1") {
		t.Fatalf("expected both message contents flattened in: %q", d.RedactedContent)
	}
}

// TestMap_FallsBackWithoutRedactor proves a nil redactor still redacts secrets
// via the generic edge redactor (the standalone/test path) — Phase 2 behavior.
func TestMap_FallsBackWithoutRedactor(t *testing.T) {
	a := NewAdapter(AdapterOptions{}) // no injected redactor
	res, err := a.Map(LLMBatch{
		Source: SourceIdentity{ID: "openai-proxy"},
		Events: []LLMEventEnvelope{redactorEnvelope("deploy with " + awsExampleKey + " now")},
	})
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	d := res.Decisions[0]
	if d.Decision != DecisionRedact {
		t.Fatalf("secret should still redact via fallback: %+v", d)
	}
	if !containsString(d.Findings, "aws_credential") {
		t.Fatalf("fallback should detect aws_credential: %v", d.Findings)
	}
}
