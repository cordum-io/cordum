package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

func TestGatewayEdgeEventRoutesRegisteredAndTenantScoped(t *testing.T) {
	s, _ := newEdgeRouteTestServer(t)
	routes := make(map[string]routeInfo, len(s.Routes()))
	for _, route := range s.Routes() {
		routes[route.methodPathKey()] = route
	}

	for _, want := range edgeEventRouteExpectations() {
		got, ok := routes[want.method+" "+want.path]
		if !ok {
			t.Fatalf("missing Edge event route registration for %s %s", want.method, want.path)
		}
		if got.Auth == "public" {
			t.Fatalf("Edge event route %s %s was registered as public", want.method, want.path)
		}
		if got.Auth != "tenant" {
			t.Fatalf("Edge event route %s %s auth = %q, want tenant", want.method, want.path, got.Auth)
		}
	}
}

func TestGatewayEdgeEventRoutesRequireAuthTenantAndReachHandlers(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	missingAuth := httptest.NewRequest(http.MethodGet, "/api/v1/edge/executions/"+session.ExecutionID+"/events", nil)
	missingAuth.Header.Set("X-Tenant-ID", edgeRouteTenant)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, missingAuth)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401 body=%s", rr.Code, rr.Body.String())
	}

	missingTenant := httptest.NewRequest(http.MethodGet, "/api/v1/edge/executions/"+session.ExecutionID+"/events", nil)
	addEdgeRouteAuth(missingTenant)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, missingTenant)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing tenant status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}

	authorized := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events")
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized Edge execution events status = %d, want 200 body=%s", authorized.Code, authorized.Body.String())
	}
}

func TestGatewayEdgeEventWriteRejectsBadJSONTenantMismatchAndUnavailableStore(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	beforeBadJSON := edgeRedisKeySnapshot(t, s)
	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/edge/events", strings.NewReader(`{"event_id":`))
	addEdgeRouteAuth(badJSON)
	badJSON.Header.Set("X-Tenant-ID", edgeRouteTenant)
	badJSON.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, badJSON)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON status = %d, want 400 body=%s", rr.Code, rr.Body.String())
	}
	assertEdgeErrorShape(t, rr, http.StatusBadRequest, edgeErrCodeInvalidJSON)
	assertEdgeRedisKeysUnchanged(t, s, beforeBadJSON)

	mismatch := edgeRoutePOST(t, handler, "/api/v1/edge/events", edgeEventWriteBody(session.SessionID, session.ExecutionID, edgeRouteOtherTenant, "evt-tenant-mismatch"))
	if mismatch.Code != http.StatusForbidden {
		t.Fatalf("body tenant mismatch status = %d, want 403 body=%s", mismatch.Code, mismatch.Body.String())
	}

	s.edgeStore = nil
	unavailableWrite := edgeRoutePOST(t, handler, "/api/v1/edge/events", edgeEventWriteBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "evt-unavailable-write"))
	assertEdgeErrorShape(t, unavailableWrite, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable)

	unavailableRead := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events")
	assertEdgeErrorShape(t, unavailableRead, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable)
}

func TestGatewayEdgeEventRoutesDenyCrossTenantWithoutLeakingIDs(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	crossTenantWrite := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/edge/events",
		strings.NewReader(edgeEventWriteBody(session.SessionID, session.ExecutionID, edgeRouteOtherTenant, "evt-cross-tenant")),
	)
	addEdgeRouteAuthFor(crossTenantWrite, edgeRouteOtherAPIKey)
	crossTenantWrite.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
	crossTenantWrite.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, crossTenantWrite)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant write status = %d, want 404 body=%s", rr.Code, rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), session.SessionID, session.ExecutionID, edgeRouteTenant)

	for _, path := range []string{
		"/api/v1/edge/sessions/" + session.SessionID + "/events",
		"/api/v1/edge/executions/" + session.ExecutionID + "/events",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		addEdgeRouteAuthFor(req, edgeRouteOtherAPIKey)
		req.Header.Set("X-Tenant-ID", edgeRouteOtherTenant)
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("cross-tenant read %s status = %d, want 404 body=%s", path, rr.Code, rr.Body.String())
		}
		assertBodyOmits(t, rr.Body.String(), session.SessionID, session.ExecutionID, edgeRouteTenant)
	}
}

