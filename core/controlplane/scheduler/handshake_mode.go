package scheduler

// CORDUM_SDK_HANDSHAKE feature flag — gates the rollout of the
// SDK-handshake / session-token authority across the scheduler.
//
// Three modes, walked in this exact order during rollout:
//
//   1. off — legacy. Scheduler skips the handshake entirely; old
//      workers (and tests) are dispatched without a session token.
//      Useful for environments that haven't upgraded any workers yet.
//   2. warn — handshake is attempted; a missing or rejected
//      handshake is logged at ERROR (rate-limited to one event per
//      worker per hour to avoid flooding) but dispatch still
//      proceeds for handshakeless workers. This is the default
//      one-release migration window.
//   3. enforce — handshake is required. Workers without a valid
//      session token are refused dispatch. Strict-mode for
//      regulated tenants and post-migration deployments.
//
// Operators upgrade by leaving the default (warn) until they have
// visibility of the warn-mode logs, then flip to enforce once every
// worker reports a successful handshake.

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// HandshakeMode enumerates the rollout modes for the SDK handshake
// (CORDUM_SDK_HANDSHAKE).
type HandshakeMode string

const (
	HandshakeModeOff     HandshakeMode = "off"
	HandshakeModeWarn    HandshakeMode = "warn"
	HandshakeModeEnforce HandshakeMode = "enforce"

	// EnvHandshakeMode is the env var operators set to pick the mode.
	EnvHandshakeMode = "CORDUM_SDK_HANDSHAKE"

	// handshakeMissingLogInterval bounds how often warn-mode emits
	// a per-worker "missing handshake" ERROR. Once per worker per
	// hour avoids the log-flood failure mode where a misconfigured
	// fleet pegs the log pipeline.
	handshakeMissingLogInterval = time.Hour
)

// ParseHandshakeMode normalises a raw env-var value. Unknown values
// fall back to warn (the safe default — keeps dispatch flowing while
// operators investigate). Empty defaults to warn so a fresh deploy
// gets the migration-friendly mode without opt-in.
func ParseHandshakeMode(raw string) HandshakeMode {
	switch HandshakeMode(strings.ToLower(strings.TrimSpace(raw))) {
	case HandshakeModeOff:
		return HandshakeModeOff
	case HandshakeModeWarn:
		return HandshakeModeWarn
	case HandshakeModeEnforce:
		return HandshakeModeEnforce
	}
	return HandshakeModeWarn
}

// ParseHandshakeModeStrict is the BOOT-time parser. Unlike the lenient
// ParseHandshakeMode (which maps any unrecognized value to warn for runtime
// callers), it returns an error for any non-empty value that does not
// round-trip to a canonical mode — so a typo'd CORDUM_SDK_HANDSHAKE (e.g.
// "enforse") refuses to boot rather than silently degrading to admit. An empty
// value maps to Off (gate disabled) to preserve back-compat for deployments
// that never set the variable.
func ParseHandshakeModeStrict(raw string) (HandshakeMode, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return HandshakeModeOff, nil
	}
	switch HandshakeMode(strings.ToLower(trimmed)) {
	case HandshakeModeOff:
		return HandshakeModeOff, nil
	case HandshakeModeWarn:
		return HandshakeModeWarn, nil
	case HandshakeModeEnforce:
		return HandshakeModeEnforce, nil
	}
	return "", fmt.Errorf("unrecognized CORDUM_SDK_HANDSHAKE value %q; valid values: off, warn, enforce", trimmed)
}

// SkipsHandshake reports whether the mode bypasses the handshake
// entirely. Only off does — warn + enforce both attempt the
// handshake; the difference is whether a missing/failed result
// blocks dispatch.
func (m HandshakeMode) SkipsHandshake() bool { return m == HandshakeModeOff }

// EnforcesHandshake reports whether handshake-less workers are
// refused dispatch. True for enforce only.
func (m HandshakeMode) EnforcesHandshake() bool { return m == HandshakeModeEnforce }

// WarnsOnMissingHandshake reports whether the mode logs a structured
// ERROR when a worker connects without a valid session token. True
// for warn only — off doesn't compute, enforce already refused.
func (m HandshakeMode) WarnsOnMissingHandshake() bool { return m == HandshakeModeWarn }

// String returns the canonical lowercase form so callers can log the
// active mode without conditional formatting.
func (m HandshakeMode) String() string {
	if m == "" {
		return string(HandshakeModeWarn)
	}
	return string(m)
}

// LogActiveMode emits a single INFO log at scheduler boot describing
// the active mode + its consequences. Helpful so anyone inspecting a
// deploy can grep the scheduler logs and confirm which mode is live.
func (m HandshakeMode) LogActiveMode(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	switch m {
	case HandshakeModeOff:
		logger.Info("sdk handshake mode active",
			"mode", string(m),
			"semantics", "legacy: handshake skipped; workers dispatched without session token",
		)
	case HandshakeModeWarn:
		logger.Info("sdk handshake mode active",
			"mode", string(m),
			"semantics", "handshake attempted; missing-handshake workers logged but still dispatched",
			"log_interval", handshakeMissingLogInterval.String(),
		)
	case HandshakeModeEnforce:
		logger.Info("sdk handshake mode active",
			"mode", string(m),
			"semantics", "handshake required; workers without a valid session token are refused dispatch",
		)
	}
}

// HandshakeMissingTracker rate-limits warn-mode "missing handshake"
// log lines so a misconfigured fleet doesn't flood the log pipeline.
// Concurrency-safe; one tracker per scheduler process.
type HandshakeMissingTracker struct {
	mu       sync.Mutex
	lastLog  map[string]time.Time
	interval time.Duration
	now      func() time.Time
}

// NewHandshakeMissingTracker returns a tracker with the default
// per-worker log interval (1 hour). Tests can override the clock via
// WithClock.
func NewHandshakeMissingTracker() *HandshakeMissingTracker {
	return &HandshakeMissingTracker{
		lastLog:  map[string]time.Time{},
		interval: handshakeMissingLogInterval,
		now:      time.Now,
	}
}

// WithClock injects a deterministic clock for tests. Returns the
// tracker for fluent chaining.
func (t *HandshakeMissingTracker) WithClock(now func() time.Time) *HandshakeMissingTracker {
	if t == nil || now == nil {
		return t
	}
	t.now = now
	return t
}

// WithInterval overrides the default rate-limit interval. Returns
// the tracker for fluent chaining; non-positive values are ignored.
func (t *HandshakeMissingTracker) WithInterval(d time.Duration) *HandshakeMissingTracker {
	if t == nil || d <= 0 {
		return t
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.interval = d
	return t
}

// ShouldLog reports whether the caller should emit the warn-mode
// "missing handshake" log for workerID right now. Returns true at
// most once per workerID per interval. The first observation always
// returns true so operators see the worker the moment it first
// connects without a handshake.
func (t *HandshakeMissingTracker) ShouldLog(workerID string) bool {
	if t == nil {
		return true
	}
	id := strings.TrimSpace(workerID)
	if id == "" {
		return false
	}
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	last, ok := t.lastLog[id]
	if ok && now.Sub(last) < t.interval {
		return false
	}
	t.lastLog[id] = now
	return true
}

// Reset clears the tracker — useful between tests so cross-test state
// doesn't leak.
func (t *HandshakeMissingTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastLog = map[string]time.Time{}
}
