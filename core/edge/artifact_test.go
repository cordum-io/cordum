package edge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestAttachArtifactPointerAttachesValidPointerOntoEvent is the happy path —
// a pointer whose tenant/session/execution/event IDs match the event must be
// appended in order, with the event's other fields untouched.
func TestAttachArtifactPointerAttachesValidPointerOntoEvent(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	event := validAgentActionEvent(started)
	event.ArtifactPointers = nil // reset to empty so we can prove a clean append
	ptr := validArtifactPointer(started)
	ptr.TenantID = event.TenantID
	ptr.SessionID = event.SessionID
	ptr.ExecutionID = event.ExecutionID
	ptr.EventID = event.EventID

	if err := AttachArtifactPointer(&event, ptr); err != nil {
		t.Fatalf("AttachArtifactPointer returned %v, want nil", err)
	}
	if got := len(event.ArtifactPointers); got != 1 {
		t.Fatalf("event.ArtifactPointers length = %d, want 1", got)
	}
	if event.ArtifactPointers[0].URI != ptr.URI {
		t.Errorf("appended pointer URI = %q, want %q", event.ArtifactPointers[0].URI, ptr.URI)
	}
	if err := event.Validate(); err != nil {
		t.Errorf("event.Validate() after attach returned %v, want nil", err)
	}
}

// TestAttachArtifactPointerRejectsCrossTenantPointer makes sure a pointer
// from tenant B cannot be smuggled into an event for tenant A. This is the
// most important security guarantee — a malicious or buggy caller must not
// be able to splice cross-tenant evidence into a session log.
func TestAttachArtifactPointerRejectsCrossTenantPointer(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	event := validAgentActionEvent(started)
	original := append([]ArtifactPointer(nil), event.ArtifactPointers...)
	ptr := validArtifactPointer(started)
	ptr.TenantID = "tenant-b"
	ptr.SessionID = event.SessionID
	ptr.ExecutionID = event.ExecutionID
	ptr.EventID = event.EventID

	err := AttachArtifactPointer(&event, ptr)
	if err == nil || !strings.Contains(err.Error(), "tenant_id") {
		t.Fatalf("AttachArtifactPointer cross-tenant returned %v, want tenant_id mismatch error", err)
	}
	// On error the event must not mutate.
	if len(event.ArtifactPointers) != len(original) {
		t.Errorf("event mutated on error: ArtifactPointers length = %d, want %d", len(event.ArtifactPointers), len(original))
	}
}

// TestAttachArtifactPointerRejectsMismatchedSessionExecutionEventIDs
// covers the remaining identity dimensions. The pointer must agree with
// the event on tenant + session + execution + event IDs; any one mismatch
// is a hard reject.
func TestAttachArtifactPointerRejectsMismatchedSessionExecutionEventIDs(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		mutate    func(*ArtifactPointer)
		wantErrIn string
	}{
		{name: "session mismatch", mutate: func(p *ArtifactPointer) { p.SessionID = "edge_sess_other" }, wantErrIn: "session_id"},
		{name: "execution mismatch", mutate: func(p *ArtifactPointer) { p.ExecutionID = "exec_other" }, wantErrIn: "execution_id"},
		{name: "event mismatch", mutate: func(p *ArtifactPointer) { p.EventID = "evt_other" }, wantErrIn: "event_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := validAgentActionEvent(started)
			ptr := validArtifactPointer(started)
			ptr.TenantID = event.TenantID
			ptr.SessionID = event.SessionID
			ptr.ExecutionID = event.ExecutionID
			ptr.EventID = event.EventID
			tc.mutate(&ptr)
			err := AttachArtifactPointer(&event, ptr)
			if err == nil || !strings.Contains(err.Error(), tc.wantErrIn) {
				t.Fatalf("AttachArtifactPointer %s returned %v, want error containing %q", tc.name, err, tc.wantErrIn)
			}
		})
	}
}

// TestAttachArtifactPointerRejectsInvalidPointer ensures the helper runs the
// full ArtifactPointer.Validate() before mutating, so callers cannot bypass
// pointer-level invariants by attaching directly.
func TestAttachArtifactPointerRejectsInvalidPointer(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(*ArtifactPointer)
	}{
		{name: "missing sha256", mutate: func(p *ArtifactPointer) { p.SHA256 = "" }},
		{name: "missing uri", mutate: func(p *ArtifactPointer) { p.URI = "" }},
		{name: "missing created_at", mutate: func(p *ArtifactPointer) { p.CreatedAt = time.Time{} }},
		{name: "unsafe artifact_type", mutate: func(p *ArtifactPointer) { p.ArtifactType = "edge.raw_secret" }},
		{name: "invalid retention_class", mutate: func(p *ArtifactPointer) { p.RetentionClass = "forever" }},
		{name: "invalid redaction_level", mutate: func(p *ArtifactPointer) { p.RedactionLevel = "none" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := validAgentActionEvent(started)
			before := len(event.ArtifactPointers)
			ptr := validArtifactPointer(started)
			ptr.TenantID = event.TenantID
			ptr.SessionID = event.SessionID
			ptr.ExecutionID = event.ExecutionID
			ptr.EventID = event.EventID
			tc.mutate(&ptr)
			if err := AttachArtifactPointer(&event, ptr); err == nil {
				t.Fatalf("AttachArtifactPointer %s returned nil, want validation error", tc.name)
			}
			if len(event.ArtifactPointers) != before {
				t.Errorf("event mutated on validation error: length = %d, want %d", len(event.ArtifactPointers), before)
			}
		})
	}
}