func TestGatewayEdgeEventSingleWriteRedactsHashesAndPreservesSeq(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	rec := &recordingEdgeRecorder{}
	s.edgeRecorder = rec
	session := createEdgeRouteSession(t, handler)

	rawSecretCommand := "Authorization: Bearer edge006-secret-token"
	first := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-single-1",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:01:00Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"tool_name":"Bash",
		"tool_use_id":"toolu-edge006-1",
		"action_name":"bash.exec",
		"capability":"exec.shell",
		"risk_tags":["exec"],
		"input_redacted":{"command":"`+rawSecretCommand+`"},
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("single event create status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	assertBodyOmits(t, first.Body.String(), rawSecretCommand)
	if !bodyHasRedactionMarker(first.Body.String()) {
		t.Fatalf("single event response did not include redaction marker: %s", first.Body.String())
	}
	var created edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, first, &created)
	if created.Seq != 1 {
		t.Fatalf("first event seq = %d, want assigned seq 1", created.Seq)
	}
	if created.InputHash == "" || !strings.HasPrefix(created.InputHash, "sha256:") {
		t.Fatalf("first event input_hash = %q, want stable sha256 hash", created.InputHash)
	}
	if created.InputRedacted["command"] == rawSecretCommand {
		t.Fatalf("stored event input_redacted kept raw command: %#v", created.InputRedacted)
	}
	if got := rec.Redacted(); len(got) != 1 || got[0] != "applied" {
		t.Fatalf("redaction metric calls = %#v, want [applied]", got)
	}

	second := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-single-2",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"seq":2,
		"ts":"2026-05-01T12:01:01Z",
		"layer":"hook",
		"kind":"hook.policy_decision",
		"input_redacted":{"summary":"policy decision"},
		"decision":"DENY",
		"status":"blocked"
	}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("second event create status = %d, want 201 body=%s", second.Code, second.Body.String())
	}
	var createdSecond edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, second, &createdSecond)
	if createdSecond.Seq != 2 {
		t.Fatalf("second event seq = %d, want explicit seq 2 preserved", createdSecond.Seq)
	}
}

func TestRecordEdgeEventRedactionFailedRecordsFailedOutcome(t *testing.T) {
	rec := &recordingEdgeRecorder{}

	recordEdgeEventRedactionFailed(rec, "gateway.edge_event_input", "redactor_error")

	if got := rec.Redacted(); len(got) != 1 || got[0] != "failed" {
		t.Fatalf("redaction outcomes = %v, want [failed]", got)
	}
	if got := rec.RedactionFailures(); len(got) != 1 || got[0] != "gateway.edge_event_input:redactor_error" {
		t.Fatalf("redaction failures = %v, want gateway.edge_event_input:redactor_error", got)
	}
}

