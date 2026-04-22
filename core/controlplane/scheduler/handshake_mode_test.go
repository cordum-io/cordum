package scheduler

import (
	"testing"
	"time"
)

func TestParseHandshakeMode(t *testing.T) {
	t.Parallel()
	cases := map[string]HandshakeMode{
		"":          HandshakeModeWarn,
		"off":       HandshakeModeOff,
		"OFF":       HandshakeModeOff,
		" warn ":    HandshakeModeWarn,
		"warn":      HandshakeModeWarn,
		"enforce":   HandshakeModeEnforce,
		" ENFORCE ": HandshakeModeEnforce,
		"bogus":     HandshakeModeWarn,
	}
	for in, want := range cases {
		if got := ParseHandshakeMode(in); got != want {
			t.Errorf("ParseHandshakeMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandshakeMode_Predicates(t *testing.T) {
	t.Parallel()
	type row struct {
		m       HandshakeMode
		skips   bool
		enforce bool
		warns   bool
	}
	rows := []row{
		{HandshakeModeOff, true, false, false},
		{HandshakeModeWarn, false, false, true},
		{HandshakeModeEnforce, false, true, false},
	}
	for _, r := range rows {
		if got := r.m.SkipsHandshake(); got != r.skips {
			t.Errorf("%s.SkipsHandshake() = %v, want %v", r.m, got, r.skips)
		}
		if got := r.m.EnforcesHandshake(); got != r.enforce {
			t.Errorf("%s.EnforcesHandshake() = %v, want %v", r.m, got, r.enforce)
		}
		if got := r.m.WarnsOnMissingHandshake(); got != r.warns {
			t.Errorf("%s.WarnsOnMissingHandshake() = %v, want %v", r.m, got, r.warns)
		}
	}
}

func TestHandshakeMode_StringDefaultsToWarn(t *testing.T) {
	t.Parallel()
	if HandshakeMode("").String() != string(HandshakeModeWarn) {
		t.Fatalf("expected empty mode to canonicalise to warn; got %q", HandshakeMode("").String())
	}
}

func TestHandshakeMissingTracker_RateLimitsPerWorker(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	tr := NewHandshakeMissingTracker().WithClock(clk.Now).WithInterval(time.Hour)

	if !tr.ShouldLog("worker-a") {
		t.Fatal("first observation should always log")
	}
	// Within the interval, suppress.
	if tr.ShouldLog("worker-a") {
		t.Fatal("second call within interval must not re-log")
	}
	// Different worker logs independently.
	if !tr.ShouldLog("worker-b") {
		t.Fatal("different worker must log on first observation")
	}
	// After the interval elapses, log again.
	clk.Advance(2 * time.Hour)
	if !tr.ShouldLog("worker-a") {
		t.Fatal("post-interval call must log again")
	}
}

func TestHandshakeMissingTracker_EmptyWorkerIDDoesNotLog(t *testing.T) {
	t.Parallel()
	tr := NewHandshakeMissingTracker()
	if tr.ShouldLog("   ") {
		t.Fatal("empty worker id must not log (would fingerprint everything as one identity)")
	}
}

func TestHandshakeMissingTracker_NilSafe(t *testing.T) {
	t.Parallel()
	var tr *HandshakeMissingTracker
	if !tr.ShouldLog("any") {
		t.Fatal("nil tracker must default to permitting the log so callers don't drop signal silently")
	}
	tr.Reset() // must not panic
	tr.WithClock(time.Now)
	tr.WithInterval(time.Minute)
}

func TestHandshakeMissingTracker_Reset(t *testing.T) {
	t.Parallel()
	tr := NewHandshakeMissingTracker().WithInterval(time.Hour)
	tr.ShouldLog("w") // primes the map
	if tr.ShouldLog("w") {
		t.Fatal("expected suppression before reset")
	}
	tr.Reset()
	if !tr.ShouldLog("w") {
		t.Fatal("reset must allow the next call to log")
	}
}
