package llmingest

import (
	"strings"
	"testing"
	"time"
)

// Finding 4 interim mitigation: llm.stream.chunk redaction can be evaded by a
// secret split across chunk boundaries, since each chunk is scanned in
// isolation. Full server-side reassembly-before-classification is a real
// architectural feature (buffering by stream_id, ordering by sequence,
// scanning once on final) and is NOT implemented in this pass. Instead:
//   - stream_id / sequence / final are added to the wire schema so a future
//     reassembly pass has a stable key to build on.
//   - EventDecision.RedactionComplete tells the proxy, machine-readably,
//     whether a given decision reflects a full-content scan (true for every
//     kind except llm.stream.chunk; for a chunk, true ONLY when final=true).
//   - A final=true chunk with no content/messages to scan is rejected
//     outright — a proxy cannot claim redaction-complete without actually
//     submitting the full aggregated text for scanning.
//
// See docs/edge/llm-proxy-governance.md "Streaming chunk redaction limits".

func streamChunkEnvelope(content string, seq int, final bool) LLMEventEnvelope {
	s := seq
	return LLMEventEnvelope{
		TenantID:      "tenant-a",
		SessionID:     "sess-1",
		ExecutionID:   "exec-1",
		SourceEventID: "evt-chunk",
		ObservedAt:    time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC),
		Kind:          KindStreamChunk,
		Provider:      "anthropic",
		Model:         "claude-opus-4-8",
		Direction:     DirectionResponse,
		Content:       content,
		StreamID:      "stream-1",
		Sequence:      &s,
		Final:         final,
	}
}

func TestMap_NonFinalStreamChunkIsNotRedactionComplete(t *testing.T) {
	env := streamChunkEnvelope("here is half a secret AKIA", 0, false)
	_, decision := mapOne(t, env)
	if decision.RedactionComplete {
		t.Fatal("a non-final stream chunk must not claim redaction-complete")
	}
}

func TestMap_FinalStreamChunkIsRedactionComplete(t *testing.T) {
	env := streamChunkEnvelope("full aggregated response, no secrets here", 1, true)
	_, decision := mapOne(t, env)
	if !decision.RedactionComplete {
		t.Fatal("a final stream chunk carrying full content must be redaction-complete")
	}
}

func TestMap_NonChunkKindsAreAlwaysRedactionComplete(t *testing.T) {
	_, decision := mapOne(t, baseEnvelope()) // KindRequestPre
	if !decision.RedactionComplete {
		t.Fatal("a non-stream-chunk envelope must always be redaction-complete")
	}
}

func TestMap_RejectsFinalStreamChunkWithNoContent(t *testing.T) {
	env := streamChunkEnvelope("", 2, true)
	_, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}})
	if err == nil {
		t.Fatal("expected rejection of a final chunk with nothing to scan")
	}
}

// TestMap_RejectsFinalStreamChunkWithRoleOnlyMessages proves the guard checks
// each message's actual Content, not just len(Messages)>0: a Messages slice
// whose entries are all role-only (empty Content, e.g. a tool-call-only turn)
// is just as hollow as an empty slice and must be rejected the same way.
func TestMap_RejectsFinalStreamChunkWithRoleOnlyMessages(t *testing.T) {
	env := streamChunkEnvelope("", 2, true)
	env.Messages = []LLMMessage{{Role: "assistant"}, {Role: "tool"}}
	if _, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}}); err == nil {
		t.Fatal("expected rejection of a final chunk whose messages are all role-only (no scannable content)")
	}
}

func TestMap_AllowsFinalStreamChunkWithMessagesOnly(t *testing.T) {
	env := streamChunkEnvelope("", 2, true)
	env.Messages = []LLMMessage{{Role: "assistant", Content: "the full aggregated reply"}}
	_, decision := mapOne(t, env)
	if !decision.RedactionComplete {
		t.Fatal("a final chunk with full content in Messages must be redaction-complete")
	}
}

func TestMap_RejectsOversizeStreamID(t *testing.T) {
	env := streamChunkEnvelope("hi", 0, false)
	env.StreamID = strings.Repeat("s", MaxLLMShortFieldBytes+1)
	if _, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}}); err == nil {
		t.Fatal("expected rejection of oversize stream_id")
	}
}

func TestMap_RejectsNegativeSequence(t *testing.T) {
	env := streamChunkEnvelope("hi", 0, false)
	neg := -1
	env.Sequence = &neg
	if _, err := (&Adapter{}).Map(LLMBatch{Source: SourceIdentity{ID: "p"}, Events: []LLMEventEnvelope{env}}); err == nil {
		t.Fatal("expected rejection of negative sequence")
	}
}

// TestMap_RedactionIncompleteLabelStampedOnEvent proves the audit trail
// itself (not just the synchronous response) carries the incompleteness
// signal, so an auditor querying stored events directly can tell a
// per-chunk-only scan apart from a complete-content one.
func TestMap_RedactionIncompleteLabelStampedOnEvent(t *testing.T) {
	env := streamChunkEnvelope("partial chunk content", 0, false)
	event, decision := mapOne(t, env)
	if decision.RedactionComplete {
		t.Fatal("precondition: non-final chunk should not be redaction-complete")
	}
	if event.Labels["llm.redaction_incomplete"] != "true" {
		t.Fatalf("event missing llm.redaction_incomplete label: %v", event.Labels)
	}
}

func TestMap_FinalChunkDoesNotStampIncompleteLabel(t *testing.T) {
	env := streamChunkEnvelope("full aggregated content", 1, true)
	event, _ := mapOne(t, env)
	if _, ok := event.Labels["llm.redaction_incomplete"]; ok {
		t.Fatalf("final chunk should not carry the incomplete label: %v", event.Labels)
	}
}