func TestGatewayEdgeEventSingleWriteStreamsPersistedEdgeEnvelope(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}

	rawSecretCommand := "Authorization: Bearer edge007-stream-secret"
	created := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge007-write-stream",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:07:00Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"tool_name":"Bash",
		"input_redacted":{"command":"`+rawSecretCommand+`"},
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("single event create status = %d, want 201 body=%s", created.Code, created.Body.String())
	}
	var persisted edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, created, &persisted)

	streamed := readGatewayEdgeStreamEvent(t, streamQueue, "persisted single edge.event")
	if streamed.tenant != edgeRouteTenant {
		t.Fatalf("stream tenant = %q, want %q", streamed.tenant, edgeRouteTenant)
	}
	if streamed.jobID != "" {
		t.Fatalf("stream jobID = %q, want empty for generic edge.event", streamed.jobID)
	}
	body := string(streamed.data)
	if strings.Contains(body, rawSecretCommand) || strings.Contains(body, "edge007-stream-secret") {
		t.Fatalf("streamed edge.event leaked raw command: %s", body)
	}
	for _, forbidden := range []string{"jobProgress", "jobRequest", "jobResult", "heartbeat"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("streamed edge.event contains BusPacket field %q: %s", forbidden, body)
		}
	}
	var envelope struct {
		Type        string                    `json:"type"`
		TenantID    string                    `json:"tenant_id"`
		SessionID   string                    `json:"session_id"`
		ExecutionID string                    `json:"execution_id"`
		Event       edgecore.AgentActionEvent `json:"event"`
	}
	if err := json.Unmarshal(streamed.data, &envelope); err != nil {
		t.Fatalf("decode streamed edge.event: %v body=%s", err, body)
	}
	if envelope.Type != "edge.event" {
		t.Fatalf("stream type = %q, want edge.event", envelope.Type)
	}
	if envelope.TenantID != persisted.TenantID || envelope.SessionID != persisted.SessionID || envelope.ExecutionID != persisted.ExecutionID {
		t.Fatalf("stream envelope ids = %q/%q/%q, want persisted %q/%q/%q",
			envelope.TenantID, envelope.SessionID, envelope.ExecutionID,
			persisted.TenantID, persisted.SessionID, persisted.ExecutionID)
	}
	if envelope.Event.EventID != persisted.EventID || envelope.Event.Seq != persisted.Seq || envelope.Event.InputHash != persisted.InputHash {
		t.Fatalf("stream payload = %#v, want persisted event id/seq/hash %q/%d/%q",
			envelope.Event, persisted.EventID, persisted.Seq, persisted.InputHash)
	}
}

func TestGatewayEdgeEventBatchWriteStreamsPersistedEventsInOrder(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}

	batch := edgeRoutePOST(t, handler, "/api/v1/edge/events/batch", edgeEventBatchBody(
		edgeEventJSON("evt-edge007-batch-stream-1", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:07:10Z", "hook.pre_tool_use", "ALLOW", "ok"),
		edgeEventJSON("evt-edge007-batch-stream-2", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:07:11Z", "hook.policy_decision", "DENY", "blocked"),
	))
	if batch.Code != http.StatusCreated {
		t.Fatalf("batch create status = %d, want 201 body=%s", batch.Code, batch.Body.String())
	}
	var persisted edgeEventBatchResponseJSON
	decodeEdgeRouteJSON(t, batch, &persisted)
	if len(persisted.Items) != 2 {
		t.Fatalf("batch response items = %d, want 2", len(persisted.Items))
	}

	for i, want := range persisted.Items {
		streamed := readGatewayEdgeStreamEvent(t, streamQueue, want.EventID)
		if streamed.tenant != edgeRouteTenant {
			t.Fatalf("stream[%d] tenant = %q, want %q", i, streamed.tenant, edgeRouteTenant)
		}
		if streamed.jobID != "" {
			t.Fatalf("stream[%d] jobID = %q, want empty", i, streamed.jobID)
		}
		var envelope struct {
			Type  string                    `json:"type"`
			Event edgecore.AgentActionEvent `json:"event"`
		}
		if err := json.Unmarshal(streamed.data, &envelope); err != nil {
			t.Fatalf("decode streamed batch event %d: %v body=%s", i, err, string(streamed.data))
		}
		if envelope.Type != "edge.event" || envelope.Event.EventID != want.EventID || envelope.Event.Seq != want.Seq {
			t.Fatalf("stream[%d] = type %q event %q seq %d, want edge.event %q seq %d",
				i, envelope.Type, envelope.Event.EventID, envelope.Event.Seq, want.EventID, want.Seq)
		}
	}
	assertNoGatewayEdgeStreamEvent(t, streamQueue, "batch should stream exactly persisted events")
}

func TestGatewayEdgeEventWriteDoesNotStreamWhenPersistenceFails(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}

	missingParent := edgeRoutePOST(t, handler, "/api/v1/edge/events", edgeEventWriteBody("missing-session", "missing-execution", edgeRouteTenant, "evt-edge007-no-phantom"))
	if missingParent.Code != http.StatusNotFound {
		t.Fatalf("missing parent create status = %d, want 404 body=%s", missingParent.Code, missingParent.Body.String())
	}
	assertNoGatewayEdgeStreamEvent(t, streamQueue, "failed persistence must not stream phantom edge.event")
}

