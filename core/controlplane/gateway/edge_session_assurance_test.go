package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	edgecore "github.com/cordum/cordum/core/edge"
)

// edgeAssuranceHeartbeatStore reports every heartbeat as not alive, standing
// for a heartbeat older than the session heartbeat TTL, or as unreadable when
// err is set. Everything else reaches the real store.
type edgeAssuranceHeartbeatStore struct {
	edgecore.Store
	err error
}

func (s edgeAssuranceHeartbeatStore) HeartbeatAlive(context.Context, string, string) (bool, error) {
	return false, s.err
}

func TestEdgeSessionAssuranceStaleHeartbeat(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	created := createEdgeAssuranceSession(t, handler, "enforce", `{"hook":true,"agentd":true}`)
	if got := created.Session.AssuranceState; got != edgecore.AssuranceStateHookMediated {
		t.Fatalf("control: create assurance_state = %q, want hook-mediated", got)
	}
	assertEdgeAssuranceEverywhere(t, handler, created.SessionID, edgecore.AssuranceStateHookMediated)

	s.edgeStore = edgeAssuranceHeartbeatStore{Store: s.edgeStore}
	assertEdgeAssuranceEverywhere(t, handler, created.SessionID, edgecore.AssuranceStateStale)
}

func TestEdgeSessionAssuranceHeartbeatErrorIsInternalError(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	created := createEdgeAssuranceSession(t, handler, "enforce", `{"hook":true,"agentd":true}`)
	s.edgeStore = edgeAssuranceHeartbeatStore{Store: s.edgeStore, err: errors.New("heartbeat read failed")}

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"get":    edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+created.SessionID),
		"list":   edgeRouteGET(t, handler, "/api/v1/edge/sessions?limit=200"),
		"export": edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+created.SessionID+"/export", `{}`),
	} {
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("%s: HTTP %d, want 500: %s", name, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "assurance_state") {
			t.Errorf("%s: an unreadable heartbeat still returned an assurance_state: %s", name, rr.Body.String())
		}
	}
}

func TestEdgeSessionAssuranceEnforceMissingLayer(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	for _, tc := range []struct{ name, policy, layers string }{
		{"enforce-hook-only", "enforce", `{"hook":true}`},
		{"enterprise-strict-agentd-only", "enterprise-strict", `{"agentd":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := createEdgeAssuranceSession(t, handler, tc.policy, tc.layers)
			if got := created.Session.AssuranceState; got != edgecore.AssuranceStateUnsupported {
				t.Errorf("create assurance_state = %q, want unsupported", got)
			}
			assertEdgeAssuranceEverywhere(t, handler, created.SessionID, edgecore.AssuranceStateUnsupported)
		})
	}
}

func TestEdgeSessionAssuranceEndedIsStale(t *testing.T) {
	s, handler := newEdgeRouteTestServer(t)
	sink := &testAuditSender{}
	s.auditExporter = sink
	created := createEdgeAssuranceSession(t, handler, "enforce", `{"hook":true,"agentd":true}`)

	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+created.SessionID+"/end", `{"status":"ended"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("end session: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var ended edgecore.EdgeSession
	decodeEdgeRouteJSON(t, rr, &ended)
	if ended.AssuranceState != edgecore.AssuranceStateStale {
		t.Errorf("end response assurance_state = %q, want stale", ended.AssuranceState)
	}
	assertEdgeAssuranceEverywhere(t, handler, created.SessionID, edgecore.AssuranceStateStale)

	want := map[string]string{
		audit.EventEdgeSessionStarted: string(edgecore.AssuranceStateHookMediated),
		audit.EventEdgeSessionEnded:   string(edgecore.AssuranceStateStale),
	}
	for i := 0; i < sink.Len(); i++ {
		ev := sink.Get(i)
		if state, ok := want[ev.EventType]; ok {
			if got := ev.Extra["assurance_state"]; got != state {
				t.Errorf("%s audit assurance_state = %q, want %q", ev.EventType, got, state)
			}
			delete(want, ev.EventType)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing session audit events: %v", want)
	}
}

func createEdgeAssuranceSession(t *testing.T, handler http.Handler, policyMode, layers string) edgeSessionCreateResponseJSON {
	t.Helper()
	rr := edgeRoutePOST(t, handler, "/api/v1/edge/sessions", `{"agent_product":"claude-code","agent_version":"1.2.3",`+
		`"mode":"local-dev","policy_mode":"`+policyMode+`","enforcement_layers":`+layers+`}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create session: HTTP %d: %s", rr.Code, rr.Body.String())
	}
	var created edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rr, &created)
	return created
}

// assertEdgeAssuranceEverywhere checks the session's assurance_state in the
// get, list and export responses.
func assertEdgeAssuranceEverywhere(t *testing.T, handler http.Handler, sessionID string, want edgecore.AssuranceState) {
	t.Helper()
	get := edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+sessionID)
	if get.Code != http.StatusOK {
		t.Fatalf("get session: HTTP %d: %s", get.Code, get.Body.String())
	}
	var session edgecore.EdgeSession
	decodeEdgeRouteJSON(t, get, &session)

	export := edgeRoutePOST(t, handler, "/api/v1/edge/sessions/"+sessionID+"/export", `{}`)
	if export.Code != http.StatusOK {
		t.Fatalf("export session: HTTP %d: %s", export.Code, export.Body.String())
	}
	var bundle struct {
		Session edgecore.EdgeSession `json:"session"`
	}
	decodeEdgeRouteJSON(t, export, &bundle)

	for name, got := range map[string]edgecore.AssuranceState{
		"get":    session.AssuranceState,
		"list":   listedEdgeAssurance(t, handler, sessionID),
		"export": bundle.Session.AssuranceState,
	} {
		if got != want {
			t.Errorf("%s assurance_state = %q, want %q", name, got, want)
		}
	}
}

func listedEdgeAssurance(t *testing.T, handler http.Handler, sessionID string) edgecore.AssuranceState {
	t.Helper()
	for cursor := ""; ; {
		rr := edgeRouteGET(t, handler, "/api/v1/edge/sessions?limit=200&cursor="+url.QueryEscape(cursor))
		if rr.Code != http.StatusOK {
			t.Fatalf("list sessions: HTTP %d: %s", rr.Code, rr.Body.String())
		}
		var page edgeSessionPageJSON
		decodeEdgeRouteJSON(t, rr, &page)
		for _, item := range page.Items {
			if item.SessionID == sessionID {
				return item.AssuranceState
			}
		}
		if page.NextCursor == "" {
			t.Fatalf("session %s missing from list", sessionID)
		}
		cursor = page.NextCursor
	}
}
