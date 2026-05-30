package mcp

import (
	"context"
	"sync"
	"time"
)

// MCPTaintKeyPrefix namespaces every Redis key the session-taint store writes,
// matching the MCPDedupeKeyPrefix convention so operators can enumerate taints
// with `KEYS mcp:taint:*` and the namespace cannot collide with other keys on a
// shared Redis instance.
const MCPTaintKeyPrefix = "mcp:taint:"

// SessionTaint records a prompt-injection finding detected in a PRIOR tool-call
// RESULT within a (tenant, session). The MCP read path persists it on a hit; the
// next destructive call in the same session reads it pre-dispatch so the gate can
// DENY content-awarely, citing the finding. Snippet is already bounded and
// control-char-stripped (attacker-controlled board content) before it lands here.
type SessionTaint struct {
	Tool          string    `json:"tool,omitempty"`            // tool whose result carried the injection
	Pattern       string    `json:"pattern,omitempty"`         // scanner rule label that matched
	Snippet       string    `json:"snippet,omitempty"`         // bounded, sanitized excerpt of the injected content
	Severity      string    `json:"severity,omitempty"`        // scanner severity (e.g. "high")
	Confidence    float64   `json:"confidence,omitempty"`      // scanner confidence in [0,1]
	DetectedAt    time.Time `json:"detected_at,omitempty"`     // when the taint was first detected
	SourceEventID string    `json:"source_event_id,omitempty"` // audit event id of the tainting read
}

// ResultFinding is the mcp-local shape of a prompt-injection hit returned by the
// injected ToolCallDeps.ResultScanner. It mirrors the gateway scanner's exported
// safetykernel.InjectionFinding so core/mcp need not import safetykernel (the
// gateway adapts InjectionFinding -> ResultFinding at wiring time, avoiding a
// core/mcp -> safetykernel import cycle).
type ResultFinding struct {
	Pattern    string
	Snippet    string
	Severity   string
	Confidence float64
}

// TaintStore persists session taints keyed by (tenant, session). Implementations
// MUST be safe for concurrent use. Mirrors the DedupeStore split: an in-process
// map for single-instance/test use and a Redis implementation for multi-instance
// HA, chosen by SelectTaintStore. A nil TaintStore in ToolCallDeps disables the
// session-taint feature (like a nil DedupeState).
type TaintStore interface {
	// Taint persists (overwrites) the taint for (tenant, session). Overwrite —
	// not insert-once — so a fresher/higher-signal finding refreshes the record
	// and its TTL. A clean read never calls Taint, so a tainted session stays
	// tainted until TTL expiry (the prompt-injection kill-chain semantics).
	Taint(ctx context.Context, tenant, session string, t SessionTaint) error
	// GetTaint returns the taint for (tenant, session). ok=false means no taint
	// (clean session — the common case). A non-nil error signals a store failure
	// the caller handles per its fail-open/closed posture; ok is false on error.
	GetTaint(ctx context.Context, tenant, session string) (*SessionTaint, bool, error)
}

// taintKey builds the (tenant, session) composite used as the in-process map key
// and as the suffix of the Redis key (MCPTaintKeyPrefix + taintKey). Tenant and
// session are server-derived identity (CallMetadata) — never raw tool-call args,
// and EvaluateToolCall fail-closes on blank identity — so the ':' join cannot be
// steered by an attacker to alias two distinct sessions onto one key.
func taintKey(tenant, session string) string {
	return tenant + ":" + session
}

// inProcessTaintStore is the mutex+map TaintStore for single-instance gateways
// and tests. No TTL — process lifetime is the only expiry, acceptable because a
// taint is scoped to a live session and tests inject a fresh store per fixture.
type inProcessTaintStore struct {
	mu sync.RWMutex
	m  map[string]SessionTaint
}

// NewInProcessTaintStore returns the dependency-free in-process TaintStore.
func NewInProcessTaintStore() TaintStore {
	return &inProcessTaintStore{m: make(map[string]SessionTaint)}
}

func (s *inProcessTaintStore) Taint(_ context.Context, tenant, session string, t SessionTaint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[taintKey(tenant, session)] = t
	return nil
}

func (s *inProcessTaintStore) GetTaint(_ context.Context, tenant, session string) (*SessionTaint, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.m[taintKey(tenant, session)]
	if !ok {
		return nil, false, nil
	}
	// Return a copy so a caller cannot mutate the stored record via the pointer.
	cp := t
	return &cp, true, nil
}
