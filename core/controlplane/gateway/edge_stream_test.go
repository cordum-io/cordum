package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestMarshalEdgeEventEnvelopeSerializesDedicatedShape(t *testing.T) {
	event := validGatewayEdgeStreamEvent()

	data, err := marshalEdgeEventEnvelope(&event)
	if err != nil {
		t.Fatalf("marshalEdgeEventEnvelope() error = %v", err)
	}
	body := string(data)
	for _, forbidden := range []string{"jobProgress", "jobRequest", "jobResult", "heartbeat"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("edge.event envelope body contains BusPacket field %q: %s", forbidden, body)
		}
	}

	var got struct {
		Type        string                    `json:"type"`
		TenantID    string                    `json:"tenant_id"`
		SessionID   string                    `json:"session_id"`
		ExecutionID string                    `json:"execution_id"`
		Event       edgecore.AgentActionEvent `json:"event"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal edge.event envelope: %v\nbody=%s", err, body)
	}
	if got.Type != "edge.event" {
		t.Fatalf("type = %q, want edge.event", got.Type)
	}
	if got.TenantID != event.TenantID || got.SessionID != event.SessionID || got.ExecutionID != event.ExecutionID {
		t.Fatalf("envelope ids = tenant:%q session:%q execution:%q, want tenant:%q session:%q execution:%q",
			got.TenantID, got.SessionID, got.ExecutionID, event.TenantID, event.SessionID, event.ExecutionID)
	}
	if got.Event.EventID != event.EventID || got.Event.TenantID != event.TenantID ||
		got.Event.SessionID != event.SessionID || got.Event.ExecutionID != event.ExecutionID {
		t.Fatalf("event payload identity = %#v, want event %q/%q/%q/%q",
			got.Event, event.TenantID, event.SessionID, event.ExecutionID, event.EventID)
	}
	if got.Event.Kind != edgecore.EventKindHookPreToolUse || got.Event.ToolName != "Bash" {
		t.Fatalf("event payload kind/tool = %q/%q, want hook.pre_tool_use/Bash", got.Event.Kind, got.Event.ToolName)
	}
	if got.Event.InputRedacted["command"] != "npm test" {
		t.Fatalf("event payload input_redacted = %#v, want command npm test", got.Event.InputRedacted)
	}
}

func TestMarshalEdgeEventEnvelopeRejectsNilAndInvalidEventsSafely(t *testing.T) {
	rawSecret := "Authorization: Bearer edge007-raw-secret"

	for _, tc := range []struct {
		name  string
		event *edgecore.AgentActionEvent
	}{
		{name: "nil", event: nil},
		{name: "zero", event: &edgecore.AgentActionEvent{}},
		{name: "missing tenant with raw-looking input", event: func() *edgecore.AgentActionEvent {
			event := validGatewayEdgeStreamEvent()
			event.TenantID = ""
			event.InputRedacted = map[string]any{"command": rawSecret}
			return &event
		}()},
		{name: "blank execution", event: func() *edgecore.AgentActionEvent {
			event := validGatewayEdgeStreamEvent()
			event.ExecutionID = " "
			return &event
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := marshalEdgeEventEnvelope(tc.event)
			if err == nil {
				t.Fatalf("marshalEdgeEventEnvelope() error = nil, want invalid event error; data=%s", string(data))
			}
			if len(data) != 0 {
				t.Fatalf("marshalEdgeEventEnvelope() data = %s, want no bytes on invalid event", string(data))
			}
			if strings.Contains(err.Error(), rawSecret) || strings.Contains(err.Error(), "edge007-raw-secret") {
				t.Fatalf("marshalEdgeEventEnvelope() error leaked raw input: %v", err)
			}
		})
	}
}

func TestEnqueueEdgeEventReturnsQueueDropSignalWhenWSBufferFull(t *testing.T) {
	s := &server{
		eventsCh: make(chan wsEvent, 1),
	}
	first := validGatewayEdgeStreamEvent()
	queued, err := s.enqueueEdgeEvent(first)
	if err != nil {
		t.Fatalf("enqueueEdgeEvent(first) error = %v", err)
	}
	if !queued {
		t.Fatal("enqueueEdgeEvent(first) queued = false, want true")
	}
	second := validGatewayEdgeStreamEvent()
	second.EventID = "evt-edge007-dropped"
	queued, err = s.enqueueEdgeEvent(second)
	if err != nil {
		t.Fatalf("enqueueEdgeEvent(second) error = %v", err)
	}
	if queued {
		t.Fatal("enqueueEdgeEvent(second) queued = true for full websocket queue, want false")
	}
}

func TestEdgeEventStreamTenantFilteringAndBusPacketRegression(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})
	s.eventsCh = make(chan wsEvent, 16)
	s.clients = make(map[*websocket.Conn]*wsClient)
	s.wsClientBufSz = 8
	if err := s.startBusTaps(); err != nil {
		t.Fatalf("startBusTaps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})

	tenantA := registerGatewayEdgeStreamClient(t, s, "tenant-edge-a", false)
	tenantB := registerGatewayEdgeStreamClient(t, s, "tenant-edge-b", false)
	crossTenant := registerGatewayEdgeStreamClient(t, s, "", true)

	eventA := validGatewayEdgeStreamEvent()
	if queued, err := s.enqueueEdgeEvent(eventA); err != nil {
		t.Fatalf("enqueueEdgeEvent(tenant A): %v", err)
	} else if !queued {
		t.Fatal("enqueueEdgeEvent(tenant A) queued = false, want true")
	}
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, tenantA, "tenant A edge.event"), "evt-edge007-stream-1", "edge.event")
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant edge.event A"), "evt-edge007-stream-1", "edge.event")
	assertNoGatewayEdgeStreamEvent(t, tenantB, "tenant B must not receive tenant A edge.event")

	eventB := validGatewayEdgeStreamEvent()
	eventB.EventID = "evt-edge007-stream-b"
	eventB.TenantID = "tenant-edge-b"
	if queued, err := s.enqueueEdgeEvent(eventB); err != nil {
		t.Fatalf("enqueueEdgeEvent(tenant B): %v", err)
	} else if !queued {
		t.Fatal("enqueueEdgeEvent(tenant B) queued = false, want true")
	}
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, tenantB, "tenant B edge.event"), "evt-edge007-stream-b", "edge.event")
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant edge.event B"), "evt-edge007-stream-b", "edge.event")
	assertNoGatewayEdgeStreamEvent(t, tenantA, "tenant A must not receive tenant B edge.event")

	missingTenant := validGatewayEdgeStreamEvent()
	missingTenant.EventID = "evt-edge007-missing-tenant"
	missingTenant.TenantID = " "
	if _, err := s.enqueueEdgeEvent(missingTenant); err == nil {
		t.Fatal("enqueueEdgeEvent(missing tenant) error = nil, want fail-closed validation error")
	}
	assertNoGatewayEdgeStreamEvent(t, tenantA, "tenant A must not receive missing-tenant edge.event")
	assertNoGatewayEdgeStreamEvent(t, tenantB, "tenant B must not receive missing-tenant edge.event")
	assertNoGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant client must not receive missing-tenant edge.event")

	const progressJobID = "job-edge007-progress"
	progressJob := &pb.JobRequest{JobId: progressJobID, Topic: "job.default", TenantId: "tenant-edge-a"}
	if err := s.jobStore.SetJobMeta(context.Background(), progressJob); err != nil {
		t.Fatalf("SetJobMeta progress job: %v", err)
	}
	s.enqueueBusPacket(&pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{
		JobId:   progressJobID,
		Percent: 42,
		Message: "still a BusPacket",
	}}})
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, tenantA, "tenant A job.progress"), progressJobID, "jobProgress")
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant job.progress"), progressJobID, "jobProgress")
	assertNoGatewayEdgeStreamEvent(t, tenantB, "tenant B must not receive tenant A job.progress BusPacket")
}

func TestMixedStreamKeepsBusPacketShapeAndDoesNotFloodJobStream(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})
	s.eventsCh = make(chan wsEvent, 16)
	s.clients = make(map[*websocket.Conn]*wsClient)
	s.wsClientBufSz = 8
	if err := s.startBusTaps(); err != nil {
		t.Fatalf("startBusTaps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})

	const progressJobID = "job-edge007-mixed"
	progressJob := &pb.JobRequest{JobId: progressJobID, Topic: "job.default", TenantId: "tenant-edge-a"}
	if err := s.jobStore.SetJobMeta(context.Background(), progressJob); err != nil {
		t.Fatalf("SetJobMeta progress job: %v", err)
	}

	globalTenantA := registerGatewayEdgeStreamClient(t, s, "tenant-edge-a", false)
	crossTenant := registerGatewayEdgeStreamClient(t, s, "", true)
	jobStream := registerGatewayEdgeStreamJobClient(t, s, "tenant-edge-a", progressJobID)

	progressPacket := &pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{
		JobId:   progressJobID,
		Percent: 64,
		Message: "unchanged progress packet",
	}}}
	wantProgress, err := protojson.Marshal(progressPacket)
	if err != nil {
		t.Fatalf("protojson marshal progress packet: %v", err)
	}
	s.enqueueBusPacket(progressPacket)
	assertGatewayEdgeStreamRawJSON(t, readGatewayEdgeStreamEvent(t, globalTenantA, "global tenant A job.progress").data, wantProgress)
	assertGatewayEdgeStreamRawJSON(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant job.progress").data, wantProgress)
	assertGatewayEdgeStreamRawJSON(t, readGatewayEdgeStreamEvent(t, jobStream, "per-job job.progress").data, wantProgress)

	edgeEvent := validGatewayEdgeStreamEvent()
	edgeEvent.EventID = "evt-edge007-mixed"
	if queued, err := s.enqueueEdgeEvent(edgeEvent); err != nil {
		t.Fatalf("enqueueEdgeEvent(mixed): %v", err)
	} else if !queued {
		t.Fatal("enqueueEdgeEvent(mixed) queued = false, want true")
	}
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, globalTenantA, "global tenant A edge.event"), "evt-edge007-mixed", "edge.event")
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant edge.event"), "evt-edge007-mixed", "edge.event")
	assertNoGatewayEdgeStreamEvent(t, jobStream, "per-job stream must not receive generic edge.event")

	heartbeatPacket := &pb.BusPacket{Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "worker-edge007"}}}
	wantHeartbeat, err := protojson.Marshal(heartbeatPacket)
	if err != nil {
		t.Fatalf("protojson marshal heartbeat packet: %v", err)
	}
	s.enqueueBusPacket(heartbeatPacket)
	assertGatewayEdgeStreamRawJSON(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant heartbeat").data, wantHeartbeat)
	assertNoGatewayEdgeStreamEvent(t, globalTenantA, "tenant-scoped global stream must not receive tenantless heartbeat")
	assertNoGatewayEdgeStreamEvent(t, jobStream, "per-job stream must not receive tenantless heartbeat")
}

func TestEdgeEventStreamLiveForwardingAndCursorResumeRegression(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	drainGatewayEdgeStreamQueue(s.eventsCh)
	streamQueue := &wsClient{ch: s.eventsCh}
	session := createEdgeRouteSession(t, handler)
	execution2 := createEdgeRouteExecution(t, handler, session.SessionID)
	drainGatewayEdgeStreamQueue(s.eventsCh)

	first := edgeRoutePOST(t, handler, "/api/v1/edge/events",
		edgeEventJSON("evt-edge028-stream-live-1", session.SessionID, session.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:40:00Z", "hook.pre_tool_use", "ALLOW", "ok"))
	if first.Code != http.StatusCreated {
		t.Fatalf("first live event status = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "subscribe-before first edge.event"), "evt-edge028-stream-live-1", edgecore.DecisionAllow, edgecore.EventKindHookPreToolUse)

	second := edgeRoutePOST(t, handler, "/api/v1/edge/events",
		edgeEventJSON("evt-edge028-stream-live-2", session.SessionID, execution2.ExecutionID, edgeRouteTenant, "", "2026-05-01T12:40:01Z", "hook.policy_decision", "DENY", "blocked"))
	if second.Code != http.StatusCreated {
		t.Fatalf("second live event status = %d, want 201 body=%s", second.Code, second.Body.String())
	}
	assertStreamedEdgeEvent(t, readGatewayEdgeStreamEvent(t, streamQueue, "subscribe-before second edge.event"), "evt-edge028-stream-live-2", edgecore.DecisionDeny, edgecore.EventKindHookPolicyDecision)

	// The WebSocket stream is live-only on HEAD: there is no Edge-specific
	// cursor parameter on /api/v1/stream. Cursor catch-up/resume for a
	// subscriber that connects after writes is provided by the session/execution
	// event list endpoints, so pin both surfaces together here.
	page1RR := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/events?limit=1")
	if page1RR.Code != http.StatusOK {
		t.Fatalf("session events page1 status = %d, want 200 body=%s", page1RR.Code, page1RR.Body.String())
	}
	var page edgeEventPageResponseJSON
	decodeEdgeRouteJSON(t, page1RR, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge028-stream-live-1"})
	if page.NextCursor == "" {
		t.Fatalf("session events page1 next_cursor empty, want bounded resume cursor")
	}

	page2RR := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/events?limit=1&cursor="+page.NextCursor)
	if page2RR.Code != http.StatusOK {
		t.Fatalf("session events page2 status = %d, want 200 body=%s", page2RR.Code, page2RR.Body.String())
	}
	decodeEdgeRouteJSON(t, page2RR, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge028-stream-live-2"})
	if page.NextCursor != "" {
		t.Fatalf("session events page2 next_cursor = %q, want empty after bounded resume", page.NextCursor)
	}

	exec1RR := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+session.ExecutionID+"/events?limit=10")
	if exec1RR.Code != http.StatusOK {
		t.Fatalf("execution1 events status = %d, want 200 body=%s", exec1RR.Code, exec1RR.Body.String())
	}
	decodeEdgeRouteJSON(t, exec1RR, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge028-stream-live-1"})

	exec2RR := edgeRouteGET(t, handler, "/api/v1/edge/executions/"+execution2.ExecutionID+"/events?limit=10")
	if exec2RR.Code != http.StatusOK {
		t.Fatalf("execution2 events status = %d, want 200 body=%s", exec2RR.Code, exec2RR.Body.String())
	}
	decodeEdgeRouteJSON(t, exec2RR, &page)
	assertEdgeEventIDs(t, page.Items, []string{"evt-edge028-stream-live-2"})
}

func TestEdgeEventStreamHeartbeatShutdownAndGoroutineBound(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})
	s.eventsCh = make(chan wsEvent, 16)
	s.clients = make(map[*websocket.Conn]*wsClient)
	s.wsClientBufSz = 8
	baselineGoroutines := runtime.NumGoroutine()
	if err := s.startBusTaps(); err != nil {
		t.Fatalf("startBusTaps: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			close(s.shutdownCh)
			s.stopBusTaps()
			s.stopWorkerExpiry()
		}
	})

	crossTenant := registerGatewayEdgeStreamClient(t, s, "", true)
	tenantScoped := registerGatewayEdgeStreamClient(t, s, "tenant-edge-a", false)
	heartbeatPacket := &pb.BusPacket{Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: "worker-edge028-heartbeat"}}}
	wantHeartbeat, err := protojson.Marshal(heartbeatPacket)
	if err != nil {
		t.Fatalf("protojson marshal heartbeat packet: %v", err)
	}
	s.enqueueBusPacket(heartbeatPacket)
	assertGatewayEdgeStreamRawJSON(t, readGatewayEdgeStreamEvent(t, crossTenant, "cross-tenant heartbeat keepalive").data, wantHeartbeat)
	assertNoGatewayEdgeStreamEvent(t, tenantScoped, "tenant-scoped stream must not receive tenantless heartbeat keepalive")

	close(s.shutdownCh)
	s.stopBusTaps()
	s.stopWorkerExpiry()
	stopped = true
	if queued := s.enqueueWSEvent([]byte(`{"type":"edge.event","event":{"event_id":"evt-after-stop"}}`), "tenant-edge-a", ""); queued {
		t.Fatal("enqueueWSEvent after shutdown queued=true, want false after ctx-cancel/stop")
	}
	eventuallyGatewayEdgeStream(t, 2*time.Second, func() bool {
		return runtime.NumGoroutine() <= baselineGoroutines+5
	}, "stream goroutines to return to bounded baseline after shutdown")
}

// recordingEdgeRecorder captures RecordStreamDrop / AddStreamClients
// calls so EDGE-014 step-11 tests can pin the drop-reason contract
// without depending on a real Prometheus registry.
type recordingEdgeRecorder struct {
	edgecore.NoopRecorder
	mu     sync.Mutex
	drops  []string
	deltas []int
	sent   []string
	redact []string
	failed []string
}

func (r *recordingEdgeRecorder) RecordStreamDrop(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drops = append(r.drops, reason)
}

func (r *recordingEdgeRecorder) AddStreamClients(_ string, delta int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deltas = append(r.deltas, delta)
}

func (r *recordingEdgeRecorder) RecordStreamEventSent(tenant string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, tenant)
}

func (r *recordingEdgeRecorder) RecordEventRedacted(outcome string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redact = append(r.redact, outcome)
}

func (r *recordingEdgeRecorder) RecordRedactionFailed(site, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, site+":"+reason)
}

func (r *recordingEdgeRecorder) Drops() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.drops...)
}

func (r *recordingEdgeRecorder) Sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sent...)
}

func (r *recordingEdgeRecorder) Redacted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.redact...)
}

func (r *recordingEdgeRecorder) RedactionFailures() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.failed...)
}

func TestForwardPersistedEdgeEventRecordsSentMetricOnQueueSuccess(t *testing.T) {
	s, _, _ := newTestGateway(t)
	rec := &recordingEdgeRecorder{}
	s.edgeRecorder = rec
	drainGatewayEdgeStreamQueue(s.eventsCh)

	s.forwardPersistedEdgeEvent(validGatewayEdgeStreamEvent())

	got := rec.Sent()
	if len(got) != 1 || got[0] != "tenant-edge-a" {
		t.Fatalf("sent metrics = %v, want [tenant-edge-a]", got)
	}
	if drops := rec.Drops(); len(drops) != 0 {
		t.Fatalf("drops = %v, want none on success", drops)
	}
}

// TestForwardPersistedEdgeEventRecordsMarshalErrorDrop pins step-11
// behavior: when the persisted event fails normalization (e.g. empty
// TenantID), forwardPersistedEdgeEvent classifies the drop as
// marshal_error and records exactly one stream-drop metric with that
// bounded reason.
func TestForwardPersistedEdgeEventRecordsMarshalErrorDrop(t *testing.T) {
	s, _, _ := newTestGateway(t)
	rec := &recordingEdgeRecorder{}
	s.edgeRecorder = rec

	// An event missing TenantID fails Validate and trips the
	// marshal_error path inside enqueueEdgeEvent.
	bad := validGatewayEdgeStreamEvent()
	bad.TenantID = ""
	s.forwardPersistedEdgeEvent(bad)

	drops := rec.Drops()
	if len(drops) != 1 || drops[0] != "marshal_error" {
		t.Errorf("drops = %v, want [marshal_error]", drops)
	}
}

// TestForwardPersistedEdgeEventRecordsClientBufferFullDrop pins step-11
// behavior: when the WS bridge's eventsCh is saturated and
// enqueueWSEvent returns false, forwardPersistedEdgeEvent classifies
// the drop as client_buffer_full.
func TestForwardPersistedEdgeEventRecordsClientBufferFullDrop(t *testing.T) {
	s, _, _ := newTestGateway(t)
	rec := &recordingEdgeRecorder{}
	s.edgeRecorder = rec

	// Fill eventsCh to saturate enqueueWSEvent's non-blocking send.
	// The eventsCh is buffered at 512 (per gateway.go newServer); we
	// fill it directly with synthetic packets so the next enqueue
	// fails with queued=false.
	for i := 0; i < cap(s.eventsCh); i++ {
		select {
		case s.eventsCh <- wsEvent{}:
		default:
			t.Fatalf("could not pre-fill eventsCh at i=%d", i)
		}
	}

	s.forwardPersistedEdgeEvent(validGatewayEdgeStreamEvent())

	drops := rec.Drops()
	if len(drops) != 1 || drops[0] != "client_buffer_full" {
		t.Errorf("drops = %v, want [client_buffer_full]", drops)
	}
}

// TestRecordEdgeStreamDropNilRecorderIsNoOp pins that recordEdgeStreamDrop
// is safe to call on a server with no recorder configured (test code
// that bypasses newServer).
func TestRecordEdgeStreamDropNilRecorderIsNoOp(t *testing.T) {
	s := &server{} // no edgeRecorder set
	s.recordEdgeStreamDrop("client_buffer_full")
	// No panic = pass.
}

func validGatewayEdgeStreamEvent() edgecore.AgentActionEvent {
	return edgecore.AgentActionEvent{
		EventID:       "evt-edge007-stream-1",
		SessionID:     "edge_sess_stream_1",
		ExecutionID:   "exec_stream_1",
		TenantID:      "tenant-edge-a",
		PrincipalID:   "user-edge-a",
		Seq:           7,
		Timestamp:     time.Date(2026, 5, 1, 12, 7, 0, 0, time.UTC),
		Layer:         edgecore.LayerHook,
		Kind:          edgecore.EventKindHookPreToolUse,
		AgentProduct:  "claude-code",
		ToolName:      "Bash",
		ToolUseID:     "toolu-edge007-1",
		ActionName:    "bash.exec",
		Capability:    "exec.shell",
		RiskTags:      []string{"exec", "test"},
		InputRedacted: map[string]any{"command": "npm test"},
		InputHash:     "sha256:edge007input",
		Decision:      edgecore.DecisionAllow,
		Status:        edgecore.ActionStatusOK,
		Labels:        edgecore.Labels{"cwd": "/workspace/cordum"},
	}
}

func registerGatewayEdgeStreamClient(t *testing.T, s *server, tenant string, allowCrossTenant bool) *wsClient {
	t.Helper()
	client := &wsClient{
		ch:               make(chan wsEvent, 8),
		tenant:           strings.TrimSpace(tenant),
		allowCrossTenant: allowCrossTenant,
	}
	s.clientsMu.Lock()
	s.clients[&websocket.Conn{}] = client
	s.clientsMu.Unlock()
	return client
}

func registerGatewayEdgeStreamJobClient(t *testing.T, s *server, tenant string, jobID string) *wsClient {
	t.Helper()
	client := &wsClient{
		ch:     make(chan wsEvent, 8),
		tenant: strings.TrimSpace(tenant),
		jobID:  strings.TrimSpace(jobID),
	}
	s.clientsMu.Lock()
	s.clients[&websocket.Conn{}] = client
	s.clientsMu.Unlock()
	return client
}

func readGatewayEdgeStreamEvent(t *testing.T, client *wsClient, label string) wsEvent {
	t.Helper()
	select {
	case event := <-client.ch:
		return event
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", label)
		return wsEvent{}
	}
}

func assertNoGatewayEdgeStreamEvent(t *testing.T, client *wsClient, label string) {
	t.Helper()
	select {
	case event := <-client.ch:
		t.Fatalf("%s: unexpected event tenant=%q jobID=%q data=%s", label, event.tenant, event.jobID, string(event.data))
	case <-time.After(75 * time.Millisecond):
	}
}

func assertGatewayEdgeStreamEvent(t *testing.T, event wsEvent, wantContains string, wantFamily string) {
	t.Helper()
	if !strings.Contains(string(event.data), wantContains) {
		t.Fatalf("stream event data = %s, want to contain %q", string(event.data), wantContains)
	}
	if !strings.Contains(string(event.data), wantFamily) {
		t.Fatalf("stream event data = %s, want family marker %q", string(event.data), wantFamily)
	}
}

func assertGatewayEdgeStreamRawJSON(t *testing.T, got []byte, want []byte) {
	t.Helper()
	if string(got) != string(want) {
		t.Fatalf("stream raw JSON = %s, want unchanged protojson %s", string(got), string(want))
	}
	if strings.Contains(string(got), `"type":"edge.event"`) || strings.Contains(string(got), `"type": "edge.event"`) {
		t.Fatalf("BusPacket stream data was wrapped as edge.event: %s", string(got))
	}
}

func eventuallyGatewayEdgeStream(t *testing.T, timeout time.Duration, condition func() bool, label string) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", label)
		case <-ticker.C:
		}
	}
}
