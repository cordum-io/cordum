package scheduler

import "sync"

// flushLatch is a per-pool one-in-flight guard used by the
// scheduler's flush-on-worker-online path (task-7a2514ae). When a
// fleet rolling-restart brings up N workers for the same pool within
// a few milliseconds, each heartbeat reports an offline→online
// transition and would otherwise fan out N concurrent flushes for the
// same pool. The latch collapses the storm: the first goroutine wins
// acquire(pool); everyone else returns immediately.
//
// Zero value is usable. The guard is stateless beyond the sync.Map of
// in-flight pools; release(pool) removes the entry so the next flush
// for that pool can acquire freely.
type flushLatch struct {
	inFlight sync.Map // pool string → struct{}
}

// acquire returns true when the caller obtained exclusive right to
// flush pool. A false return means another flush is currently running
// for pool and the caller MUST NOT proceed.
func (l *flushLatch) acquire(pool string) bool {
	_, loaded := l.inFlight.LoadOrStore(pool, struct{}{})
	return !loaded
}

// release marks pool as no longer in-flight. Safe to call on a pool
// that was never acquired (no-op). Must be called by any goroutine
// that successfully acquired, typically in a defer.
func (l *flushLatch) release(pool string) {
	l.inFlight.Delete(pool)
}