func TestGatewayEdgeEventWriteRejectsOversizeInlineInputsAndRawPayloads(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	beforeRejects := edgeRedisKeySnapshot(t, s)

	oversizeSentinel := "edge028-inline-oversize-secret"
	hugeInput := oversizeSentinel + strings.Repeat("x", edgecore.MaxInputRedactedBytes+1024)
	oversize := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-oversize",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:02:00Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"input_redacted":{"command":"`+hugeInput+`"},
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if oversize.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize inline input status = %d, want 413 body=%s", oversize.Code, oversize.Body.String())
	}
	assertBodyOmits(t, oversize.Body.String(), oversizeSentinel)
	assertEdgeRedisKeysUnchanged(t, s, beforeRejects)
	if events := readEdgeEventsFromStore(t, s, session.ExecutionID); len(events.Items) != 0 {
		t.Fatalf("oversize inline input persisted events = %#v, want none", events.Items)
	}

	rawPayloadSentinel := "edge028-raw-tool-secret"
	rawPayload := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-raw-payload",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:02:01Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"tool_input":{"raw":"`+rawPayloadSentinel+strings.Repeat("x", 4096)+`"},
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if rawPayload.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large raw tool_input status = %d, want 413 body=%s", rawPayload.Code, rawPayload.Body.String())
	}
	if !strings.Contains(rawPayload.Body.String(), "artifact_ptrs") {
		t.Fatalf("large raw payload response = %s, want artifact_ptrs guidance", rawPayload.Body.String())
	}
	assertBodyOmits(t, rawPayload.Body.String(), rawPayloadSentinel)
	assertEdgeRedisKeysUnchanged(t, s, beforeRejects)
	if events := readEdgeEventsFromStore(t, s, session.ExecutionID); len(events.Items) != 0 {
		t.Fatalf("large raw payload persisted events = %#v, want none", events.Items)
	}
}

func TestGatewayEdgeEventWriteRejectsBodyOverMaxBytesWithoutOrphanKeys(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	t.Setenv(envGatewayMaxJSONBodyBytes, "256")
	beforeOversizeBody := edgeRedisKeySnapshot(t, s)

	oversizeBodySentinel := "edge028-max-body-secret"
	body := `{
		"event_id":"evt-edge028-max-body",
		"session_id":"` + session.SessionID + `",
		"execution_id":"` + session.ExecutionID + `",
		"tenant_id":"` + edgeRouteTenant + `",
		"ts":"2026-05-01T12:02:00Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"input_redacted":{"command":"` + oversizeBodySentinel + strings.Repeat("x", 512) + `"},
		"decision":"ALLOW",
		"status":"ok"
	}`
	if len(body) <= 256 {
		t.Fatalf("oversize fixture length = %d, want > 256", len(body))
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/events", strings.NewReader(body))
	addEdgeRouteAuth(req)
	req.Header.Set("X-Tenant-ID", edgeRouteTenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("body over max bytes status = %d, want 403 tier-limit body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "tier_limit_exceeded") || !strings.Contains(rr.Body.String(), "max_body_bytes") {
		t.Fatalf("body over max bytes response = %s, want tier_limit_exceeded/max_body_bytes", rr.Body.String())
	}
	assertBodyOmits(t, rr.Body.String(), oversizeBodySentinel)
	assertEdgeRedisKeysUnchanged(t, s, beforeOversizeBody)
	if events := readEdgeEventsFromStore(t, s, session.ExecutionID); len(events.Items) != 0 {
		t.Fatalf("body over max bytes persisted events = %#v, want none", events.Items)
	}
}

func TestGatewayEdgeEventWriteRejectsMismatchedArtifactPointers(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	mismatch := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-artifact-mismatch",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:02:02Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"artifact_ptrs":[{
			"artifact_type":"edge.tool_input",
			"session_id":"`+session.SessionID+`",
			"execution_id":"`+session.ExecutionID+`",
			"event_id":"evt-edge006-artifact-mismatch",
			"tenant_id":"`+edgeRouteOtherTenant+`",
			"retention_class":"short",
			"redaction_level":"standard",
			"sha256":"sha256:abcdef",
			"uri":"edge-artifact://tenant-b/secret-tool-input",
			"created_at":"2026-05-01T12:02:02Z"
		}],
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("mismatched artifact pointer status = %d, want 400 body=%s", mismatch.Code, mismatch.Body.String())
	}
	assertBodyOmits(t, mismatch.Body.String(), edgeRouteOtherTenant, "tenant-b/secret-tool-input")
	afterMismatch := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, afterMismatch.Items, []string{})
}

