package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

var errInjectedCompleteIdempotency = errors.New("injected complete idempotency failure")

func TestGatewayEdgeEventSingleWriteIdempotencyReplaysWithoutDuplicate(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	body := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-idem-replay", "npm test")
	key := "edge0087-single-replay"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first idempotent write status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var firstEvent edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, first, &firstEvent)

	replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, key)
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want first status 201 body=%s", replay.Code, replay.Body.String())
	}
	var replayEvent edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, replay, &replayEvent)
	if replayEvent.EventID != firstEvent.EventID || replayEvent.Seq != firstEvent.Seq || replayEvent.InputHash != firstEvent.InputHash {
		t.Fatalf("replay event = %#v, want first event %#v", replayEvent, firstEvent)
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{firstEvent.EventID})
}

func TestGatewayEdgeEventSingleWriteIdempotencySameKeyDifferentBodyConflicts(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	key := "edge0087-single-conflict"
	firstBody := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-idem-conflict-a", "npm test")
	conflictBody := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-idem-conflict-b", "Authorization: Bearer edge0087-conflict-secret")

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", firstBody, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first idempotent write status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	conflict := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", conflictBody, key)
	assertEdgeErrorShape(t, conflict, http.StatusConflict, edgeErrCodeIdempotencyConflict)
	assertBodyOmits(t, conflict.Body.String(), "edge0087-conflict-secret", "Authorization: Bearer")

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-idem-conflict-a"})
}

func TestGatewayEdgeEventSingleWriteIdempotencyCrossTenantKeysAreIsolated(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	sessionA := createEdgeRouteSession(t, handler)
	sessionB := createEdgeRouteSessionForTenant(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant)
	key := "edge0087-cross-tenant"

	firstA := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events",
		idempotentEdgeEventBody(sessionA.SessionID, sessionA.ExecutionID, edgeRouteTenant, "evt-edge0087-tenant-a", "npm test"),
		key)
	if firstA.Code != http.StatusCreated {
		t.Fatalf("tenant A write status = %d, want 201 body=%s", firstA.Code, firstA.Body.String())
	}
	firstB := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteOtherAPIKey, edgeRouteOtherTenant, "/api/v1/edge/events",
		idempotentEdgeEventBody(sessionB.SessionID, sessionB.ExecutionID, edgeRouteOtherTenant, "evt-edge0087-tenant-b", "go test ./core/edge"),
		key)
	if firstB.Code != http.StatusCreated {
		t.Fatalf("tenant B same-key write status = %d, want isolated 201 body=%s", firstB.Code, firstB.Body.String())
	}

	var eventA, eventB edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, firstA, &eventA)
	decodeEdgeRouteJSON(t, firstB, &eventB)
	if eventA.TenantID != edgeRouteTenant || eventB.TenantID != edgeRouteOtherTenant {
		t.Fatalf("tenant isolation failed: A=%q B=%q", eventA.TenantID, eventB.TenantID)
	}
	if eventA.EventID == eventB.EventID {
		t.Fatalf("cross-tenant idempotency replayed tenant A event into tenant B: %#v", eventB)
	}
}

func TestGatewayEdgeEventSingleWriteWithoutIdempotencyKeyKeepsExistingAppendBehavior(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events",
		idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-no-key-1", "npm test"),
		"")
	if first.Code != http.StatusCreated {
		t.Fatalf("first non-idempotent write status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events",
		idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-no-key-2", "npm test"),
		"")
	if second.Code != http.StatusCreated {
		t.Fatalf("second non-idempotent write status = %d, want 201 body=%s", second.Code, second.Body.String())
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-no-key-1", "evt-edge0087-no-key-2"})
}

func TestGatewayEdgeEventSingleWriteIdempotencyReplayDoesNotExposeRawSecret(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	const rawSecret = "Bearer edge0087-idempotency-secret-token"
	body := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-secret", rawSecret)

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, "edge0087-secret-replay")
	if first.Code != http.StatusCreated {
		t.Fatalf("first secret-bearing write status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	assertBodyOmits(t, first.Body.String(), rawSecret)
	replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, "edge0087-secret-replay")
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay secret-bearing write status = %d, want 201 body=%s", replay.Code, replay.Body.String())
	}
	assertBodyOmits(t, replay.Body.String(), rawSecret)
}

