package mcp

import (
	"sync"
	"time"
)

// MCPDedupeKeyPrefix namespaces every Redis key the cross-process
// dedupe store writes. Operators scanning Redis can enumerate the
// dedupe set with `KEYS mcp:dedupe:*`; any subsystem using the same
// Redis instance is shielded from accidental key collisions with
// caller-supplied semantic dedupe keys.
const MCPDedupeKeyPrefix = "mcp:dedupe:"

// MCPDedupeTTL bounds how long a dedupe entry survives in Redis.
// Sized for the worst-case MCP tool call (long-running shell tools,
// HTTP fetches with retries) while still acting as a deadline-breaker
// for a stuck pending record: a winner that crashed before publishing
// the completed wire form releases its slot at most MCPDedupeTTL after
// the crash, instead of livelocking all subsequent retries.
const MCPDedupeTTL = 10 * time.Minute

// DedupeStore is the storage backend the MCP retry-dedupe layer
// consumes. The signature mirrors `sync.Map`'s LoadOrStore / Store /
// Delete so the in-process implementation is a thin wrapper and the
// Redis implementation can satisfy the same contract via `SET NX EX`.
//
// Implementations MUST be safe for concurrent use. LoadOrStore is the
// atomic check-write primitive the singleflight winner-selection
// depends on: the first caller observes (value, false) and assumes
// responsibility for publishing the completed record; subsequent
// callers observe (existing, true) and short-circuit on the cached
// outcome.
//
// CompareAndSwap and CompareAndDelete act only while key still holds old,
// the pending value the caller reserved the slot with, and report whether
// they did. A winner finishes through them, so a call whose slot another
// call took over (dedupeBeginResume) can no longer publish to it or
// delete it.
type DedupeStore interface {
	LoadOrStore(key string, value any) (actual any, loaded bool)
	Store(key string, value any)
	Delete(key string)
	CompareAndSwap(key string, old, new any) bool
	CompareAndDelete(key string, old any) bool
}

// NewInProcessDedupeStore returns the default in-process dedupe store
// — a thin sync.Map wrapper that satisfies the DedupeStore contract
// without any external dependency. This is the gateway's behavior on
// boot when no Redis client is wired AND the fallback path the Redis
// store routes to when Redis commands fail (taskRail #3 fail-soft).
func NewInProcessDedupeStore() DedupeStore {
	return &inProcessDedupeStore{}
}

// inProcessDedupeStore is the sync.Map-backed implementation. Behavior
// is byte-for-byte equivalent to the legacy `*sync.Map` the field
// previously held: LoadOrStore is the atomic singleflight primitive,
// Store overwrites, Delete clears. No TTL — process restart is the
// only expiry signal, which is acceptable because dedupe entries are
// scoped to one tool-call lifetime (winner publishes-or-errors within
// the InvokeToolWithPolicy callsite).
type inProcessDedupeStore struct {
	m sync.Map
}

func (s *inProcessDedupeStore) LoadOrStore(key string, value any) (any, bool) {
	return s.m.LoadOrStore(key, value)
}

func (s *inProcessDedupeStore) Store(key string, value any) {
	s.m.Store(key, value)
}

func (s *inProcessDedupeStore) Delete(key string) {
	s.m.Delete(key)
}

func (s *inProcessDedupeStore) CompareAndSwap(key string, old, new any) bool {
	return s.m.CompareAndSwap(key, old, new)
}

func (s *inProcessDedupeStore) CompareAndDelete(key string, old any) bool {
	return s.m.CompareAndDelete(key, old)
}
