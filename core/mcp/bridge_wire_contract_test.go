package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Regression coverage for task-2d989055 reopen #1: the MCP bridge
// was sending body shapes that the real gateway handlers silently
// drop. These tests drive the bridge against a gateway stub that
// ASSERTS the body shape matches what the production handlers
// actually read. They document the wire contract explicitly so
// future refactors can't silently drift from it again.

// TestWireContract_CreateWorkflow pins the top-level workflow
// schema the gateway's createWorkflowRequest expects. NO outer
// `spec` wrapper — that was the QA-flagged defect.
func TestWireContract_CreateWorkflow(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{"id": "wf-1", "version": "v1"})
	b := mutatingBridge(t, srv)
	_, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{
		ID:          "wf-hello",
		Name:        "Hello Workflow",
		Description: "Minimal demo",
		OrgID:       "acme",
		TeamID:      "platform",
		Version:     "1.0",
		TimeoutSec:  300,
		Steps:       map[string]any{"log": map[string]any{"type": "log", "input": map[string]any{"message": "hello"}}},
		Config:      map[string]any{"retry_limit": 3},
		Parameters:  []map[string]any{{"name": "subject", "type": "string"}},
		InputSchema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Required top-level field assertions matching handleCreateWorkflow
	// in core/controlplane/gateway/handlers_workflows.go.
	requireField(t, captured.body, "id", "wf-hello")
	requireField(t, captured.body, "name", "Hello Workflow")
	requireField(t, captured.body, "description", "Minimal demo")
	requireField(t, captured.body, "org_id", "acme")
	requireField(t, captured.body, "team_id", "platform")
	requireField(t, captured.body, "version", "1.0")
	if _, ok := captured.body["steps"].(map[string]any); !ok {
		t.Fatalf("steps must be a top-level object: %#v", captured.body)
	}
	if _, ok := captured.body["config"].(map[string]any); !ok {
		t.Fatalf("config must be a top-level object: %#v", captured.body)
	}
	// No stale `spec` wrapper.
	if _, wrapped := captured.body["spec"]; wrapped {
		t.Fatalf("wire body must NOT carry a 'spec' key — gateway ignores it")
	}
}