func TestGatewayEdgeEventWriteRejectsSecretBearingArtifactPointerURI(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	secretURI := "https://storage.example.com/evidence/tool-input.json?X-Amz-Signature=secret-sig&token=ghp_secretTokenValue123"
	rejected := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-artifact-secret-uri",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:02:03Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"artifact_ptrs":[{
			"artifact_type":"edge.tool_input",
			"session_id":"`+session.SessionID+`",
			"execution_id":"`+session.ExecutionID+`",
			"event_id":"evt-edge006-artifact-secret-uri",
			"tenant_id":"`+edgeRouteTenant+`",
			"retention_class":"short",
			"redaction_level":"standard",
			"sha256":"sha256:abcdef",
			"uri":"`+secretURI+`",
			"created_at":"2026-05-01T12:02:03Z"
		}],
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("secret-bearing artifact pointer status = %d, want 400 body=%s", rejected.Code, rejected.Body.String())
	}
	assertBodyOmits(t, rejected.Body.String(), secretURI, "secret-sig", "ghp_secretTokenValue123")
	afterRejected := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, afterRejected.Items, []string{})

	safeURI := "artifact://edge/evt-edge006-artifact-safe-uri/tool-input"
	accepted := edgeRoutePOST(t, handler, "/api/v1/edge/events", `{
		"event_id":"evt-edge006-artifact-safe-uri",
		"session_id":"`+session.SessionID+`",
		"execution_id":"`+session.ExecutionID+`",
		"tenant_id":"`+edgeRouteTenant+`",
		"ts":"2026-05-01T12:02:04Z",
		"layer":"hook",
		"kind":"hook.pre_tool_use",
		"artifact_ptrs":[{
			"artifact_type":"edge.tool_input",
			"session_id":"`+session.SessionID+`",
			"execution_id":"`+session.ExecutionID+`",
			"event_id":"evt-edge006-artifact-safe-uri",
			"tenant_id":"`+edgeRouteTenant+`",
			"retention_class":"short",
			"redaction_level":"standard",
			"sha256":"sha256:abcdef",
			"uri":"`+safeURI+`",
			"created_at":"2026-05-01T12:02:04Z"
		}],
		"decision":"ALLOW",
		"status":"ok"
	}`)
	if accepted.Code != http.StatusCreated {
		t.Fatalf("safe artifact pointer status = %d, want 201 body=%s", accepted.Code, accepted.Body.String())
	}
	var created edgecore.AgentActionEvent
	decodeEdgeRouteJSON(t, accepted, &created)
	if len(created.ArtifactPointers) != 1 || created.ArtifactPointers[0].URI != safeURI {
		t.Fatalf("created artifact pointers = %#v, want sanitized internal uri %q", created.ArtifactPointers, safeURI)
	}
}

