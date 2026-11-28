package gateway

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

// EDGE-060 step 3 — idempotency tests for handleCreateEdgeSession.

const sessionIdempotencyBody = `{
    "agent_product":"claude-code",
    "agent_version":"1.2.3",
    "mode":"local-dev",
    "policy_snapshot":"snap-idempotency-test",
    "policy_mode":"observe"
}`

// edgeRoutePOSTWithIdempotencyKey is defined in edge_events_idempotency_test.go;
// reused here.

func TestCreateEdgeSessionIdempotencyReplayRederivesAssurance(t *testing.T) {
	const body = `{"agent_product":"claude-code","agent_version":"1.2.3",` +
		`"mode":"local-dev","policy_mode":"enforce","enforcement_layers":{"hook":true,"agentd":true}}`
	for _, tc := range []struct {
		name string
		want edgecore.AssuranceState
	}{
		{"running", edgecore.AssuranceStateHookMediated},
		{"ended", edgecore.AssuranceStateStale},
		{"heartbeat", edgecore.AssuranceStateStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, handler := newEdgeRouteTestServer(t)
			first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", body, "assurance-replay")
			if first.Code != http.StatusCreated {
				t.Fatalf("create: HTTP %d: %s", first.Code, first.Body.String())
			}
			var want edgeSessionCreateResponse
			decodeEdgeRouteJSON(t, first, &want)
			if want.Session.AssuranceState != edgecore.AssuranceStateHookMediated {
				t.Fatalf("create assurance_state = %q, want hook-mediated", want.Session.AssuranceState)
			}
			switch tc.name {
			case "ended":
				end := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+want.SessionID+"/end", `{"status":"ended"}`)
				if end.Code != http.StatusOK {
					t.Fatalf("end: HTTP %d: %s", end.Code, end.Body.String())
				}
			case "heartbeat":
				s.edgeStore = edgeAssuranceHeartbeatStore{Store: s.edgeStore}
			}
			replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", body, "assurance-replay")
			if replay.Code != http.StatusCreated {
				t.Fatalf("replay: HTTP %d: %s", replay.Code, replay.Body.String())
			}
			var got edgeSessionCreateResponse
			decodeEdgeRouteJSON(t, replay, &got)
			want.Session.AssuranceState = tc.want
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("replay must change only assurance_state to %q: got %+v, want %+v", tc.want, got, want)
			}
		})
	}
}

// TestCreateEdgeSessionIdempotencyReplay proves the EDGE-060 contract:
// two POSTs with the same Idempotency-Key + identical body produce the
// SAME response (same session_id, same execution_id, same trace_id).
// Without idempotency, each POST would generate fresh UUIDs — the
// agentd retry path would create ghost sessions in the dashboard.
func TestCreateEdgeSessionIdempotencyReplay(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	const idempotencyKey = "edge-060-test-replay-key"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, idempotencyKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 body=%s", first.Code, first.Body.String())
	}
	var firstResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, first, &firstResp)
	if firstResp.SessionID == "" {
		t.Fatalf("first POST missing session_id; body=%s", first.Body.String())
	}

	// Second POST with same key + same body — must replay.
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, idempotencyKey)
	if second.Code != http.StatusCreated {
		t.Fatalf("second POST = %d, want 201 (cached replay) body=%s", second.Code, second.Body.String())
	}
	var secondResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, second, &secondResp)
	if secondResp.SessionID != firstResp.SessionID {
		t.Fatalf("idempotent retry produced different session_id: first=%q second=%q",
			firstResp.SessionID, secondResp.SessionID)
	}
	if secondResp.ExecutionID != firstResp.ExecutionID {
		t.Fatalf("idempotent retry produced different execution_id: first=%q second=%q",
			firstResp.ExecutionID, secondResp.ExecutionID)
	}
	if secondResp.TraceID != firstResp.TraceID {
		t.Fatalf("idempotent retry produced different trace_id: first=%q second=%q",
			firstResp.TraceID, secondResp.TraceID)
	}
}

type edgeSessionReplayReadStore struct {
	edgecore.Store
	err error
}

func (s edgeSessionReplayReadStore) GetSession(context.Context, string, string) (*edgecore.EdgeSession, bool, error) {
	return nil, false, s.err
}

