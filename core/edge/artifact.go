package edge

import (
	"fmt"
	"strings"
)

// AttachArtifactPointer appends ptr to event.ArtifactPointers after running
// the full pointer-level invariants and verifying that pointer identity
// matches the event's tenant/session/execution/event scope. The helper:
//
//   - never dereferences or loads the artifact body — the URI is opaque from
//     this layer's perspective
//   - never mutates event on error (the caller can retry without unwinding
//     state)
//   - rejects duplicate pointers (by canonical URI) so the bundle manifest
//     does not double-count one artifact
//   - enforces MaxArtifactPointersPerEvent before append so an event never
//     transiently exceeds the cap
//
// Cross-scope pointers are rejected up front because the export bundler
// trusts that every pointer on a session's events belongs to that session;
// allowing a tenant-B pointer to land on a tenant-A event would let an
// attacker splice cross-tenant evidence into the audit trail.
//
// Concurrency: this helper is NOT safe to call on the same *AgentActionEvent
// from multiple goroutines. The slice append + duplicate scan is unsynchronized;
// concurrent attaches on the same event can lose pointers or panic on a
// concurrent slice grow. Callers must serialize attaches per-event (in
// practice events are owned by a single hook→agentd flow, so this is
// trivial — the constraint is documented to keep that property intentional).
func AttachArtifactPointer(event *AgentActionEvent, ptr ArtifactPointer) error {
	if event == nil {
		return fmt.Errorf("event is required")
	}
	if err := ptr.Validate(); err != nil {
		return fmt.Errorf("artifact pointer: %w", err)
	}
	if got, want := strings.TrimSpace(ptr.TenantID), strings.TrimSpace(event.TenantID); got != want {
		return fmt.Errorf("artifact pointer tenant_id %q does not match event tenant_id %q", got, want)
	}
	if got, want := strings.TrimSpace(ptr.SessionID), strings.TrimSpace(event.SessionID); got != want {
		return fmt.Errorf("artifact pointer session_id %q does not match event session_id %q", got, want)
	}
	if got, want := strings.TrimSpace(ptr.ExecutionID), strings.TrimSpace(event.ExecutionID); got != want {
		return fmt.Errorf("artifact pointer execution_id %q does not match event execution_id %q", got, want)
	}
	if got, want := strings.TrimSpace(ptr.EventID), strings.TrimSpace(event.EventID); got != want {
		return fmt.Errorf("artifact pointer event_id %q does not match event event_id %q", got, want)
	}
	if len(event.ArtifactPointers) >= MaxArtifactPointersPerEvent {
		return fmt.Errorf("event has %d artifact pointers, max %d", len(event.ArtifactPointers), MaxArtifactPointersPerEvent)
	}
	for _, existing := range event.ArtifactPointers {
		if existing.URI == ptr.URI {
			return fmt.Errorf("duplicate artifact pointer uri %q", ptr.URI)
		}
	}
	event.ArtifactPointers = append(event.ArtifactPointers, ptr)
	return nil
}
