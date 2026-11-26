package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Concurrent submits share the one policy audit log. Every submit must still
// store its own entry, so none of them fails with 503.
func TestSubmitJobHTTP_ConcurrentSubmitsEachStoreOneEntry(t *testing.T) {
	const n = 64
	s, bus, _ := submitAuditGateway(t, false)
	keys := make([]string, n)
	reqs := make([]*http.Request, n)
	for i := range n {
		keys[i] = fmt.Sprintf("submit-concurrent-%02d", i)
		body, err := json.Marshal(map[string]any{"prompt": "hi", "topic": "job.test", "idempotency_key": keys[i]})
		require.NoError(t, err)
		reqs[i] = httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
		reqs[i].Header.Set("X-Tenant-ID", "default")
	}

	recs := make([]*httptest.ResponseRecorder, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			recs[i] = httptest.NewRecorder()
			s.handleSubmitJobHTTP(recs[i], reqs[i])
		})
	}
	close(start)
	wg.Wait()

	unavailable := 0
	for _, rec := range recs {
		if rec.Code == http.StatusServiceUnavailable {
			unavailable++
		}
	}
	t.Logf("%d of %d concurrent submits answered 503", unavailable, n)
	for i, rec := range recs {
		require.Equal(t, http.StatusOK, rec.Code, "submit %d: %s", i, rec.Body.String())
	}
	require.Len(t, submitAuditPublished(bus), n)

	jobIDs := make([]string, n)
	for i, key := range keys {
		jobIDs[i], _ = submitAuditJobState(t, s, key)
	}
	entries, err := s.loadPolicyAudit(context.Background())
	require.NoError(t, err)
	var stored []string
	for _, entry := range entries {
		if entry.Action == "submit" {
			stored = append(stored, entry.ResourceID)
		}
	}
	require.ElementsMatch(t, jobIDs, stored, "each submit must store exactly one entry")
}

// policyAuditQueueWaiting returns how many appends wait for a turn or a batch.
func policyAuditQueueWaiting(s *server) int {
	s.policyAudit.mu.Lock()
	defer s.policyAudit.mu.Unlock()
	return len(s.policyAudit.waiting)
}

// policyAuditLogRevision returns the stored revision of the policy audit log.
func policyAuditLogRevision(t *testing.T, s *server) int64 {
	t.Helper()
	doc, err := s.configSvc.Get(context.Background(), configsvc.Scope(policybundles.PolicyAuditScope), policybundles.PolicyAuditID)
	require.NoError(t, err)
	return doc.Revision
}

// policyAuditQueueBehindBlockedWrite holds the first append's write after its
// read, queues queued more appends behind it, then lets the write finish. It
// returns every append's action and error, the blocked append's first.
func policyAuditQueueBehindBlockedWrite(t *testing.T, queued int) (*server, []string, []error) {
	t.Helper()
	s, _, hooked, _ := auditPersistServer(t)
	blocked, release := make(chan struct{}), make(chan struct{})
	hooked.AddHook(auditPersistHook{getKey: auditPersistLogKey, fired: &atomic.Bool{}, afterGet: func() {
		close(blocked)
		<-release
	}})
	actions := make([]string, queued+1)
	errs := make([]error, queued+1)
	var wg sync.WaitGroup
	appendAt := func(i int) {
		actions[i] = fmt.Sprintf("policy.queued.%03d", i)
		wg.Go(func() { errs[i] = s.appendPolicyAudit(context.Background(), auditPersistEntry(actions[i])) })
	}
	appendAt(0)
	<-blocked
	for i := 1; i <= queued; i++ {
		appendAt(i)
	}
	// assert, not require: the held write must be released either way.
	assert.Eventually(t, func() bool { return policyAuditQueueWaiting(s) == queued }, 10*time.Second, time.Millisecond)
	close(release)
	wg.Wait()
	return s, actions, errs
}

func TestPolicyAuditQueue_BatchesQueuedEntries(t *testing.T) {
	s, actions, errs := policyAuditQueueBehindBlockedWrite(t, 8)

	for i, err := range errs {
		require.NoError(t, err, "append %d", i)
	}
	require.ElementsMatch(t, actions, auditPersistActions(t, s))
	require.EqualValues(t, 2, policyAuditLogRevision(t, s), "the 8 queued entries were not stored in one write")
}

func TestPolicyAuditQueue_CapsABatchAt500(t *testing.T) {
	s, _, errs := policyAuditQueueBehindBlockedWrite(t, 501)

	for i, err := range errs {
		require.NoError(t, err, "append %d", i)
	}
	stored := auditPersistActions(t, s)
	require.Len(t, stored, 500)
	require.Len(t, slices.Compact(slices.Sorted(slices.Values(stored))), 500, "an entry was stored twice")
	require.EqualValues(t, 3, policyAuditLogRevision(t, s), "501 queued entries need two writes of at most 500")
}

func TestPolicyAuditQueue_FailsEveryEntryOfAFailedWrite(t *testing.T) {
	const n = 16
	s, mr, hooked, _ := auditPersistServer(t)
	hooked.AddHook(auditPersistHook{failKey: auditPersistLogKey})
	logs := storeWriterLogs(t)

	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = s.appendPolicyAudit(context.Background(), auditPersistEntry(fmt.Sprintf("policy.failed.%02d", i)))
		})
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.Error(t, err, "append %d reported an entry that was not stored", i)
		require.Len(t, auditPersistErrors(logs, fmt.Sprintf("policy.failed.%02d", i)), 1)
	}
	require.False(t, mr.Exists(auditPersistLogKey), "a failed write stored the audit log")
}

// A write that panics fails the other entries of its batch instead of
// reporting them stored, and the turn still passes on, so no append hangs.
func TestPolicyAuditQueue_PanickedWriteFailsItsBatch(t *testing.T) {
	var q policyAuditQueue
	held, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	write := func([]policybundles.PolicyAuditEntry) error {
		switch calls.Add(1) {
		case 1:
			close(held)
			<-release
		case 2:
			panic("injected write panic")
		}
		return nil
	}
	entry := policybundles.PolicyAuditEntry{Action: "policy.panic"}
	errs, panicked := make([]error, 3), make([]bool, 3)
	var wg sync.WaitGroup
	appendAt := func(i int) {
		wg.Go(func() {
			defer func() { panicked[i] = recover() != nil }()
			errs[i] = q.append(entry, write)
		})
	}
	appendAt(0)
	<-held
	appendAt(1)
	appendAt(2)
	assert.Eventually(t, func() bool { q.mu.Lock(); defer q.mu.Unlock(); return len(q.waiting) == 2 }, 10*time.Second, time.Millisecond)
	close(release)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("an append hung after a write panicked")
	}

	require.NoError(t, errs[0])
	require.NotEqual(t, panicked[1], panicked[2], "exactly one append holds the panicking batch's turn")
	for i := 1; i <= 2; i++ {
		if !panicked[i] {
			require.ErrorIs(t, errs[i], errPolicyAuditWriteAborted, "append %d", i)
		}
	}
	require.NoError(t, q.append(entry, write), "the turn did not pass on after the panic")
}