func TestGatewayEdgeEventWriteRejectsInvalidRequiredFields(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "missing session_id",
			body: `{"event_id":"evt-missing-session","execution_id":"` + session.ExecutionID + `","tenant_id":"` + edgeRouteTenant + `","ts":"2026-05-01T12:03:00Z","layer":"hook","kind":"hook.pre_tool_use","decision":"ALLOW","status":"ok"}`,
		},
		{
			name: "missing execution_id",
			body: `{"event_id":"evt-missing-execution","session_id":"` + session.SessionID + `","tenant_id":"` + edgeRouteTenant + `","ts":"2026-05-01T12:03:00Z","layer":"hook","kind":"hook.pre_tool_use","decision":"ALLOW","status":"ok"}`,
		},
		{
			name: "missing event_id",
			body: `{"session_id":"` + session.SessionID + `","execution_id":"` + session.ExecutionID + `","tenant_id":"` + edgeRouteTenant + `","ts":"2026-05-01T12:03:00Z","layer":"hook","kind":"hook.pre_tool_use","decision":"ALLOW","status":"ok"}`,
		},
		{
			name: "missing kind",
			body: `{"event_id":"evt-missing-kind","session_id":"` + session.SessionID + `","execution_id":"` + session.ExecutionID + `","tenant_id":"` + edgeRouteTenant + `","ts":"2026-05-01T12:03:00Z","layer":"hook","decision":"ALLOW","status":"ok"}`,
		},
		{
			name: "missing timestamp",
			body: `{"event_id":"evt-missing-ts","session_id":"` + session.SessionID + `","execution_id":"` + session.ExecutionID + `","tenant_id":"` + edgeRouteTenant + `","layer":"hook","kind":"hook.pre_tool_use","decision":"ALLOW","status":"ok"}`,
		},
		{
			name: "invalid decision",
			body: `{"event_id":"evt-invalid-decision","session_id":"` + session.SessionID + `","execution_id":"` + session.ExecutionID + `","tenant_id":"` + edgeRouteTenant + `","ts":"2026-05-01T12:03:00Z","layer":"hook","kind":"hook.pre_tool_use","decision":"MAYBE","status":"ok"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := edgeRoutePOST(t, handler, "/api/v1/edge/events", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s status = %d, want 400 body=%s", tc.name, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestGatewayEdgeEventBatchPreservesOrderAndPrevalidatesBeforeAppend(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	batch := edgeRoutePOST(t, handler, "/api/v1/edge/events/batch", edgeEventBatchBody(
		edgeEventJSON("evt-edge006-batch-1", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:04:00Z", "hook.pre_tool_use", "ALLOW", "ok"),
		edgeEventJSON("evt-edge006-batch-2", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:04:01Z", "hook.policy_decision", "DENY", "blocked"),
	))
	if batch.Code != http.StatusCreated {
		t.Fatalf("batch create status = %d, want 201 body=%s", batch.Code, batch.Body.String())
	}
	var batchResp edgeEventBatchResponseJSON
	decodeEdgeRouteJSON(t, batch, &batchResp)
	if len(batchResp.Items) != 2 {
		t.Fatalf("batch response items len = %d, want 2: %#v", len(batchResp.Items), batchResp.Items)
	}
	if batchResp.Items[0].EventID != "evt-edge006-batch-1" || batchResp.Items[0].Seq != 1 {
		t.Fatalf("batch item 0 = %#v, want event 1 seq 1", batchResp.Items[0])
	}
	if batchResp.Items[1].EventID != "evt-edge006-batch-2" || batchResp.Items[1].Seq != 2 {
		t.Fatalf("batch item 1 = %#v, want event 2 seq 2", batchResp.Items[1])
	}

	invalidLater := edgeRoutePOST(t, handler, "/api/v1/edge/events/batch", edgeEventBatchBody(
		edgeEventJSON("evt-edge006-batch-should-not-append", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:04:02Z", "hook.post_tool_use", "ALLOW", "ok"),
		edgeEventJSON("evt-edge006-batch-invalid-later", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:04:03Z", "", "ALLOW", "ok"),
	))
	if invalidLater.Code != http.StatusBadRequest {
		t.Fatalf("invalid later batch status = %d, want 400 body=%s", invalidLater.Code, invalidLater.Body.String())
	}
	afterInvalidPage := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, afterInvalidPage.Items, []string{"evt-edge006-batch-1", "evt-edge006-batch-2"})

	mixedTenant := edgeRoutePOST(t, handler, "/api/v1/edge/events/batch", edgeEventBatchBody(
		edgeEventJSON("evt-edge006-batch-mixed-first", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:04:04Z", "hook.post_tool_use", "ALLOW", "ok"),
		edgeEventJSON("evt-edge006-batch-mixed-second", session.SessionID, session.ExecutionID, edgeRouteOtherTenant, "", "2026-05-01T12:04:05Z", "hook.post_tool_use", "ALLOW", "ok"),
	))
	if mixedTenant.Code != http.StatusForbidden {
		t.Fatalf("mixed-tenant batch status = %d, want 403 body=%s", mixedTenant.Code, mixedTenant.Body.String())
	}
	afterMixedTenantPage := readEdgeEventsFromStore(t, s, session.ExecutionID)
	assertEdgeEventIDs(t, afterMixedTenantPage.Items, []string{"evt-edge006-batch-1", "evt-edge006-batch-2"})
}

func TestGatewayEdgeEventReadsSupportPaginationFiltersTimeAndScopes(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)
	execution2 := createEdgeRouteExecution(t, handler, session.SessionID)

	for _, body := range []string{
		edgeEventJSON("evt-edge006-read-1", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:05:00Z", "hook.pre_tool_use", "ALLOW", "ok"),
		edgeEventJSON("evt-edge006-read-2", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:05:10Z", "hook.policy_decision", "DENY", "blocked"),
		edgeEventJSON("evt-edge006-read-3", session.SessionID, execution2.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:05:20Z", "approval.requested", "REQUIRE_APPROVAL", "blocked"),
	} {
		rr := edgeRoutePOST(t, handler, "/api/v1/edge/events", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed event status = %d, want 201 body=%s", rr.Code, rr.Body.String())
		}
	}

	page1 := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events?limit=1")
	if page1.Code != http.StatusOK {
		t.Fatalf("execution events page1 status = %d, want 200 body=%s", page1.Code, page1.Body.String())
	}
	var page edgeEventPageResponseJSON
	decodeEdgeRouteJSON(t, page1, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge006-read-1"})
	if page.NextCursor == "" {
		t.Fatalf("execution events page1 next_cursor empty, want continuation")
	}

	page2 := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events?limit=1&cursor="+page.NextCursor)
	if page2.Code != http.StatusOK {
		t.Fatalf("execution events page2 status = %d, want 200 body=%s", page2.Code, page2.Body.String())
	}
	decodeEdgeRouteJSON(t, page2, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge006-read-2"})
	if page.NextCursor != "" {
		t.Fatalf("execution events page2 next_cursor = %q, want empty", page.NextCursor)
	}

	kindFiltered := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events?kind=hook.policy_decision&limit=10")
	if kindFiltered.Code != http.StatusOK {
		t.Fatalf("kind filter status = %d, want 200 body=%s", kindFiltered.Code, kindFiltered.Body.String())
	}
	decodeEdgeRouteJSON(t, kindFiltered, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge006-read-2"})

	decisionFiltered := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events?decision=DENY&limit=10")
	if decisionFiltered.Code != http.StatusOK {
		t.Fatalf("decision filter status = %d, want 200 body=%s", decisionFiltered.Code, decisionFiltered.Body.String())
	}
	decodeEdgeRouteJSON(t, decisionFiltered, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge006-read-2"})

	timeFiltered := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/events?since=2026-05-01T12:05:05Z&until=2026-05-01T12:05:25Z&limit=10")
	if timeFiltered.Code != http.StatusOK {
		t.Fatalf("time filter status = %d, want 200 body=%s", timeFiltered.Code, timeFiltered.Body.String())
	}
	decodeEdgeRouteJSON(t, timeFiltered, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge006-read-2", "evt-edge006-read-3"})

	sessionScoped := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/events?limit=10")
	if sessionScoped.Code != http.StatusOK {
		t.Fatalf("session events status = %d, want 200 body=%s", sessionScoped.Code, sessionScoped.Body.String())
	}
	decodeEdgeRouteJSON(t, sessionScoped, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge006-read-1", "evt-edge006-read-2", "evt-edge006-read-3"})
}

func edgeEventRouteExpectations() []edgeRouteExpectation {
	return []edgeRouteExpectation{
		{method: http.MethodPost, path: "/api/v1/edge/events"},
		{method: http.MethodPost, path: "/api/v1/edge/events/batch"},
		{method: http.MethodGet, path: "/api/v1/edge/sessions/{session_id}/events"},
		{method: http.MethodGet, path: "/api/v1/edge/executions/{execution_id}/events"},
	}
}

func edgeEventWriteBody(sessionID, executionID, tenantID, eventID string) string {
	return edgeEventJSON(eventID, sessionID, executionID, tenantID, "", "2026-05-01T12:00:00Z", "hook.pre_tool_use", "ALLOW", "ok")
}

func edgeEventJSON(eventID, sessionID, executionID, tenantID, seq, timestamp, kind, decision, status string) string {
	seqField := ""
	if strings.TrimSpace(seq) != "" {
		seqField = `"seq":` + seq + `,`
	}
	return `{
		"event_id":"` + eventID + `",
		"session_id":"` + sessionID + `",
		"execution_id":"` + executionID + `",
		"tenant_id":"` + tenantID + `",
		` + seqField + `
		"ts":"` + timestamp + `",
		"layer":"hook",
		"kind":"` + kind + `",
		"tool_name":"Bash",
		"input_redacted":{"command":"npm test"},
		"decision":"` + decision + `",
		"status":"` + status + `"
	}`
}

func edgeEventBatchBody(events ...string) string {
	return `{"events":[` + strings.Join(events, ",") + `]}`
}

type edgeEventBatchResponseJSON struct {
	Items []edgecore.AgentActionEvent `json:"items"`
}

type edgeEventPageResponseJSON struct {
	Items      []edgecore.AgentActionEvent `json:"items"`
	NextCursor string                      `json:"next_cursor"`
}

func createEdgeRouteExecution(t *testing.T, handler http.Handler, sessionID string) edgecore.AgentExecution {
	t.Helper()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/executions", `{"session_id":"`+sessionID+`","adapter":"claude-code-hook","mode":"local-dev"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create execution status = %d, want 201 body=%s", rr.Code, rr.Body.String())
	}
	var execution edgecore.AgentExecution
	decodeEdgeRouteJSON(t, rr, &execution)
	return execution
}

