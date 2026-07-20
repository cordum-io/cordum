package scheduler

import (
	"context"
	"sync"

	"github.com/cordum/cordum/core/audit"
)

// recordingSink is shared by scheduler audit tests.
type recordingSink struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (r *recordingSink) Emit(_ context.Context, event audit.SIEMEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingSink) last() audit.SIEMEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return audit.SIEMEvent{}
	}
	return r.events[len(r.events)-1]
}

func (r *recordingSink) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}