func TestGatewayEdgeEventIdempotencyHashesAfterPrincipalOverride(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	firstBody := idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-principal-override", "npm test")
	firstBody["principal_id"] = "spoofed-client-principal-a"
	replayBody := idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-principal-override", "npm test")
	replayBody["principal_id"] = "spoofed-client-principal-b"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", mustJSON(firstBody), "edge0087-principal-override")
	if first.Code != http.StatusCreated {
		t.Fatalf("first write status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var created edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, first, &created)
	if created.PrincipalID != "principal-edge-a" {
		t.Fatalf("created principal_id = %q, want auth principal override", created.PrincipalID)
	}

	replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", mustJSON(replayBody), "edge0087-principal-override")
	if replay.Code != http.StatusCreated {
		t.Fatalf("replay with different client principal status = %d, want replay 201 body=%s", replay.Code, replay.Body.String())
	}
	var replayed edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, replay, &replayed)
	if replayed.EventID != created.EventID || replayed.PrincipalID != created.PrincipalID {
		t.Fatalf("replayed event = %#v, want original principal-overridden event %#v", replayed, created)
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-principal-override"})
}

func TestGatewayEdgeEventIdempotencyRejectsOversizeKeyWithoutAppend(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	body := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-huge-key", "npm test")

	rr := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, strings.Repeat("k", maxEdgeIdempotencyKeyBytes+1))
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeIdempotencyKeyTooLong)
	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, nil)
}

func TestGatewayEdgeEventAppendAtomicWithIdempotency(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	store := &edgeCompleteIdempotencyFailureStore{Store: s.edgeStore, completeFailures: 1}
	s.edgeStore = store
	session := createEdgeRouteSession(t, handler)
	key := "edge00871-single-atomic"
	body := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge00871-single-atomic", "npm test")

	rr := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, key)
	if rr.Code != http.StatusCreated {
		t.Fatalf("atomic idempotent append status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var created edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, rr, &created)
	if created.EventID != "evt-edge00871-single-atomic" || created.Seq != 1 {
		t.Fatalf("created event = %#v, want atomic event with seq 1", created)
	}
	if store.completeCalls != 0 {
		t.Fatalf("CompleteIdempotency calls = %d, want atomic path to skip separate completion", store.completeCalls)
	}
	if store.releaseCalls != 0 {
		t.Fatalf("ReleaseIdempotency calls = %d, want no partial cleanup path", store.releaseCalls)
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge00871-single-atomic"})

	replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, key)
	if replay.Code != rr.Code || replay.Body.String() != rr.Body.String() {
		t.Fatalf("replay response changed:\nfirst=%d %s\nreplay=%d %s", rr.Code, rr.Body.String(), replay.Code, replay.Body.String())
	}
	var replayed edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, replay, &replayed)
	if replayed.EventID != created.EventID || replayed.Seq != created.Seq {
		t.Fatalf("replayed event = %#v, want created event %#v", replayed, created)
	}
	page = readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge00871-single-atomic"})
}

