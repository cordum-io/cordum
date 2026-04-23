// Package timeutil holds small, self-contained time-formatting helpers
// shared across the gateway handler layer. The package intentionally
// depends only on the standard library so any core package can depend
// on it without circular-import risk.
package timeutil

import "time"

// FormatUnixAuto converts a unix timestamp (seconds, milliseconds,
// microseconds, or nanoseconds) to its RFC3339 UTC representation,
// auto-detecting the unit by magnitude. The thresholds match the
// handlers_chat.go inline predecessor byte-for-byte:
//
//	ts > 1e18 -> nanoseconds
//	ts > 1e15 -> microseconds
//	ts > 1e12 -> milliseconds
//	else      -> seconds
//
// Modern timestamps bucket cleanly into these bands (seconds ~1.7e9,
// millis ~1.7e12, micros ~1.7e15, nanos ~1.7e18). Callers that KNOW
// their input unit at compile time should prefer the typed variants
// (FromSeconds / FromMillis / FromMicros / FromNanos) — auto-detect is
// only safe when the caller genuinely receives a union of units.
//
// Returns "" on ts <= 0 so operator views that render a formatted
// timestamp only when it's meaningful can skip the empty result
// without branching on zero.
func FormatUnixAuto(ts int64) string {
	if ts <= 0 {
		return ""
	}
	switch {
	case ts > 1_000_000_000_000_000_000: // > 1e18 -> nanoseconds
		return time.Unix(0, ts).UTC().Format(time.RFC3339)
	case ts > 1_000_000_000_000_000: // > 1e15 -> microseconds
		return time.UnixMicro(ts).UTC().Format(time.RFC3339)
	case ts > 1_000_000_000_000: // > 1e12 -> milliseconds
		return time.UnixMilli(ts).UTC().Format(time.RFC3339)
	default:
		return time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
}

// FromSeconds formats a unix-seconds timestamp as RFC3339 UTC.
// Returns "" on ts <= 0, matching the inline guards in the handler
// layer this helper replaces.
func FromSeconds(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

// FromMillis formats a unix-milliseconds timestamp as RFC3339 UTC.
// Returns "" on ts <= 0.
func FromMillis(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.UnixMilli(ts).UTC().Format(time.RFC3339)
}

// FromMicros formats a unix-microseconds timestamp as RFC3339 UTC.
// Returns "" on ts <= 0.
func FromMicros(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.UnixMicro(ts).UTC().Format(time.RFC3339)
}

// FromNanos formats a unix-nanoseconds timestamp as RFC3339 UTC.
// Returns "" on ts <= 0.
func FromNanos(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(0, ts).UTC().Format(time.RFC3339)
}
