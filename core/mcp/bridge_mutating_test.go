package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each test below stands up a small httptest.Server that records the
// request and returns a canned JSON body. HTTPServiceBridge is pointed
// at the server and we assert the wire shape + idempotency-key
// forwarding + output decoding path.

type capturedRequest struct {
	method string
	path   string
	header http.Header
	body   map[string]any
}

func newStubGateway(t *testing.T, status int, response map[string]any) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.RequestURI()
		captured.header = r.Header.Clone()
		defer func() { _ = r.Body.Close() }()
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &captured.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if response != nil {
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func mutatingBridge(t *testing.T, srv *httptest.Server) *HTTPServiceBridge {
	t.Helper()
	return NewHTTPServiceBridge(HTTPServiceBridgeConfig{
		BaseURL:           srv.URL,
		TenantID:          "tenant-A",
		AllowPrivateHosts: true,
	})
}

func TestHTTPBridge_CreateWorkflow_HappyPath(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusCreated, map[string]any{
		"id":      "wf-123",
		"version": "v1",
	})
	b := mutatingBridge(t, srv)
	out, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{
		Name:           "demo",
		Description:    "quickstart",
		Steps:          map[string]any{"log": map[string]any{"type": "log"}},
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == nil || out.WorkflowID != "wf-123" {
		t.Fatalf("wrong output: %+v", out)
	}
	if captured.method != http.MethodPost {
		t.Fatalf("wrong method: %s", captured.method)
	}
	if captured.path != "/api/v1/workflows" {
		t.Fatalf("wrong path: %s", captured.path)
	}
	if captured.header.Get("Idempotency-Key") != "idem-1" {
		t.Fatalf("idempotency key not forwarded: %q", captured.header.Get("Idempotency-Key"))
	}
	if captured.body["name"] != "demo" {
		t.Fatalf("name not in body: %v", captured.body)
	}
	// Steps must reach the gateway at the TOP level (NOT nested under
	// `spec`) — regression for the QA reopen.
	if _, ok := captured.body["steps"].(map[string]any); !ok {
		t.Fatalf("steps must be top-level in body, got %#v", captured.body)
	}
	if _, wrapped := captured.body["spec"]; wrapped {
		t.Fatalf("wire body must NOT wrap fields in 'spec' (see handleCreateWorkflow createWorkflowRequest shape)")
	}
}

func TestHTTPBridge_CreateWorkflow_MissingSpec(t *testing.T) {
	t.Parallel()
	srv, _ := newStubGateway(t, http.StatusOK, nil)
	b := mutatingBridge(t, srv)
	_, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{})
	if err == nil {
		t.Fatalf("expected error for missing spec")
	}
	var be *BridgeError
	if !asBridgeError(err, &be) || be.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 BridgeError, got %v", err)
	}
}

