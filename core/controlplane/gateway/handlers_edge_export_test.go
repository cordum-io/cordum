package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// EDGE-065 — POST /api/v1/edge/sessions/{id}/export must reject
// caller-supplied max_events values above maxExportEventsRequest at
// request-validation time (before any assembler invocation that would
// pre-allocate iteration buffers). Pre-fix, the handler passed any int
// straight to the assembler; the late edgeExportMaxBytes size cap fired
// only AFTER the assembler had already iterated and allocated.

func TestEdgeExportMaxEventsAcceptsZeroFallsBackToDefault(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	// max_events=0 is the historical sentinel for "use the assembler's
	// defaultExportEventsCap (5000)". Behavior preserved by the EDGE-065
	// fix — the handler validates only the upper bound, not the lower one.
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":0}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("max_events=0 status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEdgeExportMaxEventsAcceptsValueBelowCap(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":5000}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("max_events=5000 (≤ cap) status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

func TestEdgeExportMaxEventsRejectsValueAboveCap(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":10001}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("max_events=10001 (above cap) status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "max_events_too_large") {
		t.Fatalf("max_events=10001 response missing stable code 'max_events_too_large': %s", rr.Body.String())
	}
}

func TestEdgeExportMaxEventsRejectsAtBoundaryPlusOne(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	session := createEdgeRouteSession(t, handler)

	// Exactly maxExportEventsRequest is allowed (≤ cap).
	rrAtCap := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":10000}`)
	if rrAtCap.Code != http.StatusOK {
		t.Fatalf("max_events=10000 (exactly cap) status = %d, want 200; body=%s", rrAtCap.Code, rrAtCap.Body.String())
	}

	// One above is rejected.
	rrOver := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+session.SessionID+"/export", `{"max_events":1000000}`)
	if rrOver.Code != http.StatusBadRequest {
		t.Fatalf("max_events=1000000 status = %d, want 400; body=%s", rrOver.Code, rrOver.Body.String())
	}
}
