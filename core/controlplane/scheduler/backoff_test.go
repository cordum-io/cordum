package scheduler

import (
	"testing"
	"time"
)

func TestBackoffDelayAttemptZero(t *testing.T) {
	d := backoffDelay(0, backoffBase, backoffMax)
	if d < backoffBase {
		t.Fatalf("attempt=0: got %v, want >= %v", d, backoffBase)
	}
	upper := backoffBase + backoffJitterMax
	if d > upper {
		t.Fatalf("attempt=0: got %v, want <= %v", d, upper)
	}
}

func TestBackoffDelayGrows(t *testing.T) {
	// attempt=3 should give base*8 = 8s + jitter
	d := backoffDelay(3, backoffBase, backoffMax)
	expected := 8 * backoffBase
	if d < expected {
		t.Fatalf("attempt=3: got %v, want >= %v", d, expected)
	}
	upper := expected + backoffJitterMax
	if upper > backoffMax {
		upper = backoffMax
	}
	if d > upper {
		t.Fatalf("attempt=3: got %v, want <= %v", d, upper)
	}
}

func TestBackoffDelayClampsToMax(t *testing.T) {
	d := backoffDelay(100, backoffBase, backoffMax)
	if d > backoffMax {
		t.Fatalf("attempt=100: got %v, want <= %v (maxDelay)", d, backoffMax)
	}
	// Should be close to max (within jitter, but clamped)
	if d < backoffMax-backoffJitterMax {
		t.Fatalf("attempt=100: got %v, want close to %v", d, backoffMax)
	}
}

func TestBackoffDelayNegativeAttempt(t *testing.T) {
	d := backoffDelay(-5, backoffBase, backoffMax)
	if d < backoffBase || d > backoffBase+backoffJitterMax {
		t.Fatalf("negative attempt: got %v, want in [%v, %v]", d, backoffBase, backoffBase+backoffJitterMax)
	}
}

func TestBackoffDelayJitterVaries(t *testing.T) {
	seen := make(map[time.Duration]bool)
	for i := 0; i < 20; i++ {
		d := backoffDelay(0, backoffBase, backoffMax)
		seen[d] = true
	}
	// With 20 samples and ns-precision jitter, we should see multiple distinct values.
	if len(seen) < 2 {
		t.Fatalf("expected jitter to produce varying results, got %d distinct values", len(seen))
	}
}

func TestBackoffDelayAttempt5Bounds(t *testing.T) {
	// attempt=5: base*32 = 32s, exceeds max=30s, so clamped
	d := backoffDelay(5, backoffBase, backoffMax)
	if d > backoffMax {
		t.Fatalf("attempt=5: got %v, want <= %v", d, backoffMax)
	}
}
