package timeutil_test

import (
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/timeutil"
)

// anchor is a fixed instant used across the magnitude tests so the
// same wall-clock time can be expressed in each unit. 2026-04-24T00:00:00Z.
var (
	anchorSec = time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC).Unix()
	anchorRFC = time.Date(2026, 4, 24, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
)

func TestFormatUnixAuto_Seconds(t *testing.T) {
	got := timeutil.FormatUnixAuto(anchorSec)
	if got != anchorRFC {
		t.Fatalf("seconds: got %q, want %q", got, anchorRFC)
	}
}

func TestFormatUnixAuto_Millis(t *testing.T) {
	got := timeutil.FormatUnixAuto(anchorSec * 1_000)
	if got != anchorRFC {
		t.Fatalf("millis: got %q, want %q", got, anchorRFC)
	}
}

func TestFormatUnixAuto_Micros(t *testing.T) {
	got := timeutil.FormatUnixAuto(anchorSec * 1_000_000)
	if got != anchorRFC {
		t.Fatalf("micros: got %q, want %q", got, anchorRFC)
	}
}

func TestFormatUnixAuto_Nanos(t *testing.T) {
	got := timeutil.FormatUnixAuto(anchorSec * 1_000_000_000)
	if got != anchorRFC {
		t.Fatalf("nanos: got %q, want %q", got, anchorRFC)
	}
}

func TestFormatUnixAuto_Zero(t *testing.T) {
	if got := timeutil.FormatUnixAuto(0); got != "" {
		t.Fatalf("ts=0: got %q, want empty", got)
	}
}

func TestFormatUnixAuto_Negative(t *testing.T) {
	if got := timeutil.FormatUnixAuto(-1); got != "" {
		t.Fatalf("ts=-1: got %q, want empty", got)
	}
}

// TestFormatUnixAuto_BoundaryAtMilli1e12 pins the threshold: a ts
// exactly at 1e12 must bucket as SECONDS (the comparison is strictly
// `> 1_000_000_000_000`), not milliseconds. Off-by-one here would
// misformat any tightly-packed near-boundary input from the chat
// history firehose.
func TestFormatUnixAuto_BoundaryAtMilli1e12(t *testing.T) {
	got := timeutil.FormatUnixAuto(1_000_000_000_000)
	want := time.Unix(1_000_000_000_000, 0).UTC().Format(time.RFC3339)
	if got != want {
		t.Fatalf("ts=1e12: got %q, want seconds-bucket %q", got, want)
	}
	// One tick higher picks the millis bucket.
	got = timeutil.FormatUnixAuto(1_000_000_000_001)
	want = time.UnixMilli(1_000_000_000_001).UTC().Format(time.RFC3339)
	if got != want {
		t.Fatalf("ts=1e12+1: got %q, want millis-bucket %q", got, want)
	}
}

func TestFromSeconds_ReturnsRFC3339(t *testing.T) {
	got := timeutil.FromSeconds(anchorSec)
	if got != anchorRFC {
		t.Fatalf("FromSeconds: got %q, want %q", got, anchorRFC)
	}
}

func TestFromMillis_ReturnsRFC3339(t *testing.T) {
	got := timeutil.FromMillis(anchorSec * 1_000)
	if got != anchorRFC {
		t.Fatalf("FromMillis: got %q, want %q", got, anchorRFC)
	}
}

func TestFromMicros_ReturnsRFC3339(t *testing.T) {
	got := timeutil.FromMicros(anchorSec * 1_000_000)
	if got != anchorRFC {
		t.Fatalf("FromMicros: got %q, want %q", got, anchorRFC)
	}
}

func TestFromNanos_ReturnsRFC3339(t *testing.T) {
	got := timeutil.FromNanos(anchorSec * 1_000_000_000)
	if got != anchorRFC {
		t.Fatalf("FromNanos: got %q, want %q", got, anchorRFC)
	}
}

// TestTypedVariants_ReturnEmptyOnZeroOrNegative pins the behavioural
// invariant the 4 inline wrapper funcs this helper replaces enforce:
// governanceTimestamp / millisToRFC3339 / timestampFromMicros all
// guard `ts <= 0 -> ""` before formatting. The typed helpers must do
// the same so byte-equivalence holds post-migration.
func TestTypedVariants_ReturnEmptyOnZeroOrNegative(t *testing.T) {
	for _, ts := range []int64{0, -1, -1_000_000} {
		if got := timeutil.FromSeconds(ts); got != "" {
			t.Fatalf("FromSeconds(%d): got %q, want empty", ts, got)
		}
		if got := timeutil.FromMillis(ts); got != "" {
			t.Fatalf("FromMillis(%d): got %q, want empty", ts, got)
		}
		if got := timeutil.FromMicros(ts); got != "" {
			t.Fatalf("FromMicros(%d): got %q, want empty", ts, got)
		}
		if got := timeutil.FromNanos(ts); got != "" {
			t.Fatalf("FromNanos(%d): got %q, want empty", ts, got)
		}
	}
}

// TestRoundTripParseable pins that every emitted string parses cleanly
// back through time.Parse — a regression here would mean the helper
// emitted a non-RFC3339-compliant string.
func TestRoundTripParseable(t *testing.T) {
	formats := []struct {
		name string
		out  string
	}{
		{"auto", timeutil.FormatUnixAuto(anchorSec)},
		{"seconds", timeutil.FromSeconds(anchorSec)},
		{"millis", timeutil.FromMillis(anchorSec * 1_000)},
		{"micros", timeutil.FromMicros(anchorSec * 1_000_000)},
		{"nanos", timeutil.FromNanos(anchorSec * 1_000_000_000)},
	}
	for _, f := range formats {
		if _, err := time.Parse(time.RFC3339, f.out); err != nil {
			t.Fatalf("%s: time.Parse round-trip failed for %q: %v", f.name, f.out, err)
		}
	}
}

// TestHandlersChatThresholds_Verbatim pins the three comparison
// constants byte-for-byte against the handlers_chat.go source the
// helper replaces. If anyone ever tunes the cascade without updating
// this test, the assertion below forces them to notice.
func TestHandlersChatThresholds_Verbatim(t *testing.T) {
	// Just above 1e12 picks millis (handlers_chat.go:415).
	if got, want := timeutil.FormatUnixAuto(1_000_000_000_001),
		time.UnixMilli(1_000_000_000_001).UTC().Format(time.RFC3339); got != want {
		t.Fatalf("threshold-millis mismatch: got %q want %q", got, want)
	}
	// Just above 1e15 picks micros (handlers_chat.go:413).
	if got, want := timeutil.FormatUnixAuto(1_000_000_000_000_001),
		time.UnixMicro(1_000_000_000_000_001).UTC().Format(time.RFC3339); got != want {
		t.Fatalf("threshold-micros mismatch: got %q want %q", got, want)
	}
	// Just above 1e18 picks nanos (handlers_chat.go:411).
	if got, want := timeutil.FormatUnixAuto(1_000_000_000_000_000_001),
		time.Unix(0, 1_000_000_000_000_000_001).UTC().Format(time.RFC3339); got != want {
		t.Fatalf("threshold-nanos mismatch: got %q want %q", got, want)
	}
}
