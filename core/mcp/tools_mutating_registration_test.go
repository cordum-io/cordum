package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Metadata pinning: every mutating tool must be gated, tagged, and
// tiered correctly per the epic's safe-closed default. This test
// doubles as documentation — if the definition of a tool changes,
// it must change here too.

func TestMutatingTools_ApprovalMetadata(t *testing.T) {
	t.Parallel()
	specs := mutatingToolSpecs(&mockServiceBridge{})

	want := map[string]struct {
		scope    string
		riskTier string
	}{
		ToolCreateWorkflow:      {ApprovalScopeWrite, "medium"},
		ToolInstallPack:         {ApprovalScopeWrite, "medium"},
		ToolUninstallPack:       {ApprovalScopeWriteAdmin, "high"},
		ToolRegisterAgent:       {ApprovalScopeWriteAdmin, "high"},
		ToolUpdatePolicyBundle:  {ApprovalScopeWriteAdmin, "high"},
		ToolRevokeWorkerSession: {ApprovalScopeWriteAdmin, "high"},
		ToolSetAgentScope:       {ApprovalScopeWriteAdmin, "high"},
	}

	if len(specs) != len(want) {
		t.Fatalf("expected %d mutating tools, got %d", len(want), len(specs))
	}

	seen := map[string]bool{}
	for _, spec := range specs {
		w, ok := want[spec.tool.Name]
		if !ok {
			t.Fatalf("unexpected tool %q in mutating surface", spec.tool.Name)
		}
		if !spec.tool.RequiresApproval {
			t.Errorf("%s: RequiresApproval must be true (epic rail)", spec.tool.Name)
		}
		if spec.tool.ApprovalScope != w.scope {
			t.Errorf("%s: approval scope = %q, want %q", spec.tool.Name, spec.tool.ApprovalScope, w.scope)
		}
		if spec.tool.RiskTier != w.riskTier {
			t.Errorf("%s: risk tier = %q, want %q", spec.tool.Name, spec.tool.RiskTier, w.riskTier)
		}
		if !containsTag(spec.tool.Tags, "mutating") {
			t.Errorf("%s: Tags must include 'mutating'", spec.tool.Name)
		}
		if !strings.Contains(spec.tool.Description, "approval") {
			t.Errorf("%s: Description must mention approval flow for LLM consumption", spec.tool.Name)
		}
		if !strings.Contains(spec.tool.Description, "-32099") {
			t.Errorf("%s: Description must mention JSON-RPC -32099 code", spec.tool.Name)
		}
		seen[spec.tool.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("mutating tool %q never registered", name)
		}
	}
}

func containsTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

// Registration test — all 7 mutating tools must land in the registry
// after RegisterAllTools and be returned by ListTools.
func TestMutatingTools_RegisterAllTools_Includes7Mutators(t *testing.T) {
	t.Parallel()
	reg := NewToolRegistry()
	if err := RegisterAllTools(reg, &mockServiceBridge{}); err != nil {
		t.Fatalf("RegisterAllTools err: %v", err)
	}
	expected := []string{
		ToolCreateWorkflow,
		ToolInstallPack,
		ToolUninstallPack,
		ToolRegisterAgent,
		ToolUpdatePolicyBundle,
		ToolRevokeWorkerSession,
		ToolSetAgentScope,
	}
	list := reg.ListToolsUnfiltered()
	seen := map[string]Tool{}
	for _, t := range list {
		seen[t.Name] = t
	}
	for _, name := range expected {
		tool, ok := seen[name]
		if !ok {
			t.Errorf("tool %q missing from registry", name)
			continue
		}
		if !tool.RequiresApproval {
			t.Errorf("tool %q: RequiresApproval=false after RegisterAllTools", name)
		}
	}
}

// Handler contract test — each handler should forward the args into
// the bridge, surface the bridge's output as a JSON tool-call result,
// and map bridge errors into IsError=true results.