// TestWireContract_InstallPack pins the marketplace-install JSON
// shape. Key check: bridge targets /api/v1/marketplace/install, NOT
// /api/v1/packs/install (which is multipart-only).
func TestWireContract_InstallPack(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{"installed": true, "pack_id": "cordum/slack"})
	b := mutatingBridge(t, srv)
	if _, err := b.InstallPack(context.Background(), InstallPackInput{
		CatalogID: "default",
		PackID:    "cordum/slack",
		Version:   "1.2.3",
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if captured.path != "/api/v1/marketplace/install" {
		t.Fatalf("wrong path: %s (expected marketplace/install)", captured.path)
	}
	requireField(t, captured.body, "catalog_id", "default")
	requireField(t, captured.body, "pack_id", "cordum/slack")
	requireField(t, captured.body, "version", "1.2.3")
}

// TestWireContract_InstallPack_DirectURL pins the URL+sha256 path
// for private / air-gapped installs.
func TestWireContract_InstallPack_DirectURL(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{"installed": true})
	b := mutatingBridge(t, srv)
	if _, err := b.InstallPack(context.Background(), InstallPackInput{
		URL:    "https://example.com/pack.tar.gz",
		Sha256: "abc123",
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	requireField(t, captured.body, "url", "https://example.com/pack.tar.gz")
	requireField(t, captured.body, "sha256", "abc123")
}

// TestWireContract_RegisterAgent pins the createAgentRequest
// required fields (name, owner, risk_tier) that the previous bridge
// wasn't sending.
func TestWireContract_RegisterAgent(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"id":        "agt-new",
		"name":      "release-bot",
		"owner":     "acme",
		"risk_tier": "medium",
	})
	b := mutatingBridge(t, srv)
	if _, err := b.RegisterAgent(context.Background(), RegisterAgentInput{
		Name:          "release-bot",
		Description:   "CI bot",
		Owner:         "acme",
		Team:          "platform",
		RiskTier:      "medium",
		AllowedTopics: []string{"job.deploy"},
		AllowedPools:  []string{"deploy"},
		AllowedTools:  []string{"cordum_install_pack"},
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	requireField(t, captured.body, "name", "release-bot")
	requireField(t, captured.body, "owner", "acme")
	requireField(t, captured.body, "risk_tier", "medium")
	// Optional fields forwarded when present.
	if _, ok := captured.body["allowed_topics"].([]any); !ok {
		t.Fatalf("allowed_topics not forwarded: %#v", captured.body)
	}
	// Stale fields must NOT be sent.
	for _, stale := range []string{"id", "org_id", "labels"} {
		if _, present := captured.body[stale]; present {
			t.Fatalf("stale field %q must not be in body (old contract)", stale)
		}
	}
}

// TestWireContract_SetAgentScope_CarriesPreapprovedList pins that
// preapproved_mutating_tools is sent as an explicit array so the
// gateway can persist (or clear) the scope-preapproval list.
func TestWireContract_SetAgentScope_CarriesPreapprovedList(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"id":                         "agt-1",
		"allowed_tools":              []any{"cordum_list_jobs"},
		"preapproved_mutating_tools": []any{"cordum_install_pack"},
	})
	b := mutatingBridge(t, srv)
	out, err := b.SetAgentScope(context.Background(), SetAgentScopeInput{
		AgentID:                  "agt-1",
		AllowedTools:             []string{"cordum_list_jobs"},
		PreapprovedMutatingTools: []string{"cordum_install_pack"},
		Status:                   "active",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if captured.path != "/api/v1/agents/agt-1" {
		t.Fatalf("wrong path: %s", captured.path)
	}
	if captured.method != http.MethodPut {
		t.Fatalf("wrong method: %s", captured.method)
	}
	// preapproved_mutating_tools must be present as an array — empty
	// OR populated, never missing (that's how 'clear scope' works).
	arr, ok := captured.body["preapproved_mutating_tools"].([]any)
	if !ok {
		t.Fatalf("preapproved_mutating_tools must be an array in body: %#v", captured.body)
	}
	if len(arr) != 1 || arr[0] != "cordum_install_pack" {
		t.Fatalf("wrong preapproved list: %#v", arr)
	}
	// Output parses the gateway response.
	if len(out.PreapprovedMutatingTools) != 1 {
		t.Fatalf("output did not decode preapproved list: %+v", out)
	}
}

// TestWireContract_RevokeWorkerSession pins the session-specific
// endpoint — NOT the credential-revoke DELETE which is a broader
// operation.
func TestWireContract_RevokeWorkerSession(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{"revoked": true, "worker_id": "w-1"})
	b := mutatingBridge(t, srv)
	if err := b.RevokeWorkerSession(context.Background(), RevokeWorkerSessionInput{
		WorkerID: "w-1",
		Reason:   "rotated at 2026-04-19",
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if captured.method != http.MethodPost {
		t.Fatalf("wrong method: %s (expected POST — session-revoke, not credential-DELETE)", captured.method)
	}
	if captured.path != "/api/v1/workers/w-1/revoke-session" {
		t.Fatalf("wrong path: %s", captured.path)
	}
	// Reason travels in the JSON body (the handler surfaces it on the
	// worker_trust_change SIEMEvent).
	requireField(t, captured.body, "reason", "rotated at 2026-04-19")
}

// requireField asserts that body[key] equals want. Used to keep the
// wire-contract tests readable.
func requireField(t *testing.T, body map[string]any, key, want string) {
	t.Helper()
	got, ok := body[key]
	if !ok {
		t.Fatalf("body missing required field %q: %s", key, dumpBody(body))
	}
	if s, isStr := got.(string); !isStr || s != want {
		t.Fatalf("body[%q] = %v, want %q: %s", key, got, want, dumpBody(body))
	}
}

func dumpBody(body map[string]any) string {
	raw, _ := json.Marshal(body)
	return strings.ReplaceAll(string(raw), ",", ", ")
}