func TestHTTPBridge_InstallPack(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"pack_id":   "cordum/slack",
		"version":   "1.2.3",
		"installed": true,
	})
	b := mutatingBridge(t, srv)
	out, err := b.InstallPack(context.Background(), InstallPackInput{
		PackID:         "cordum/slack",
		Version:        "1.2.3",
		IdempotencyKey: "idem-p1",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out == nil || !out.Installed || out.PackID != "cordum/slack" {
		t.Fatalf("wrong output: %+v", out)
	}
	// Must target the JSON marketplace endpoint, NOT
	// /api/v1/packs/install (which expects a multipart bundle).
	if captured.path != "/api/v1/marketplace/install" {
		t.Fatalf("wrong path: %s", captured.path)
	}
}

func TestHTTPBridge_InstallPack_ByURL_RequiresSha256(t *testing.T) {
	t.Parallel()
	srv, _ := newStubGateway(t, http.StatusOK, map[string]any{"installed": true})
	b := mutatingBridge(t, srv)
	// URL without sha256 → immediate client-side error (no round-trip).
	if _, err := b.InstallPack(context.Background(), InstallPackInput{
		URL: "https://example.com/pack.tar.gz",
	}); err == nil {
		t.Fatalf("expected error when url has no sha256")
	}
}

func TestHTTPBridge_UninstallPack(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{"ok": true})
	b := mutatingBridge(t, srv)
	if err := b.UninstallPack(context.Background(), UninstallPackInput{
		PackID:         "cordum/slack",
		Reason:         "deprecated",
		IdempotencyKey: "idem-u1",
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(captured.path, "/packs/cordum%2Fslack/uninstall") {
		t.Fatalf("path did not escape pack_id: %s", captured.path)
	}
	if captured.body["reason"] != "deprecated" {
		t.Fatalf("reason not forwarded: %v", captured.body)
	}
}

func TestHTTPBridge_RegisterAgent(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"id":        "agt-1",
		"name":      "Quickstart bot",
		"owner":     "acme",
		"risk_tier": "medium",
	})
	b := mutatingBridge(t, srv)
	out, err := b.RegisterAgent(context.Background(), RegisterAgentInput{
		Name:         "Quickstart bot",
		Owner:        "acme",
		RiskTier:     "medium",
		AllowedTools: []string{"cordum_list_jobs"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out == nil || out.ID != "agt-1" || !out.Registered {
		t.Fatalf("wrong output: %+v", out)
	}
	if captured.path != "/api/v1/agents" {
		t.Fatalf("wrong path: %s", captured.path)
	}
	// Required fields land in body.
	if captured.body["name"] != "Quickstart bot" {
		t.Fatalf("name not forwarded: %v", captured.body)
	}
	if captured.body["owner"] != "acme" {
		t.Fatalf("owner not forwarded: %v", captured.body)
	}
	if captured.body["risk_tier"] != "medium" {
		t.Fatalf("risk_tier not forwarded: %v", captured.body)
	}
	if tools, _ := captured.body["allowed_tools"].([]any); len(tools) != 1 {
		t.Fatalf("allowed_tools not forwarded: %v", captured.body)
	}
}

func TestHTTPBridge_RegisterAgent_RequiresCoreFields(t *testing.T) {
	t.Parallel()
	srv, _ := newStubGateway(t, http.StatusOK, nil)
	b := mutatingBridge(t, srv)
	cases := []RegisterAgentInput{
		{Owner: "o", RiskTier: "low"},                // no name
		{Name: "n", RiskTier: "low"},                 // no owner
		{Name: "n", Owner: "o"},                      // no risk_tier
	}
	for i, c := range cases {
		if _, err := b.RegisterAgent(context.Background(), c); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}

func TestHTTPBridge_UpdatePolicyBundle_SignatureSurfaced(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"id":         "secops/core",
		"updated_at": "2026-04-19T00:00:00Z",
		"signature": map[string]any{
			"key_id": "prod-key-1",
			"value":  "sig-bytes",
		},
	})
	b := mutatingBridge(t, srv)
	enabled := true
	out, err := b.UpdatePolicyBundle(context.Background(), UpdatePolicyBundleInput{
		BundleID:       "secops/core",
		Content:        "rules: []",
		Author:         "operator-a",
		Enabled:        &enabled,
		IdempotencyKey: "idem-pb-1",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.BundleID != "secops/core" || !out.Signed || out.KeyID != "prod-key-1" {
		t.Fatalf("wrong output: %+v", out)
	}
	if !strings.Contains(captured.path, "/api/v1/policy/bundles/") {
		t.Fatalf("wrong path: %s", captured.path)
	}
	// bundleID 'secops/core' should be re-encoded as 'secops~core'
	// (BundleIDFromRequest reverses). Then url.PathEscape keeps '~'.
	if !strings.Contains(captured.path, "secops~core") {
		t.Fatalf("bundle id not tilde-encoded: %s", captured.path)
	}
	if captured.method != http.MethodPut {
		t.Fatalf("wrong method: %s", captured.method)
	}
}

func TestHTTPBridge_UpdatePolicyBundle_ValidationErrors(t *testing.T) {
	t.Parallel()
	srv, _ := newStubGateway(t, http.StatusOK, nil)
	b := mutatingBridge(t, srv)
	if _, err := b.UpdatePolicyBundle(context.Background(), UpdatePolicyBundleInput{Content: "x"}); err == nil {
		t.Fatalf("expected error for missing bundle_id")
	}
	if _, err := b.UpdatePolicyBundle(context.Background(), UpdatePolicyBundleInput{BundleID: "b", Content: "  "}); err == nil {
		t.Fatalf("expected error for missing content")
	}
}

func TestHTTPBridge_RevokeWorkerSession(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"worker_id": "w-42",
		"revoked":   true,
	})
	b := mutatingBridge(t, srv)
	if err := b.RevokeWorkerSession(context.Background(), RevokeWorkerSessionInput{
		WorkerID: "w-42",
		Reason:   "rotated",
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	// Must be POST to the session-specific endpoint, not DELETE on
	// /workers/credentials/{id} (which revokes the whole credential).
	if captured.method != http.MethodPost {
		t.Fatalf("wrong method: %s (expected POST to /revoke-session)", captured.method)
	}
	if captured.path != "/api/v1/workers/w-42/revoke-session" {
		t.Fatalf("wrong path: %s", captured.path)
	}
	if captured.body["reason"] != "rotated" {
		t.Fatalf("reason not in body: %v", captured.body)
	}
}

func TestHTTPBridge_SetAgentScope(t *testing.T) {
	t.Parallel()
	srv, captured := newStubGateway(t, http.StatusOK, map[string]any{
		"id":                         "agt-1",
		"allowed_tools":              []any{"cordum_list_jobs", "cordum_get_job"},
		"preapproved_mutating_tools": []any{"cordum_install_pack"},
	})
	b := mutatingBridge(t, srv)
	out, err := b.SetAgentScope(context.Background(), SetAgentScopeInput{
		AgentID:                  "agt-1",
		AllowedTools:             []string{"cordum_list_jobs", "cordum_get_job"},
		PreapprovedMutatingTools: []string{"cordum_install_pack"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.AgentID != "agt-1" || len(out.AllowedTools) != 2 || len(out.PreapprovedMutatingTools) != 1 {
		t.Fatalf("wrong output: %+v", out)
	}
	if captured.method != http.MethodPut {
		t.Fatalf("wrong method: %s", captured.method)
	}
	if captured.path != "/api/v1/agents/agt-1" {
		t.Fatalf("wrong path: %s", captured.path)
	}
}

func TestHTTPBridge_MutatingGatewayErrorMapping(t *testing.T) {
	t.Parallel()
	srv, _ := newStubGateway(t, http.StatusConflict, map[string]any{
		"error": "pack already installed",
		"code":  "already_installed",
	})
	b := mutatingBridge(t, srv)
	_, err := b.InstallPack(context.Background(), InstallPackInput{PackID: "cordum/slack"})
	if err == nil {
		t.Fatalf("expected error")
	}
	var be *BridgeError
	if !asBridgeError(err, &be) || be.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 BridgeError, got %v", err)
	}
	if be.Code != "already_installed" {
		t.Fatalf("wrong code: %q", be.Code)
	}
}

func TestHTTPBridge_NilReceiverReturnsBridgeUnavailable(t *testing.T) {
	t.Parallel()
	var b *HTTPServiceBridge
	if _, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{Steps: map[string]any{"x": map[string]any{"type": "log"}}}); err != ErrBridgeUnavailable {
		t.Fatalf("expected ErrBridgeUnavailable, got %v", err)
	}
	if err := b.UninstallPack(context.Background(), UninstallPackInput{PackID: "p"}); err != ErrBridgeUnavailable {
		t.Fatalf("expected ErrBridgeUnavailable, got %v", err)
	}
}

func TestDirectBridge_MutatingHooks(t *testing.T) {
	t.Parallel()
	called := map[string]bool{}
	b := NewDirectServiceBridge(DirectServiceBridgeConfig{
		CreateWorkflowFunc: func(context.Context, CreateWorkflowInput) (*CreateWorkflowOutput, error) {
			called["create"] = true
			return &CreateWorkflowOutput{WorkflowID: "ok"}, nil
		},
		InstallPackFunc: func(context.Context, InstallPackInput) (*InstallPackOutput, error) {
			called["install"] = true
			return &InstallPackOutput{PackID: "p", Installed: true}, nil
		},
	})
	if _, err := b.CreateWorkflow(context.Background(), CreateWorkflowInput{Steps: map[string]any{"x": map[string]any{"type": "log"}}}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, err := b.InstallPack(context.Background(), InstallPackInput{PackID: "p"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	// Hook not configured → ErrBridgeUnavailable.
	if _, err := b.RegisterAgent(context.Background(), RegisterAgentInput{Name: "a", Owner: "o", RiskTier: "low"}); err != ErrBridgeUnavailable {
		t.Fatalf("expected ErrBridgeUnavailable, got %v", err)
	}
	if !called["create"] || !called["install"] {
		t.Fatalf("hooks not invoked: %+v", called)
	}
}

// asBridgeError is a small helper — errors.As with a typed pointer.
func asBridgeError(err error, target **BridgeError) bool {
	type asErr interface{ As(any) bool }
	if err == nil {
		return false
	}
	if be, ok := err.(*BridgeError); ok {
		*target = be
		return true
	}
	if ae, ok := err.(asErr); ok {
		return ae.As(target)
	}
	return false
}
