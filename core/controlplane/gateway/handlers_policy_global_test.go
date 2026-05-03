package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// putPolicyGlobal is a helper that calls handlePutPolicyGlobal with the
// given body and the admin role tenant — matches the pattern used by the
// signing-test helpers.
func putPolicyGlobal(t *testing.T, s *server, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/global", bytes.NewReader(raw))
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handlePutPolicyGlobal(rec, req)
	return rec
}

func getPolicyGlobal(t *testing.T, s *server) (*httptest.ResponseRecorder, globalPolicyResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/global", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handleGetPolicyGlobal(rec, req)
	if rec.Code == http.StatusOK {
		var resp globalPolicyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
		}
		return rec, resp
	}
	return rec, globalPolicyResponse{}
}

const globalInputContent = `rules:
  - id: global-input-allow
    match:
      topics:
        - job.test
    decision: allow
`

const globalEdgeContent = `rules:
  - id: global-edge-deny
    match:
      topics:
        - job.edge.action
      labels:
        path.class: secret
    decision: deny
`

const globalInvariantsContent = `rules:
  - id: inv-deny-secret-paths
    match:
      topics:
        - job.edge.action
      labels:
        path.class: secret
    decision: deny
    reason: SecOps invariant
output_rules:
  - id: inv-quarantine-output
    severity: critical
    decision: quarantine
    match:
      content_patterns:
        - 'API_KEY=[A-Za-z0-9]+'
`

// TestGetPolicyGlobalReturnsAllSections verifies the GET handler returns
// the canonical 5-section view with snapshot identifiers and per-section
// bundle ids — even when no bundles have been authored yet (empty state).
func TestGetPolicyGlobalReturnsAllSections(t *testing.T) {
	s, _, _ := newTestGateway(t)

	rec, resp := getPolicyGlobal(t, s)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/policy/global = %d body=%s", rec.Code, rec.Body.String())
	}
	wantSections := []string{
		globalSectionInputRules,
		globalSectionOutputRules,
		globalSectionEdgeActionRules,
		globalSectionMCPToolRules,
		globalSectionInvariants,
	}
	for _, name := range wantSections {
		section, ok := resp.Sections[name]
		if !ok {
			t.Fatalf("missing section %q in response: %+v", name, resp.Sections)
		}
		if section.BundleID == "" {
			t.Fatalf("section %q has empty bundle_id", name)
		}
	}
	// Empty state — no bundles authored yet — yields empty snapshot.
	if resp.SnapshotHash != "" {
		t.Fatalf("expected empty snapshot for empty bundles, got %q", resp.SnapshotHash)
	}
}

// TestPutPolicyGlobalAtomicWriteReadBack proves PUT writes all 5 sections
// in one call and a subsequent GET reflects them with the new snapshot.
func TestPutPolicyGlobalAtomicWriteReadBack(t *testing.T) {
	s, _, _ := newTestGateway(t)

	body := map[string]any{
		"author":  "tester",
		"message": "EDGE-052 unified Global write test",
		"sections": map[string]any{
			globalSectionInputRules:      map[string]any{"content": globalInputContent},
			globalSectionEdgeActionRules: map[string]any{"content": globalEdgeContent},
			globalSectionInvariants:      map[string]any{"content": globalInvariantsContent},
		},
	}
	rec := putPolicyGlobal(t, s, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d body=%s", rec.Code, rec.Body.String())
	}
	var putResp globalPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &putResp); err != nil {
		t.Fatalf("decode PUT response: %v body=%s", err, rec.Body.String())
	}
	if putResp.SnapshotHash == "" {
		t.Fatalf("expected non-empty snapshot in PUT response, got %+v", putResp)
	}

	// GET back — content reflects the PUT.
	_, getResp := getPolicyGlobal(t, s)
	if getResp.SnapshotHash != putResp.SnapshotHash {
		t.Fatalf("GET snapshot %q != PUT snapshot %q", getResp.SnapshotHash, putResp.SnapshotHash)
	}
	if !strings.Contains(getResp.Sections[globalSectionInputRules].Content, "global-input-allow") {
		t.Fatalf("input section missing rule; got %q", getResp.Sections[globalSectionInputRules].Content)
	}
	if !strings.Contains(getResp.Sections[globalSectionEdgeActionRules].Content, "global-edge-deny") {
		t.Fatalf("edge_action section missing rule; got %q", getResp.Sections[globalSectionEdgeActionRules].Content)
	}
	if !strings.Contains(getResp.Sections[globalSectionInvariants].Content, "inv-deny-secret-paths") {
		t.Fatalf("invariants section missing rule; got %q", getResp.Sections[globalSectionInvariants].Content)
	}
	// Sections not included in the PUT remain empty.
	if getResp.Sections[globalSectionOutputRules].Content != "" {
		t.Fatalf("output section should be empty (not included in PUT); got %q", getResp.Sections[globalSectionOutputRules].Content)
	}
	if getResp.Sections[globalSectionMCPToolRules].Content != "" {
		t.Fatalf("mcp_tool section should be empty (not included in PUT); got %q", getResp.Sections[globalSectionMCPToolRules].Content)
	}
}