type stubMutatingBridge struct {
	mockServiceBridge
	createWorkflowCalled    bool
	installPackCalled       bool
	installPackErr          error
	updateBundleCalledWith  UpdatePolicyBundleInput
	lastIdempotencyKey      string
	setAgentScopeResponse   *SetAgentScopeOutput
	lastSetAgentScopeInput  SetAgentScopeInput
	lastRegisterAgentInput  RegisterAgentInput
	lastRevokeWorkerSession RevokeWorkerSessionInput
}

func (b *stubMutatingBridge) CreateWorkflow(_ context.Context, req CreateWorkflowInput) (*CreateWorkflowOutput, error) {
	b.createWorkflowCalled = true
	b.lastIdempotencyKey = req.IdempotencyKey
	return &CreateWorkflowOutput{WorkflowID: "wf-xyz", Version: "v1"}, nil
}

func (b *stubMutatingBridge) InstallPack(_ context.Context, req InstallPackInput) (*InstallPackOutput, error) {
	b.installPackCalled = true
	if b.installPackErr != nil {
		return nil, b.installPackErr
	}
	return &InstallPackOutput{PackID: req.PackID, Version: req.Version, Installed: true}, nil
}

func (b *stubMutatingBridge) UpdatePolicyBundle(_ context.Context, req UpdatePolicyBundleInput) (*UpdatePolicyBundleOutput, error) {
	b.updateBundleCalledWith = req
	return &UpdatePolicyBundleOutput{
		BundleID:  req.BundleID,
		UpdatedAt: "2026-04-19T00:00:00Z",
		Signed:    true,
		KeyID:     "prod-key",
	}, nil
}

func (b *stubMutatingBridge) SetAgentScope(_ context.Context, req SetAgentScopeInput) (*SetAgentScopeOutput, error) {
	b.lastSetAgentScopeInput = req
	if b.setAgentScopeResponse != nil {
		return b.setAgentScopeResponse, nil
	}
	return &SetAgentScopeOutput{
		AgentID:                  req.AgentID,
		AllowedTools:             req.AllowedTools,
		AllowedServers:           req.AllowedServers,
		AllowedResources:         req.AllowedResources,
		Entitlements:             req.Entitlements,
		PreapprovedMutatingTools: req.PreapprovedMutatingTools,
	}, nil
}

func (b *stubMutatingBridge) RegisterAgent(_ context.Context, req RegisterAgentInput) (*RegisterAgentOutput, error) {
	b.lastRegisterAgentInput = req
	return &RegisterAgentOutput{
		ID:         "agt-stub",
		Name:       req.Name,
		Owner:      req.Owner,
		RiskTier:   req.RiskTier,
		Registered: true,
	}, nil
}

func (b *stubMutatingBridge) RevokeWorkerSession(_ context.Context, req RevokeWorkerSessionInput) error {
	b.lastRevokeWorkerSession = req
	return nil
}

func (b *stubMutatingBridge) UninstallPack(_ context.Context, _ UninstallPackInput) error {
	return nil
}