func TestGatewayEdgeEventBatchAppendAtomicWithIdempotency(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	store := &edgeCompleteIdempotencyFailureStore{Store: s.edgeStore, completeFailures: 1}
	s.edgeStore = store
	session := createEdgeRouteSession(t, handler)
	key := "edge00871-batch-atomic"
	body := idempotentEdgeEventBatchBody(
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge00871-batch-atomic-1", "npm test"),
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge00871-batch-atomic-2", "go test ./core/edge"),
	)

	rr := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", body, key)
	if rr.Code != http.StatusCreated {
		t.Fatalf("atomic idempotent batch status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var created edgeEventBatchResponse
	decodeEdgeRouteJSON(t, rr, &created)
	if len(created.Items) != 2 || created.Items[0].Seq != 1 || created.Items[1].Seq != 2 {
		t.Fatalf("created batch = %#v, want 2 atomic events with seq 1,2", created)
	}
	if store.completeCalls != 0 {
		t.Fatalf("CompleteIdempotency calls = %d, want atomic path to skip separate completion", store.completeCalls)
	}
	if store.releaseCalls != 0 {
		t.Fatalf("ReleaseIdempotency calls = %d, want no partial cleanup path", store.releaseCalls)
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge00871-batch-atomic-1", "evt-edge00871-batch-atomic-2"})

	replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", body, key)
	if replay.Code != rr.Code || replay.Body.String() != rr.Body.String() {
		t.Fatalf("batch replay response changed:\nfirst=%d %s\nreplay=%d %s", rr.Code, rr.Body.String(), replay.Code, replay.Body.String())
	}
	page = readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge00871-batch-atomic-1", "evt-edge00871-batch-atomic-2"})
}

func TestGatewayEdgeEventAutoSeqIdempotencyTTLRetryDoesNotDuplicateEvent(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	key := "edge00871-autoseq-ttl"
	body := idempotentEdgeEventBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge00871-autoseq-ttl", "npm test")

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first idempotent write status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var firstEvent edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, first, &firstEvent)
	if firstEvent.Seq != 1 {
		t.Fatalf("first event seq = %d, want 1", firstEvent.Seq)
	}

	deleteGatewayEdgeIdempotencyKeys(t, s)
	retry := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events", body, key)
	assertEdgeErrorShape(t, retry, http.StatusConflict, edgeErrCodeIdempotencyWindowExpired)

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge00871-autoseq-ttl"})
}

func TestGatewayEdgeEventBatchIdempotencyReplaysWithoutDuplicate(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	body := idempotentEdgeEventBatchBody(
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-replay-1", "npm test"),
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-replay-2", "go test ./core/edge"),
	)
	key := "edge0087-batch-replay"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", body, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first idempotent batch status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var firstBatch edgeEventBatchResponse
	decodeEdgeRouteJSON(t, first, &firstBatch)
	if len(firstBatch.Items) != 2 {
		t.Fatalf("first batch item count = %d, want 2", len(firstBatch.Items))
	}

	replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", body, key)
	if replay.Code != http.StatusCreated {
		t.Fatalf("batch replay status = %d, want 201 body=%s", replay.Code, replay.Body.String())
	}
	var replayBatch edgeEventBatchResponse
	decodeEdgeRouteJSON(t, replay, &replayBatch)
	if len(replayBatch.Items) != len(firstBatch.Items) {
		t.Fatalf("replay item count = %d, want %d", len(replayBatch.Items), len(firstBatch.Items))
	}
	for i := range firstBatch.Items {
		if replayBatch.Items[i].EventID != firstBatch.Items[i].EventID || replayBatch.Items[i].Seq != firstBatch.Items[i].Seq {
			t.Fatalf("replay item %d = %#v, want first %#v", i, replayBatch.Items[i], firstBatch.Items[i])
		}
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-batch-replay-1", "evt-edge0087-batch-replay-2"})
}

func TestGatewayEdgeEventBatchIdempotencySameKeyDifferentBatchConflicts(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	key := "edge0087-batch-conflict"
	firstBody := idempotentEdgeEventBatchBody(
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-conflict-1", "npm test"),
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-conflict-2", "go test ./core/edge"),
	)
	const rawSecret = "Authorization: Bearer edge0087-batch-conflict-secret"
	conflictBody := idempotentEdgeEventBatchBody(
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-conflict-3", rawSecret),
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-conflict-4", "npm run build"),
	)

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", firstBody, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first idempotent batch status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	conflict := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", conflictBody, key)
	assertEdgeErrorShape(t, conflict, http.StatusConflict, edgeErrCodeIdempotencyConflict)
	assertBodyOmits(t, conflict.Body.String(), rawSecret, "Authorization: Bearer")

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-batch-conflict-1", "evt-edge0087-batch-conflict-2"})
}

func TestGatewayEdgeEventBatchIdempotencyInvalidLaterEventDoesNotCacheFailure(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	key := "edge0087-batch-invalid-retry"
	validFirst := idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-invalid-1", "npm test")
	invalidSecond := idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-invalid-2", "go test ./core/edge")
	invalidSecond["kind"] = ""
	invalidBody := idempotentEdgeEventBatchBody(validFirst, invalidSecond)

	invalid := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", invalidBody, key)
	assertEdgeErrorShape(t, invalid, http.StatusBadRequest, edgeErrCodeInvalidRequest)

	validBody := idempotentEdgeEventBatchBody(
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-valid-1", "npm test"),
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-valid-2", "go test ./core/edge"),
	)
	retry := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", validBody, key)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry after invalid batch status = %d, want 201 body=%s", retry.Code, retry.Body.String())
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-batch-valid-1", "evt-edge0087-batch-valid-2"})
}

func TestGatewayEdgeEventBatchIdempotencyConcurrentDuplicateAppendsOnce(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	body := idempotentEdgeEventBatchBody(
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-concurrent-1", "npm test"),
		idempotentEdgeEventMap(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-edge0087-batch-concurrent-2", "go test ./core/edge"),
	)
	const key = "edge0087-batch-concurrent"

	start := make(chan struct{})
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 2)
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			responses[index] = edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/events/batch", body, key)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, rr := range responses {
		if rr == nil {
			t.Fatalf("response %d is nil", i)
		}
		if rr.Code != http.StatusCreated {
			t.Fatalf("response %d status = %d, want 201 body=%s", i, rr.Code, rr.Body.String())
		}
	}
	if responses[0].Body.String() != responses[1].Body.String() {
		t.Fatalf("concurrent duplicate responses differ:\nfirst=%s\nsecond=%s", responses[0].Body.String(), responses[1].Body.String())
	}

	page := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge0087-batch-concurrent-1", "evt-edge0087-batch-concurrent-2"})
}

