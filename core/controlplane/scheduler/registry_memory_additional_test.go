package scheduler

import (
	"testing"
	"time"
)

func TestMemoryRegistryCloseStopsLoop(t *testing.T) {
	reg := NewMemoryRegistryWithTTL(10 * time.Millisecond)
	reg.Close()

	select {
	case <-reg.stopCh:
		// closed as expected
	default:
		t.Fatalf("expected stop channel to be closed")
	}
}