func TestCreateEdgeSessionIdempotencyReplayReadBoundaries(t *testing.T) {
	const body = `{"agent_product":"claude-code","mode":"local-dev","policy_mode":"enforce",` +
		`"enforcement_layers":{"hook":true,"agentd":true}}`
	for _, name := range []string{"missing", "session_error", "heartbeat_error"} {
		t.Run(name, func(t *testing.T) {
			s, handler := newEdgeRouteTestServer(t)
			first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", body, "read-boundaries")
			if first.Code != http.StatusCreated {
				t.Fatalf("create: HTTP %d: %s", first.Code, first.Body.String())
			}
			wantStatus := http.StatusInternalServerError
			switch name {
			case "missing":
				s.edgeStore = edgeSessionReplayReadStore{Store: s.edgeStore}
				wantStatus = http.StatusCreated
			case "session_error":
				s.edgeStore = edgeSessionReplayReadStore{Store: s.edgeStore, err: errors.New("private session read error")}
			case "heartbeat_error":
				s.edgeStore = edgeAssuranceHeartbeatStore{Store: s.edgeStore, err: errors.New("private heartbeat error")}
			}
			replay := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", body, "read-boundaries")
			if replay.Code != wantStatus {
				t.Fatalf("replay: HTTP %d, want %d: %s", replay.Code, wantStatus, replay.Body.String())
			}
			if name == "missing" {
				var got edgeSessionCreateResponse
				decodeEdgeRouteJSON(t, replay, &got)
				if got.Session.AssuranceState != edgecore.AssuranceStateStale {
					t.Fatalf("missing session assurance_state = %q, want stale", got.Session.AssuranceState)
				}
			} else if strings.Contains(replay.Body.String(), "hook-mediated") || strings.Contains(replay.Body.String(), "private") {
				t.Fatalf("read error released cached assurance or private error: %s", replay.Body.String())
			}
		})
	}
}

func TestEdgeSessionReplayRejectsMalformedCache(t *testing.T) {
	if body, err := rederiveEdgeSessionReplay(context.Background(), nil, edgeRouteTenant, []byte(`{`)); err == nil || len(body) != 0 {
		t.Fatalf("malformed cache: body=%s error=%v", body, err)
	}
}

// TestCreateEdgeSessionIdempotencyConflictOnBodyChange proves that
// reusing an Idempotency-Key with a DIFFERENT body returns 409 — the
// hash mismatch is detected and the second request is refused, so a
// buggy client can't accidentally pivot a cached response onto a
// different intent.
func TestCreateEdgeSessionIdempotencyConflictOnBodyChange(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})
	const idempotencyKey = "edge-060-test-conflict-key"

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, idempotencyKey)
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST = %d, want 201 body=%s", first.Code, first.Body.String())
	}

	// Same key, different body — 409 idempotency_conflict.
	differentBody := `{
        "agent_product":"claude-code",
        "agent_version":"9.9.9",
        "mode":"local-dev",
        "policy_snapshot":"DIFFERENT-snap",
        "policy_mode":"enforce"
    }`
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", differentBody, idempotencyKey)
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST with different body = %d, want 409 body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "idempotency") {
		t.Fatalf("expected idempotency-shaped error body; got %s", second.Body.String())
	}
}

// TestCreateEdgeSessionMissingKeyNonIdempotent proves that a POST
// WITHOUT an Idempotency-Key header still works as today — every call
// generates a fresh session. Backward compat with clients that haven't
// adopted the idempotency contract yet.
func TestCreateEdgeSessionMissingKeyNonIdempotent(t *testing.T) {
	_, handler := newEdgeEvaluateTestServer(t, &edgeEvaluateStubSafetyClient{})

	first := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, "")
	second := edgeRoutePOSTWithIdempotencyKey(t, handler, "/api/v1/edge/sessions", sessionIdempotencyBody, "")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("expected both POSTs to be 201; got first=%d second=%d", first.Code, second.Code)
	}
	var firstResp, secondResp edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, first, &firstResp)
	decodeEdgeRouteJSON(t, second, &secondResp)
	if firstResp.SessionID == secondResp.SessionID {
		t.Fatalf("non-idempotent POSTs returned same session_id %q — keys must produce fresh UUIDs", firstResp.SessionID)
	}
	if firstResp.ExecutionID == secondResp.ExecutionID {
		t.Fatalf("non-idempotent POSTs returned same execution_id — keys must produce fresh UUIDs")
	}
}