// TestPutPolicyGlobalSnapshotMismatchReturns409 proves optimistic
// concurrency: PUT with a stale snapshot_version is rejected with 409 so
// concurrent dashboard editors don't silently overwrite each other.
func TestPutPolicyGlobalSnapshotMismatchReturns409(t *testing.T) {
	s, _, _ := newTestGateway(t)

	// First PUT establishes an initial snapshot.
	first := putPolicyGlobal(t, s, map[string]any{
		"sections": map[string]any{
			globalSectionInputRules: map[string]any{"content": globalInputContent},
		},
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first PUT = %d body=%s", first.Code, first.Body.String())
	}

	// Second PUT with an obviously-stale snapshot_version → 409.
	stale := putPolicyGlobal(t, s, map[string]any{
		"snapshot_version": "cfg:0000000000000000000000000000000000000000000000000000000000000000",
		"sections": map[string]any{
			globalSectionInputRules: map[string]any{"content": globalInputContent},
		},
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale PUT = %d, want 409 Conflict body=%s", stale.Code, stale.Body.String())
	}
	if !strings.Contains(stale.Body.String(), "snapshot_version mismatch") {
		t.Fatalf("expected snapshot_version mismatch error; got %s", stale.Body.String())
	}
}

// TestPutPolicyGlobalUnknownSectionReturns400 verifies the handler
// rejects unknown section names — typo guard for the dashboard.
func TestPutPolicyGlobalUnknownSectionReturns400(t *testing.T) {
	s, _, _ := newTestGateway(t)
	rec := putPolicyGlobal(t, s, map[string]any{
		"sections": map[string]any{
			"not_a_real_section": map[string]any{"content": globalInputContent},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown section") {
		t.Fatalf("expected 'unknown section' error; got %s", rec.Body.String())
	}
}

// TestPutPolicyGlobalMalformedYAMLReturns400 verifies the handler
// validates each section's YAML and reports which section failed so
// dashboard editors can pinpoint the bad input.
func TestPutPolicyGlobalMalformedYAMLReturns400(t *testing.T) {
	s, _, _ := newTestGateway(t)
	rec := putPolicyGlobal(t, s, map[string]any{
		"sections": map[string]any{
			globalSectionInputRules:      map[string]any{"content": globalInputContent},
			globalSectionEdgeActionRules: map[string]any{"content": "rules:\n  - id: bad\n    match: not_a_map\n"},
		},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), globalSectionEdgeActionRules) {
		t.Fatalf("expected error to identify the bad section %q; got %s",
			globalSectionEdgeActionRules, rec.Body.String())
	}
}

// TestPolicyGlobalRoutesRegistered verifies the new endpoints land in
// the gateway routing table with auth=admin gating — the wire contract
// the dashboard depends on. Direct 403 testing requires a configured
// auth provider; the test gateway intentionally leaves s.auth nil so
// other handlers can exercise their happy paths without RBAC plumbing.
// Auth enforcement is exercised by the integration-tagged suite that
// runs the full RunWithAuth wiring.
func TestPolicyGlobalRoutesRegistered(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	// /api/v1/policy/* routes default to Auth=tenant per inferRouteAuth
	// (matching the existing /api/v1/policy/bundles convention) — admin
	// permission gating happens inside the handler via
	// requireStoreAndPermissionOrRole(PermPolicyWrite, "admin", ...).
	var got, gotPut bool
	for _, route := range s.Routes() {
		if route.Path == "/api/v1/policy/global" && route.Method == http.MethodGet {
			got = true
			if route.Auth == "public" {
				t.Errorf("GET /api/v1/policy/global Auth = public, must be authenticated")
			}
		}
		if route.Path == "/api/v1/policy/global" && route.Method == http.MethodPut {
			gotPut = true
			if route.Auth == "public" {
				t.Errorf("PUT /api/v1/policy/global Auth = public, must be authenticated")
			}
		}
	}
	if !got {
		t.Fatal("missing GET /api/v1/policy/global route registration")
	}
	if !gotPut {
		t.Fatal("missing PUT /api/v1/policy/global route registration")
	}
}

// TestPutPolicyGlobalBackwardCompatWithBundlesEndpoint verifies that a
// PUT /global writes per-section bundles and a subsequent GET
// /api/v1/policy/bundles surfaces them with the same content. The legacy
// per-bundle endpoints remain a working alias.
func TestPutPolicyGlobalBackwardCompatWithBundlesEndpoint(t *testing.T) {
	s, _, _ := newTestGateway(t)

	rec := putPolicyGlobal(t, s, map[string]any{
		"sections": map[string]any{
			globalSectionInputRules: map[string]any{"content": globalInputContent},
			globalSectionInvariants: map[string]any{"content": globalInvariantsContent},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d body=%s", rec.Code, rec.Body.String())
	}

	// loadPolicyBundles returns the same map BuildPolicyFromBundles
	// consumes — what the legacy /api/v1/policy/bundles endpoint
	// projects.
	bundles, _, err := s.loadPolicyBundles(testContext())
	if err != nil {
		t.Fatalf("loadPolicyBundles: %v", err)
	}
	rawInput, ok := bundles[globalBundleKeyInput].(map[string]any)
	if !ok {
		t.Fatalf("expected %q in bundles map; got keys=%v", globalBundleKeyInput, sortedBundleKeys(bundles))
	}
	if !strings.Contains(strings.TrimSpace(policybundles.StringFromAny(rawInput["content"])), "global-input-allow") {
		t.Fatalf("legacy bundle entry missing PUT content; got %v", rawInput)
	}
	rawInv, ok := bundles[policybundles.PolicyInvariantsBundleKey].(map[string]any)
	if !ok {
		t.Fatalf("expected invariants bundle key in bundles map; got keys=%v", sortedBundleKeys(bundles))
	}
	if !strings.Contains(strings.TrimSpace(policybundles.StringFromAny(rawInv["content"])), "inv-deny-secret-paths") {
		t.Fatalf("invariants bundle missing PUT content; got %v", rawInv)
	}
}