// TestAttachArtifactPointerEnforcesMaxArtifactPointersPerEvent prevents an
// adversary or runaway producer from stuffing an event with thousands of
// pointers. Hits the same MaxArtifactPointersPerEvent ceiling that
// AgentActionEvent.Validate already checks, but here we enforce it before
// the append so the event never transiently exceeds the bound.
func TestAttachArtifactPointerEnforcesMaxArtifactPointersPerEvent(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	event := validAgentActionEvent(started)
	event.ArtifactPointers = make([]ArtifactPointer, MaxArtifactPointersPerEvent)
	for i := range event.ArtifactPointers {
		ptr := validArtifactPointer(started)
		ptr.TenantID = event.TenantID
		ptr.SessionID = event.SessionID
		ptr.ExecutionID = event.ExecutionID
		ptr.EventID = event.EventID
		ptr.URI = ptr.URI + string(rune('a'+(i%26)))
		event.ArtifactPointers[i] = ptr
	}
	overflow := validArtifactPointer(started)
	overflow.TenantID = event.TenantID
	overflow.SessionID = event.SessionID
	overflow.ExecutionID = event.ExecutionID
	overflow.EventID = event.EventID
	err := AttachArtifactPointer(&event, overflow)
	if err == nil || !strings.Contains(err.Error(), "max") {
		t.Fatalf("AttachArtifactPointer overflow returned %v, want max-pointers error", err)
	}
	if len(event.ArtifactPointers) != MaxArtifactPointersPerEvent {
		t.Errorf("event mutated past cap: length = %d, want %d", len(event.ArtifactPointers), MaxArtifactPointersPerEvent)
	}
}

// TestAttachArtifactPointerRejectsDuplicateURI prevents the same artifact
// being attached twice to one event. Pointers are content-addressed via the
// (sha256, uri) tuple — duplicating a pointer would inflate the export
// bundle artifact count and could mask drift between two artifacts that
// share a URI but differ on hash.
func TestAttachArtifactPointerRejectsDuplicateURI(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	event := validAgentActionEvent(started)
	event.ArtifactPointers = nil
	ptr := validArtifactPointer(started)
	ptr.TenantID = event.TenantID
	ptr.SessionID = event.SessionID
	ptr.ExecutionID = event.ExecutionID
	ptr.EventID = event.EventID

	if err := AttachArtifactPointer(&event, ptr); err != nil {
		t.Fatalf("first attach returned %v, want nil", err)
	}
	if err := AttachArtifactPointer(&event, ptr); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("second attach with same URI returned %v, want duplicate error", err)
	}
	if len(event.ArtifactPointers) != 1 {
		t.Errorf("event ArtifactPointers length after duplicate attach = %d, want 1", len(event.ArtifactPointers))
	}
}

// TestAttachArtifactPointerOnNilEventReturnsError keeps the helper
// defensive against caller bugs without panicking.
func TestAttachArtifactPointerOnNilEventReturnsError(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	ptr := validArtifactPointer(started)
	if err := AttachArtifactPointer(nil, ptr); err == nil {
		t.Fatalf("AttachArtifactPointer(nil, ptr) returned nil, want error")
	}
}

// TestEventMarshalCarriesOnlyArtifactPointerMetadata is the wire-shape
// guarantee — an attached pointer reaches Redis/the dashboard as metadata
// only, never as inlined content. This is the rail enforced for the whole
// Edge subsystem ("no large raw transcripts/tool payloads in Redis events").
func TestEventMarshalCarriesOnlyArtifactPointerMetadata(t *testing.T) {
	started := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	event := validAgentActionEvent(started)
	event.ArtifactPointers = nil
	ptr := validArtifactPointer(started)
	ptr.TenantID = event.TenantID
	ptr.SessionID = event.SessionID
	ptr.ExecutionID = event.ExecutionID
	ptr.EventID = event.EventID
	if err := AttachArtifactPointer(&event, ptr); err != nil {
		t.Fatalf("AttachArtifactPointer returned %v, want nil", err)
	}

	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// The marshaled event must contain the pointer URI and sha256, but must
	// NOT contain raw artifact content fields. Anyone adding "content",
	// "body", "payload", etc. to ArtifactPointer would have to break this
	// test on purpose.
	must := []string{ptr.URI, ptr.SHA256, "artifact_ptrs"}
	for _, m := range must {
		if !strings.Contains(string(raw), m) {
			t.Errorf("event JSON missing %q: %s", m, raw)
		}
	}
	mustNot := []string{"\"content\"", "\"body\"", "\"payload\"", "\"raw\"", "\"transcript\""}
	for _, mn := range mustNot {
		if strings.Contains(string(raw), mn) {
			t.Errorf("event JSON unexpectedly contained %q (artifact bodies must not be inlined): %s", mn, raw)
		}
	}
}
