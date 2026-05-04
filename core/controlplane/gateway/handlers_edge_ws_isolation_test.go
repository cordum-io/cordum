package gateway

import (
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/gorilla/websocket"
)

// EDGE-067 — WebSocket /api/v1/stream cross-tenant isolation.
//
// The shared WS surface delivers edge events tagged by tenant. Existing
// TestEdgeEventStreamTenantFilteringAndBusPacketRegression covers the
// in-memory dispatcher tenant filter with hand-built events. This test
// adds end-to-end coverage: tenant A connects to /api/v1/stream and tenant
// B's persisted edge events do NOT appear on A's connection.
//
// Per task DoD #3: "Stream subscribes to TENANT-SCOPED channels/keys, not
// a global firehose with post-filter (verify in code; if it's post-filter,
// file a follow-up to fix at subscribe time)." Today's gateway uses
// per-connection POST-FILTER on a shared `s.eventsCh` (handlers_stream.go
// `enqueueWSEvent`). This test confirms post-filter works end-to-end and
// the architecture note in docs/edge/cross-tenant-isolation.md tracks the
// preference for subscribe-time scoping.

func TestEdgeCrossTenantWSDoesNotLeakForeignTenantEvents(t *testing.T) {
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

	// Tenant A connects with no cross-tenant override — strict filter.
	// This calls into the same registerGatewayEdgeStreamClient pattern used
	// by edge_stream_test.go; reuse-before-build (epic rail).
	tenantA := registerGatewayEdgeStreamClient(t, s, edgeRouteTenant, false)

	// Persist an event in tenant B's scope and force the gateway to enqueue
	// it onto the shared WS dispatch channel. Any leak across tenants would
	// surface here as a delivery to tenantA's client.
	eventB := validGatewayEdgeStreamEvent()
	eventB.TenantID = edgeRouteOtherTenant
	eventB.SessionID = "sess-edge067-cross-b"
	eventB.ExecutionID = "exec-edge067-cross-b"
	eventB.EventID = "evt-edge067-cross-b"
	queued, err := s.enqueueEdgeEvent(eventB)
	if err != nil {
		t.Fatalf("enqueueEdgeEvent for tenant B: %v", err)
	}
	if !queued {
		t.Fatal("enqueueEdgeEvent for tenant B: queued=false, want true (queue capacity OK)")
	}

	// Tenant A must NOT receive the tenant B event; assertNo* drains the
	// shared queue with a brief deadline.
	assertNoGatewayEdgeStreamEvent(t, tenantA, "tenant A WS must not receive tenant B edge.event")

	// Tenant A's own event proves the connection is still wired and able to
	// receive — guarantees the assertion above isn't passing because the
	// channel is dead.
	eventA := validGatewayEdgeStreamEvent()
	eventA.TenantID = edgeRouteTenant
	eventA.EventID = "evt-edge067-tenant-a"
	if queued, err := s.enqueueEdgeEvent(eventA); err != nil {
		t.Fatalf("enqueueEdgeEvent for tenant A: %v", err)
	} else if !queued {
		t.Fatal("enqueueEdgeEvent for tenant A: queued=false, want true")
	}
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, tenantA, "tenant A WS receives own edge.event"), eventA.EventID, "edge.event")
}

// TestEdgeCrossTenantWSEnqueueValidatesTenantTagBeforeDispatch — the WS
// fan-out path (forwardPersistedEdgeEvent) calls enqueueEdgeEvent, which
// runs normalizeEdgeEventForStream first. A blank tenant_id on a
// persisted event must fail-closed (no broadcast) so dispatch can never
// touch a per-connection filter with empty input.
func TestEdgeCrossTenantWSEnqueueValidatesTenantTagBeforeDispatch(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.shutdownCh = make(chan struct{})
	s.eventsCh = make(chan wsEvent, 8)
	s.clients = make(map[*websocket.Conn]*wsClient)
	s.wsClientBufSz = 4
	if err := s.startBusTaps(); err != nil {
		t.Fatalf("startBusTaps: %v", err)
	}
	t.Cleanup(func() {
		close(s.shutdownCh)
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})

	tenantA := registerGatewayEdgeStreamClient(t, s, edgeRouteTenant, false)

	tenantless := validGatewayEdgeStreamEvent()
	tenantless.TenantID = " "
	if _, err := s.enqueueEdgeEvent(tenantless); err == nil {
		t.Fatal("enqueueEdgeEvent with blank tenant_id error = nil, want fail-closed validation error")
	}

	assertNoGatewayEdgeStreamEvent(t, tenantA, "tenant A must not receive tenantless edge.event")

	// Sanity: a normal tenant-tagged event still flows.
	good := validGatewayEdgeStreamEvent()
	good.TenantID = edgeRouteTenant
	good.EventID = "evt-edge067-tenantless-sanity"
	good.Decision = edgecore.DecisionAllow
	if queued, err := s.enqueueEdgeEvent(good); err != nil {
		t.Fatalf("enqueueEdgeEvent good event: %v", err)
	} else if !queued {
		t.Fatal("enqueueEdgeEvent good event: queued=false")
	}
	assertGatewayEdgeStreamEvent(t, readGatewayEdgeStreamEvent(t, tenantA, "tenant A receives good event"), good.EventID, "edge.event")
}