func assertEdgeEventIDs(t *testing.T, got []edgecore.AgentActionEvent, want []string) {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, event := range got {
		ids = append(ids, event.EventID)
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("event ids = %#v, want %#v", ids, want)
	}
}

func readEdgeEventsFromStore(t *testing.T, s *server, executionID string) edgeEventPageResponseJSON {
	t.Helper()
	page, err := s.edgeStore.ListEvents(context.Background(), edgecore.ListEventsQuery{
		TenantID:    edgeRouteTenant,
		ExecutionID: executionID,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListEvents(%s): %v", executionID, err)
	}
	return edgeEventPageResponseJSON{Items: page.Items, NextCursor: page.NextCursor}
}

func edgeRedisKeySnapshot(t *testing.T, s *server) string {
	t.Helper()
	if s == nil || s.jobStore == nil || s.jobStore.Client() == nil {
		t.Fatal("edge Redis key snapshot requires gateway Redis job store")
	}
	keys, err := s.jobStore.Client().Keys(context.Background(), "edge:*").Result()
	if err != nil {
		t.Fatalf("snapshot edge Redis keys: %v", err)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

func assertEdgeRedisKeysUnchanged(t *testing.T, s *server, before string) {
	t.Helper()
	after := edgeRedisKeySnapshot(t, s)
	if after != before {
		t.Fatalf("edge Redis keys changed after rejected request\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func drainGatewayEdgeStreamQueue(ch <-chan wsEvent) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