func edgeRoutePOSTWithIdempotencyKey(t *testing.T, handler http.Handler, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	return edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, edgeRouteTestAPIKey, edgeRouteTenant, path, body, key)
}

func edgeRoutePOSTAsTenantWithIdempotencyKey(t *testing.T, handler http.Handler, apiKey, tenantID, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	addEdgeRouteAuthFor(req, apiKey)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(key) != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func createEdgeRouteSessionForTenant(t *testing.T, handler http.Handler, apiKey, tenantID string) edgeSessionCreateResponseJSON {
	t.Helper()
	rr := edgeRoutePOSTAsTenantWithIdempotencyKey(t, handler, apiKey, tenantID, "/api/v1/edge/sessions", `{
		"agent_product":"claude-code",
		"agent_version":"1.2.3",
		"mode":"local-dev",
		"policy_snapshot":"snap-session-for-execution",
		"policy_mode":"observe"
	}`, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant session status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var session edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rr, &session)
	return session
}

func idempotentEdgeEventBody(sessionID, executionID, tenantID, eventID, command string) string {
	return mustJSON(idempotentEdgeEventMap(sessionID, executionID, tenantID, eventID, command))
}

func idempotentEdgeEventMap(sessionID, executionID, tenantID, eventID, command string) map[string]any {
	return map[string]any{
		"event_id":       eventID,
		"session_id":     sessionID,
		"execution_id":   executionID,
		"tenant_id":      tenantID,
		"ts":             "2026-05-02T12:00:00Z",
		"layer":          "hook",
		"kind":           "hook.pre_tool_use",
		"tool_name":      "Bash",
		"tool_use_id":    "toolu-edge0087-1",
		"action_name":    "bash.exec",
		"capability":     "exec.shell",
		"risk_tags":      []string{"exec"},
		"input_redacted": map[string]any{"command": command},
		"decision":       "ALLOW",
		"status":         "ok",
	}
}

func idempotentEdgeEventBatchBody(events ...map[string]any) string {
	return mustJSON(map[string]any{"events": events})
}

func mustJSON(data any) string {
	payload, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return string(payload)
}

type edgeCompleteIdempotencyFailureStore struct {
	edgecore.Store
	completeFailures int
	completeCalls    int
	releaseCalls     int
}

func (s *edgeCompleteIdempotencyFailureStore) CompleteIdempotency(ctx context.Context, req edgecore.EdgeIdempotencyRequest, response edgecore.EdgeIdempotencyResponse) (*edgecore.EdgeIdempotencyRecord, error) {
	s.completeCalls++
	if s.completeFailures > 0 {
		s.completeFailures--
		return nil, errInjectedCompleteIdempotency
	}
	return s.Store.CompleteIdempotency(ctx, req, response)
}

func (s *edgeCompleteIdempotencyFailureStore) ReleaseIdempotency(ctx context.Context, req edgecore.EdgeIdempotencyRequest) error {
	s.releaseCalls++
	return s.Store.ReleaseIdempotency(ctx, req)
}

func deleteGatewayEdgeIdempotencyKeys(t *testing.T, s *server) {
	t.Helper()
	keys, err := s.jobStore.Client().Keys(context.Background(), "edge:idempotency:*").Result()
	if err != nil {
		t.Fatalf("list edge idempotency keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("edge idempotency key count = %d, want 1 keys=%v", len(keys), keys)
	}
	if err := s.jobStore.Client().Del(context.Background(), keys...).Err(); err != nil {
		t.Fatalf("delete edge idempotency keys: %v", err)
	}
}
