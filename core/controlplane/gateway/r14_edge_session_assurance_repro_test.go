package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
)

func TestR14_EdgeSessionAssuranceState(t *testing.T) {
	_, handler := newEdgeRouteTestServer(t)
	hook := map[string]bool{"hook": true, "agentd": true}
	passive := []string{"compatibility", "unsupported", "stale"}
	mediated := []string{"compatibility", "hook-mediated", "unsupported", "stale"}
	unknown := []string{"unsupported", "stale"}
	// No executor fact exists, so executor-enforced is never allowed. Observe
	// does not block. No recognized layer is an unknown combination: unsupported.
	cases := []struct {
		name, policy, mode string
		layers             map[string]bool
		allowed            []string
	}{
		{"observe-hook", "observe", "local-dev", hook, passive},
		{"enforce-hook", "enforce", "local-dev", hook, mediated},
		{"enterprise-strict-hook", "enterprise-strict", "enterprise-managed", hook, mediated},
		{"enforce-no-layer", "enforce", "local-dev", map[string]bool{}, unknown},
		{"enforce-unknown-layer", "enforce", "local-dev", map[string]bool{"x-unrecognized-layer": true}, unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"agent_product": "claude-code", "agent_version": "1.2.3", "mode": tc.mode,
				"policy_snapshot": "r14-" + tc.name, "policy_mode": tc.policy,
				"enforcement_layers": tc.layers, "labels": map[string]string{"case": tc.name},
			})
			if err != nil {
				t.Fatalf("R14 setup: encode create: %v", err)
			}
			created := r14Decode(t, edgeRoutePOST(t, handler, "/api/v1/edge/sessions", string(body)), http.StatusCreated)
			id, ok := created["session_id"].(string)
			if !ok || id == "" {
				t.Fatalf("R14 setup: missing session_id: %v", created)
			}
			get := r14Decode(t, edgeRouteGET(t, handler, "/api/v1/edge/sessions/"+id), http.StatusOK)
			page := r14Decode(t, edgeRouteGET(t, handler, "/api/v1/edge/sessions?limit=200"), http.StatusOK)
			r14CheckSources(t, tc.name, tc.allowed, r14Object(t, created["session"]), get, r14FindSession(t, page, id))
		})
	}
}

func r14Decode(t *testing.T, rr *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("R14 setup: HTTP %d, want %d: %s", rr.Code, status, rr.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("R14 setup: decode response: %v", err)
	}
	return result
}

func r14Object(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("R14 setup: expected session object, got %v", value)
	}
	return object
}

func r14FindSession(t *testing.T, page map[string]any, id string) map[string]any {
	t.Helper()
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatalf("R14 setup: missing list items: %v", page)
	}
	for _, item := range items {
		session := r14Object(t, item)
		if session["session_id"] == id {
			return session
		}
	}
	t.Fatalf("R14 setup: session %s missing from list", id)
	return nil
}

func r14CheckSources(t *testing.T, caseName string, allowed []string, create, get, list map[string]any) {
	t.Helper()
	states := []string{"compatibility", "hook-mediated", "executor-enforced", "unsupported", "stale"}
	for _, source := range []struct {
		name    string
		session map[string]any
	}{{"create", create}, {"get", get}, {"list", list}} {
		value, exists := source.session["assurance_state"]
		if !exists {
			t.Errorf("R14 %s: %s has no assurance_state field", caseName, source.name)
			continue
		}
		state, ok := value.(string)
		if !ok || !slices.Contains(states, state) {
			t.Errorf("R14 %s: %s assurance_state %v is not one of the five states", caseName, source.name, value)
		} else if !slices.Contains(allowed, state) {
			t.Errorf("R14 %s: %s assurance_state %q is stronger than the facts support; allowed %v", caseName, source.name, state, allowed)
		}
	}
	c, g, l := create["assurance_state"], get["assurance_state"], list["assurance_state"]
	if !reflect.DeepEqual(c, g) || !reflect.DeepEqual(g, l) {
		t.Errorf("R14 %s: assurance_state differs across the session APIs: create=%v get=%v list=%v", caseName, c, g, l)
	}
}
