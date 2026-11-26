package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
