package mcp

import (
	"context"
	"sort"
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

// maxInProcessTaints caps the in-process taint map so an attacker cycling
// fabricated (largely client-supplied) session ids cannot grow it without bound
// — a memory-exhaustion DoS. Deliberately generous: organic load holds far fewer
// live taints than this within the TTL window.
const maxInProcessTaints = 10_000

// inProcessTaintEvictBatch is how far below the cap a cap-triggered eviction
// drops the map, so eviction amortizes to ~O(1) per write (one sort sweep every
// ~batch writes) instead of an O(n) oldest-scan on every over-cap write.
const inProcessTaintEvictBatch = 1_000

// inProcessTaintEntry is a stored taint plus its absolute expiry timestamp.
type inProcessTaintEntry struct {
	taint   SessionTaint
	expires time.Time
}

// inProcessTaintStore is the mutex+map TaintStore for single-instance gateways,
// tests, and the per-instance fallback the Redis store writes into on a Redis
// error. It is bounded two ways so it cannot leak: a per-entry TTL (a missing
// taint is the clean default, so expiry is FAIL-SAFE — it can only ever lose a
// taint, never fabricate one) and a max-entry cap with oldest-by-expiry
// eviction. A tainted session stays tainted until its TTL within the cap.
type inProcessTaintStore struct {
	mu         sync.RWMutex
	m          map[string]inProcessTaintEntry
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
}

// NewInProcessTaintStore returns the dependency-free in-process TaintStore,
// bounded by the resolved taint TTL (CORDUM_MCP_TAINT_TTL / defaultTaintTTL —
// the same lifetime the Redis backend uses) and maxInProcessTaints entries.
func NewInProcessTaintStore() TaintStore {
	return newInProcessTaintStore(resolveTaintTTL(), maxInProcessTaints, time.Now)
}

// newInProcessTaintStore is the testable constructor: ttl, cap, and clock are
// injectable. A non-positive ttl/cap falls back to the defaults; a nil clock to
// time.Now.
func newInProcessTaintStore(ttl time.Duration, maxEntries int, now func() time.Time) *inProcessTaintStore {
	if ttl <= 0 {
		ttl = defaultTaintTTL
	}
	if maxEntries <= 0 {
		maxEntries = maxInProcessTaints
	}
	if now == nil {
		now = time.Now
	}
	return &inProcessTaintStore{
		m:          make(map[string]inProcessTaintEntry),
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        now,
	}
}

func (s *inProcessTaintStore) Taint(_ context.Context, tenant, session string, t SessionTaint) error {
	key := taintKey(tenant, session)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = inProcessTaintEntry{taint: t, expires: s.now().Add(s.ttl)}
	if len(s.m) > s.maxEntries {
		s.evictLocked(key)
	}
	return nil
}

// evictLocked bounds the map. It first drops every expired entry (cheap, and the
// common steady-state bound), then — only if still over the cap (a sustained
// attack within the TTL window) — drops the oldest-by-expiry entries down to a
// low watermark in one batch so the cost amortizes. The caller holds s.mu; keep
// (the just-written entry) is never evicted.
func (s *inProcessTaintStore) evictLocked(keep string) {
	now := s.now()
	for k, e := range s.m {
		if k != keep && now.After(e.expires) {
			delete(s.m, k)
		}
	}
	if len(s.m) <= s.maxEntries {
		return
	}
	type keyed struct {
		key     string
		expires time.Time
	}
	live := make([]keyed, 0, len(s.m))
	for k, e := range s.m {
		if k == keep {
			continue
		}
		live = append(live, keyed{key: k, expires: e.expires})
	}
	sort.Slice(live, func(i, j int) bool { return live[i].expires.Before(live[j].expires) })
	target := s.maxEntries - inProcessTaintEvictBatch
	if target < 1 {
		target = 1
	}
	for i := 0; i < len(live) && len(s.m) > target; i++ {
		delete(s.m, live[i].key)
	}
}

func (s *inProcessTaintStore) GetTaint(_ context.Context, tenant, session string) (*SessionTaint, bool, error) {
	key := taintKey(tenant, session)
	s.mu.RLock()
	e, ok := s.m[key]
	s.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if s.now().After(e.expires) {
		// Expired — opportunistically GC and report clean (the fail-safe
		// default). Re-check under the write lock so a concurrent refresh that
		// extended the entry is not dropped.
		s.mu.Lock()
		if cur, still := s.m[key]; still {
			// A concurrent refresh may have extended the entry between the
			// RUnlock above and acquiring this write lock. Re-read under the
			// lock: GC + report clean ONLY if it is STILL expired; otherwise
			// return the refreshed taint (ok=true) so the refresh is not
			// silently treated as clean for this request (which would weaken
			// taint gating).
			if s.now().After(cur.expires) {
				delete(s.m, key)
				s.mu.Unlock()
				return nil, false, nil
			}
			refreshed := cur.taint
			s.mu.Unlock()
			return &refreshed, true, nil
		}
		s.mu.Unlock()
		return nil, false, nil
	}
	// Return a copy so a caller cannot mutate the stored record via the pointer.
	cp := e.taint
	return &cp, true, nil
}

// entryCount returns the number of tracked taints (observability + tests).
func (s *inProcessTaintStore) entryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}