func TestCreateWorkflowHandler_ForwardsStepsAndIdempotency(t *testing.T) {
	t.Parallel()
	bridge := &stubMutatingBridge{}
	handler := createWorkflowHandler(bridge)
	res, err := handler(context.Background(), json.RawMessage(`{"name":"demo","steps":{"log":{"type":"log"}},"idempotency_key":"idem-1"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected ok result, got %+v", res)
	}
	if !bridge.createWorkflowCalled {
		t.Fatalf("bridge not called")
	}
	if bridge.lastIdempotencyKey != "idem-1" {
		t.Fatalf("idempotency key not forwarded: %q", bridge.lastIdempotencyKey)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "wf-xyz") {
		t.Fatalf("workflow_id not in result: %+v", res)
	}
}

func TestCreateWorkflowHandler_RejectsEmptySteps(t *testing.T) {
	t.Parallel()
	handler := createWorkflowHandler(&stubMutatingBridge{})
	_, err := handler(context.Background(), json.RawMessage(`{"name":"demo"}`))
	if err == nil {
		t.Fatalf("expected error for missing steps")
	}
	if !strings.Contains(err.Error(), "steps are required") {
		t.Fatalf("wrong err: %v", err)
	}
}

func TestInstallPackHandler_MapsBridgeErrorToIsError(t *testing.T) {
	t.Parallel()
	bridge := &stubMutatingBridge{
		installPackErr: NewBridgeError(http.StatusConflict, "already_installed", "pack already installed", nil),
	}
	handler := installPackHandler(bridge)
	res, err := handler(context.Background(), json.RawMessage(`{"pack_id":"cordum/slack"}`))
	if err != nil {
		t.Fatalf("unexpected top-level err: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("expected IsError=true, got %+v", res)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "already_installed") {
		t.Fatalf("bridge error code not surfaced: %+v", res)
	}
}

func TestUpdatePolicyBundleHandler_ForwardsContentAndSurfacesSignature(t *testing.T) {
	t.Parallel()
	bridge := &stubMutatingBridge{}
	handler := updatePolicyBundleHandler(bridge)
	res, err := handler(context.Background(), json.RawMessage(`{"bundle_id":"secops/core","content":"rules:[]","author":"op","enabled":true}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("expected ok, got %+v", res)
	}
	if bridge.updateBundleCalledWith.BundleID != "secops/core" {
		t.Fatalf("bundle_id not forwarded")
	}
	if bridge.updateBundleCalledWith.Enabled == nil || !*bridge.updateBundleCalledWith.Enabled {
		t.Fatalf("enabled pointer not forwarded")
	}
	if !strings.Contains(res.Content[0].Text, "\"signed\":true") {
		t.Fatalf("signed flag missing from response: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "prod-key") {
		t.Fatalf("key_id missing from response")
	}
}

func TestRegisterAgentHandler_ForwardsMCPAllowlists(t *testing.T) {
	t.Parallel()
	bridge := &stubMutatingBridge{}
	handler := registerAgentHandler(bridge)
	_, err := handler(context.Background(), json.RawMessage(`{
		"name":"repo-bot","owner":"platform","risk_tier":"high",
		"allowed_servers":["prod-mcp"],"allowed_tools":["repo.*"],
		"allowed_resources":["cordum://repos/*"],"entitlements":["repo.read"]
	}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(bridge.lastRegisterAgentInput.AllowedServers) != 1 {
		t.Fatalf("allowed_servers not forwarded: %+v", bridge.lastRegisterAgentInput)
	}
	if len(bridge.lastRegisterAgentInput.AllowedResources) != 1 {
		t.Fatalf("allowed_resources not forwarded: %+v", bridge.lastRegisterAgentInput)
	}
	if len(bridge.lastRegisterAgentInput.Entitlements) != 1 {
		t.Fatalf("entitlements not forwarded: %+v", bridge.lastRegisterAgentInput)
	}
}

func TestSetAgentScopeHandler_SendsBothScopeLists(t *testing.T) {
	t.Parallel()
	bridge := &stubMutatingBridge{}
	handler := setAgentScopeHandler(bridge)
	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"a",
		"allowed_tools":["x","y"],
		"allowed_servers":["prod-mcp"],
		"allowed_resources":["cordum://repos/*"],
		"entitlements":["repo.read"],
		"preapproved_mutating_tools":["cordum_install_pack"]
	}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(bridge.lastSetAgentScopeInput.AllowedTools) != 2 {
		t.Fatalf("allowed_tools not forwarded: %+v", bridge.lastSetAgentScopeInput)
	}
	if len(bridge.lastSetAgentScopeInput.PreapprovedMutatingTools) != 1 {
		t.Fatalf("preapproved_mutating_tools not forwarded: %+v", bridge.lastSetAgentScopeInput)
	}
	if len(bridge.lastSetAgentScopeInput.AllowedServers) != 1 {
		t.Fatalf("allowed_servers not forwarded: %+v", bridge.lastSetAgentScopeInput)
	}
	if len(bridge.lastSetAgentScopeInput.AllowedResources) != 1 {
		t.Fatalf("allowed_resources not forwarded: %+v", bridge.lastSetAgentScopeInput)
	}
	if len(bridge.lastSetAgentScopeInput.Entitlements) != 1 {
		t.Fatalf("entitlements not forwarded: %+v", bridge.lastSetAgentScopeInput)
	}
}
