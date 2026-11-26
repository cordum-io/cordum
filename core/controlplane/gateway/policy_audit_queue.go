package gateway

import (
	"errors"
	"sync"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// policyAuditBatchMax caps the entries one policy audit write carries. It is
// the log's own cap, so a write never trims an entry it carries.
const policyAuditBatchMax = 500

// errPolicyAuditWriteAborted is the result of a batch whose write never
// returned, so no waiter reads a nil error for an entry nobody stored.
var errPolicyAuditWriteAborted = errors.New("policy audit write aborted")

// policyAuditQueue batches concurrent appends to the policy audit log (group
// commit). One appender at a time holds the turn: it writes every entry queued
// by then, up to policyAuditBatchMax, in one write, then hands the turn to the
// oldest queued appender. The zero value is ready to use.
type policyAuditQueue struct {
	mu      sync.Mutex
	waiting []*policyAuditWaiter
	writing bool // a turn holder exists; while false, waiting is empty
}

type policyAuditWaiter struct {
	entry policybundles.PolicyAuditEntry
	turn  chan struct{} // closed when this waiter must write the next batch
	done  chan struct{} // closed once err is the result of the write that carried entry
	err   error
}

// append queues entry and returns the result of the one write that carried
// it: nil only once that write has stored it.
func (q *policyAuditQueue) append(entry policybundles.PolicyAuditEntry, write func([]policybundles.PolicyAuditEntry) error) error {
	w := &policyAuditWaiter{entry: entry, turn: make(chan struct{}), done: make(chan struct{})}
	q.mu.Lock()
	q.waiting = append(q.waiting, w)
	waitForTurn := q.writing
	q.writing = true
	q.mu.Unlock()
	if waitForTurn {
		select {
		case <-w.done:
			return w.err
		case <-w.turn:
		}
	}
	q.writeBatch(write)
	<-w.done
	return w.err
}

// writeBatch writes the queued entries as the turn holder, which is always the
// first of them, sets the result on every waiter it took, and hands the turn on.
func (q *policyAuditQueue) writeBatch(write func([]policybundles.PolicyAuditEntry) error) {
	q.mu.Lock()
	batch := q.waiting[:min(len(q.waiting), policyAuditBatchMax)]
	q.waiting = q.waiting[len(batch):]
	q.mu.Unlock()

	err := errPolicyAuditWriteAborted
	defer func() {
		for _, w := range batch {
			w.err = err
			close(w.done)
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if len(q.waiting) > 0 {
			close(q.waiting[0].turn)
			return
		}
		q.writing = false
		q.waiting = nil
	}()
	entries := make([]policybundles.PolicyAuditEntry, len(batch))
	for i, w := range batch {
		entries[i] = w.entry
	}
	err = write(entries)
}
