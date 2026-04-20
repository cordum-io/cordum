package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestToolRegistry_RuntimePolicy_Tightens(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()

	// Code registers fs.write at medium tier.
	if err := r.Register(Tool{Name: "fs.write", RiskTier: "medium"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Baseline: a medium-tier identity sees fs.write.
	mediumID := &AgentIdentity{ID: "m", RiskTier: "medium", AllowedTools: []string{"*"}}
	ctx := ContextWithIdentity(context.Background(), mediumID)
	if got := r.ListTools(ctx); len(got) != 1 {
		t.Fatalf("baseline: medium actor should see fs.write, got %v", toolNames(got))
	}

	// Runtime raises the required tier to high.
	r.SetConfig(map[string]any{
		"mcp_tool_policy": map[string]any{
			"tools": []any{
				map[string]any{
					"tool_pattern":  "fs.*",
					"min_risk_tier": "high",
				},
			},
		},
	})

	if got := r.ListTools(ctx); len(got) != 0 {
		t.Fatalf("after tightening: medium actor must NOT see fs.write, got %v", toolNames(got))
	}

	// High-tier actor still sees it.
	highCtx := ContextWithIdentity(context.Background(), &AgentIdentity{ID: "h", RiskTier: "high", AllowedTools: []string{"*"}})
	if got := r.ListTools(highCtx); len(got) != 1 {
		t.Fatalf("after tightening: high actor must still see fs.write, got %v", toolNames(got))
	}
}

func TestToolRegistry_RuntimePolicy_DoesNotLoosen(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()

	// Code registers nuke at critical tier.
	if err := r.Register(Tool{Name: "nuke.everything", RiskTier: "critical"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Runtime attempts to loosen to "low" — must be rejected.
	r.SetConfig(map[string]any{
		"mcp_tool_policy": map[string]any{
			"tools": []any{
				map[string]any{
					"tool_pattern":  "nuke.*",
					"min_risk_tier": "low",
				},
			},
		},
	})

	lowCtx := ContextWithIdentity(context.Background(), &AgentIdentity{ID: "l", RiskTier: "low", AllowedTools: []string{"*"}})
	if got := r.ListTools(lowCtx); len(got) != 0 {
		t.Fatalf("runtime must NOT loosen: low actor should still not see critical tool, got %v", toolNames(got))
	}

	critCtx := ContextWithIdentity(context.Background(), &AgentIdentity{ID: "c", RiskTier: "critical", AllowedTools: []string{"*"}})
	if got := r.ListTools(critCtx); len(got) != 1 {
		t.Fatalf("critical actor should still see tool: got %v", toolNames(got))
	}
}

func TestToolRegistry_RuntimePolicy_AddsRequiredClassification(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()

	if err := r.Register(Tool{Name: "data.read", RiskTier: "low"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Baseline: any allowed actor sees the tool.
	baseID := &AgentIdentity{ID: "a", RiskTier: "high", AllowedTools: []string{"*"}}
	if got := r.ListTools(ContextWithIdentity(context.Background(), baseID)); len(got) != 1 {
		t.Fatalf("baseline: got %v", toolNames(got))
	}

	// Runtime adds a required classification (pii).
	r.SetConfig(map[string]any{
		"mcp_tool_policy": map[string]any{
			"tools": []any{
				map[string]any{
					"tool_pattern":                  "data.read",
					"required_data_classifications": []any{"pii"},
				},
			},
		},
	})

	if got := r.ListTools(ContextWithIdentity(context.Background(), baseID)); len(got) != 0 {
		t.Fatalf("after adding required pii: actor without pii must NOT see tool, got %v", toolNames(got))
	}

	piiID := &AgentIdentity{ID: "a", RiskTier: "high", AllowedTools: []string{"*"}, DataClassifications: []string{"pii"}}
	if got := r.ListTools(ContextWithIdentity(context.Background(), piiID)); len(got) != 1 {
		t.Fatalf("pii actor should see tool: got %v", toolNames(got))
	}
}

func TestToolRegistry_ListTools_FiltersByIdentity(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()

	handler := func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) { return &ToolCallResult{}, nil }
	tools := []Tool{
		{Name: "fs.read", RiskTier: "low"},
		{Name: "fs.write", RiskTier: "medium"},
		{Name: "jobs.submit", RiskTier: "medium"},
		{Name: "jobs.delete", RiskTier: "high"},
		{Name: "nuke.everything", RiskTier: "critical"},
	}
	for _, tool := range tools {
		if err := r.Register(tool, handler); err != nil {
			t.Fatalf("register %s: %v", tool.Name, err)
		}
	}

	// Missing identity → empty list (fail-closed).
	if got := r.ListTools(context.Background()); len(got) != 0 {
		t.Fatalf("no identity should yield zero tools, got %d", len(got))
	}

	// Underprivileged (medium tier, fs.*) sees only low/medium fs tools.
	under := &AgentIdentity{
		ID:           "under",
		RiskTier:     "medium",
		AllowedTools: []string{"fs.*"},
	}
	ctx := ContextWithIdentity(context.Background(), under)
	got := r.ListTools(ctx)
	names := toolNames(got)
	want := []string{"fs.read", "fs.write"}
	if len(names) != len(want) {
		t.Fatalf("underprivileged: want %v, got %v", want, names)
	}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("underprivileged[%d]: want %q, got %q", i, n, names[i])
		}
	}

	// Admin identity → full catalogue.
	admin := &AgentIdentity{
		ID:                  "admin",
		RiskTier:            "critical",
		AllowedTools:        []string{"*"},
		DataClassifications: []string{"pii", "phi"},
	}
	ctx = ContextWithIdentity(context.Background(), admin)
	if got := r.ListTools(ctx); len(got) != len(tools) {
		t.Fatalf("admin should see all %d tools, got %d", len(tools), len(got))
	}

	// Diagnostic path returns full catalogue regardless of ctx.
	if got := r.ListToolsUnfiltered(); len(got) != len(tools) {
		t.Fatalf("ListToolsUnfiltered: want %d, got %d", len(tools), len(got))
	}
}

// TestToolJSONBackwardCompat confirms (a) existing serialised tools
// without the new approval fields still decode cleanly, and (b) a tool
// with RequiresApproval=false (the default) does NOT emit the field on
// the wire — the JSON tag has omitempty so MCP clients that didn't
// know about the field continue to see the same descriptor as before.
func TestToolJSONBackwardCompat(t *testing.T) {
	t.Parallel()

	// Decode-side: a JSON document produced by an older Cordum version
	// (no requiresApproval / approvalScope fields) must populate Tool
	// without error and leave the new fields at their zero values.
	const legacy = `{"name":"jobs.submit","description":"submit a job","inputSchema":{"type":"object"}}`
	var tool Tool
	if err := json.Unmarshal([]byte(legacy), &tool); err != nil {
		t.Fatalf("unmarshal legacy tool: %v", err)
	}
	if tool.Name != "jobs.submit" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.RequiresApproval || tool.ApprovalScope != "" {
		t.Errorf("legacy tool decoded with non-zero approval fields: %+v", tool)
	}

	// Encode-side: a default tool (RequiresApproval=false) must omit the
	// new fields. Adding them unconditionally would surprise downstream
	// JSON-schema validators in older MCP clients.
	plain := Tool{Name: "jobs.submit", Description: "x"}
	raw, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, ok := asMap["requiresApproval"]; ok {
		t.Errorf("requiresApproval should be omitted when false: %s", string(raw))
	}
	if _, ok := asMap["approvalScope"]; ok {
		t.Errorf("approvalScope should be omitted when empty: %s", string(raw))
	}

	// Encode-side: when RequiresApproval=true, the field must appear so
	// the runtime config and other Cordum components can read it.
	gated := Tool{Name: "x", RequiresApproval: true, ApprovalScope: "dangerous"}
	raw, err = json.Marshal(gated)
	if err != nil {
		t.Fatalf("marshal gated: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("invalid JSON: %s", raw)
	}
	asMap = nil
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("re-decode gated: %v", err)
	}
	if v, _ := asMap["requiresApproval"].(bool); !v {
		t.Errorf("requiresApproval missing/false in gated tool: %s", raw)
	}
	if v, _ := asMap["approvalScope"].(string); v != "dangerous" {
		t.Errorf("approvalScope = %v, want dangerous", asMap["approvalScope"])
	}
}

// fakeGate is a test double for ApprovalGate. It records invocations
// and replays a pre-programmed response.
type fakeGate struct {
	calls    int
	response *ApprovalRequired
	err      error
	lastTool Tool
}

func (f *fakeGate) Check(_ context.Context, tool Tool, _ json.RawMessage) (*ApprovalRequired, error) {
	f.calls++
	f.lastTool = tool
	return f.response, f.err
}

// TestApprovalGateCalledForGatedTool asserts the gate runs exactly once
// when a tool has RequiresApproval=true, and that its ApprovalRequired
// flows back out as an error with the tool name populated.
func TestApprovalGateCalledForGatedTool(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	if err := r.Register(Tool{Name: "dangerous.delete", RequiresApproval: true}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("handler must not run when gated")
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	gate := &fakeGate{response: &ApprovalRequired{ApprovalID: "app-xyz", Reason: "dangerous rule matched"}}
	r.SetApprovalGate(gate)

	_, err := r.Call(context.Background(), "dangerous.delete", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected ApprovalRequired error, got nil")
	}
	var got *ApprovalRequired
	if !errors.As(err, &got) {
		t.Fatalf("expected *ApprovalRequired, got %T: %v", err, err)
	}
	if got.ApprovalID != "app-xyz" {
		t.Errorf("approval_id = %q", got.ApprovalID)
	}
	if got.Tool != "dangerous.delete" {
		t.Errorf("tool = %q (gate should fill this from registry)", got.Tool)
	}
	if gate.calls != 1 {
		t.Errorf("gate invoked %d times, want 1", gate.calls)
	}
}

// TestApprovalGateSkippedWhenNotGated confirms the happy path: tools
// without RequiresApproval run their handler directly and never touch
// the gate.
func TestApprovalGateSkippedWhenNotGated(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	handlerRan := false
	_ = r.Register(Tool{Name: "safe.read"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		handlerRan = true
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	})
	gate := &fakeGate{}
	r.SetApprovalGate(gate)

	if _, err := r.Call(context.Background(), "safe.read", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !handlerRan {
		t.Error("handler did not run")
	}
	if gate.calls != 0 {
		t.Errorf("gate invoked %d times, want 0 (tool not gated)", gate.calls)
	}
}

// TestApprovalGatePreApproved simulates the step-5 pre-approval path:
// the gate returns (nil, nil), indicating a valid consumed pre-approval
// exists, so the handler should execute as if no gate were present.
func TestApprovalGatePreApproved(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	handlerRan := false
	_ = r.Register(Tool{Name: "dangerous.delete", RequiresApproval: true}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		handlerRan = true
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "done"}}}, nil
	})
	// gate.response == nil signals pre-approval was found+consumed.
	gate := &fakeGate{response: nil, err: nil}
	r.SetApprovalGate(gate)

	if _, err := r.Call(context.Background(), "dangerous.delete", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gate.calls != 1 {
		t.Errorf("gate invoked %d times, want 1", gate.calls)
	}
	if !handlerRan {
		t.Error("handler should have run after gate returned allow")
	}
}

// TestApprovalGateNilSkipsCheck covers the dev/test path where no gate
// is wired — gated tools still run their handler so stdio-only deploys
// don't need the gateway's approval infrastructure.
func TestApprovalGateNilSkipsCheck(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	handlerRan := false
	_ = r.Register(Tool{Name: "dangerous.delete", RequiresApproval: true}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		handlerRan = true
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	})
	// No SetApprovalGate call — registry.gate is nil.
	if _, err := r.Call(context.Background(), "dangerous.delete", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("unexpected error with nil gate: %v", err)
	}
	if !handlerRan {
		t.Error("handler should run when no gate is wired")
	}
}

// TestGlobMatchCoversExpectedPatterns table-tests the tiny glob matcher
// so future edits can't silently break pattern rules operators rely on.
func TestGlobMatchCoversExpectedPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"dangerous.*", "dangerous.delete", true},
		{"dangerous.*", "dangerous", false},
		{"dangerous.*", "safe.read", false},
		{"*", "anything", true},
		{"files.*.delete", "files.batch.delete", true},
		{"files.*.delete", "files.batch.read", false},
		{"fs/*", "fs/read", true},       // path.Match rejects this; we must not.
		{"fs/*", "fs/sub/read", true},   // greedy *
		{"exact", "exact", true},
		{"exact", "exacty", false},
		{"", "", true},
		{"*pre*", "contains_pre_something", true},
		{"*pre*", "no_match_here", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.input); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.input, got, tc.want)
		}
	}
}

// TestEffectiveApprovalRuntimeConfigWins confirms a runtime rule that
// sets requires_approval=true gates a tool that was registered without
// a gate — the whole purpose of the mcp_policy config.
func TestEffectiveApprovalRuntimeConfigWins(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	_ = r.Register(Tool{Name: "fs.delete"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("handler must not run — runtime rule gated this call")
		return nil, nil
	})
	r.SetConfig(map[string]any{
		"tools": []any{
			map[string]any{
				"tool_name_pattern": "fs.*",
				"requires_approval": true,
				"approval_scope":    "filesystem",
			},
		},
	})
	gate := &fakeGate{response: &ApprovalRequired{ApprovalID: "app-rt"}}
	r.SetApprovalGate(gate)

	_, err := r.Call(context.Background(), "fs.delete", json.RawMessage(`{}`))
	var gated *ApprovalRequired
	if !errors.As(err, &gated) {
		t.Fatalf("expected ApprovalRequired, got %v", err)
	}
	if gate.calls != 1 {
		t.Errorf("gate called %d times, want 1", gate.calls)
	}
	if gate.lastTool.ApprovalScope != "filesystem" {
		t.Errorf("gate received scope %q, want filesystem (runtime override should apply)", gate.lastTool.ApprovalScope)
	}
}

// TestEffectiveApprovalRuntimeConfigCanDisableGate ensures an operator
// can turn OFF a code-gated tool at runtime (e.g. signed CI bypass).
// First matching rule wins so rule ordering must be explicit.
func TestEffectiveApprovalRuntimeConfigCanDisableGate(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	handlerRan := false
	_ = r.Register(Tool{Name: "dangerous.delete", RequiresApproval: true}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		handlerRan = true
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	})
	r.SetConfig(map[string]any{
		"tools": []any{
			map[string]any{
				"tool_name_pattern": "dangerous.*",
				"requires_approval": false,
			},
		},
	})
	gate := &fakeGate{}
	r.SetApprovalGate(gate)

	if _, err := r.Call(context.Background(), "dangerous.delete", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if gate.calls != 0 {
		t.Errorf("gate called %d times, want 0 (runtime override disabled gating)", gate.calls)
	}
	if !handlerRan {
		t.Error("handler should have run after runtime override")
	}
}

// TestEffectiveApprovalFirstMatchingRuleWins pins the ordering semantics
// so a narrow rule placed early is honoured even if a later broader
// rule would also match. This is the operator's knob for exception rules.
func TestEffectiveApprovalFirstMatchingRuleWins(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	_ = r.Register(Tool{Name: "fs.temp.clean"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{}, nil
	})
	r.SetConfig(map[string]any{
		"tools": []any{
			map[string]any{"tool_name_pattern": "fs.temp.*", "requires_approval": false},
			map[string]any{"tool_name_pattern": "fs.*", "requires_approval": true, "approval_scope": "filesystem"},
		},
	})
	gate := &fakeGate{}
	r.SetApprovalGate(gate)
	if _, err := r.Call(context.Background(), "fs.temp.clean", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("call: %v", err)
	}
	if gate.calls != 0 {
		t.Errorf("narrow rule (requires_approval=false) should have prevailed; gate called %d times", gate.calls)
	}
}

// TestEffectiveApprovalRootsUnderMcpPolicy confirms the config payload
// also works when wrapped under the mcp_policy key (the common shape
// when config comes from /api/v1/config?scope=system&id=mcp_policy).
func TestEffectiveApprovalRootsUnderMcpPolicy(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	_ = r.Register(Tool{Name: "db.drop"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		t.Fatal("handler must not run")
		return nil, nil
	})
	r.SetConfig(map[string]any{
		"mcp_policy": map[string]any{
			"tools": []any{
				map[string]any{"tool_name_pattern": "db.drop", "requires_approval": true},
			},
		},
	})
	gate := &fakeGate{response: &ApprovalRequired{ApprovalID: "app-nested"}}
	r.SetApprovalGate(gate)

	_, err := r.Call(context.Background(), "db.drop", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected gated error")
	}
	var gated *ApprovalRequired
	if !errors.As(err, &gated) {
		t.Fatalf("expected ApprovalRequired, got %v", err)
	}
}

// TestToolRegistryPreservesApprovalFields asserts that ToolRegistry.Register
// stores the new approval fields and Call's lookup path returns the same
// tool metadata that was registered. Catches regressions where a future
// edit might strip the fields during normalisation.
func TestToolRegistryPreservesApprovalFields(t *testing.T) {
	t.Parallel()
	r := NewToolRegistry()
	err := r.Register(Tool{
		Name:             "destructive.delete",
		Description:      "wipe everything",
		RequiresApproval: true,
		ApprovalScope:    "dangerous",
	}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	listed := r.List()
	if len(listed) != 1 {
		t.Fatalf("want 1 listed tool, got %d", len(listed))
	}
	if !listed[0].RequiresApproval {
		t.Error("RequiresApproval not preserved")
	}
	if listed[0].ApprovalScope != "dangerous" {
		t.Errorf("ApprovalScope = %q, want dangerous", listed[0].ApprovalScope)
	}
}

func TestToolRegistrationAndCall(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()

	called := false
	err := registry.Register(
		Tool{
			Name:        "jobs.submit",
			Description: "submit a job",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []any{"topic"},
				"properties": map[string]any{"topic": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			called = true
			var payload map[string]any
			if err := json.Unmarshal(params, &payload); err != nil {
				return nil, err
			}
			return &ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "ok"}},
				StructuredContent: map[string]any{
					"topic": payload["topic"],
				},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("register tool failed: %v", err)
	}

	tools := registry.List()
	if len(tools) != 1 || tools[0].Name != "jobs.submit" {
		t.Fatalf("unexpected tools list: %+v", tools)
	}

	result, err := registry.Call(context.Background(), "jobs.submit", json.RawMessage(`{"topic":"job.echo"}`))
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if !called {
		t.Fatal("expected tool handler to be invoked")
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestToolDisabledByConfig(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(Tool{Name: "jobs.submit"}, func(_ context.Context, _ json.RawMessage) (*ToolCallResult, error) {
		return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("register tool failed: %v", err)
	}
	registry.SetConfig(map[string]any{
		"mcp": map[string]any{
			"tools": map[string]any{
				"jobs.submit": map[string]any{"enabled": false},
			},
		},
	})

	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected disabled tool to be omitted from list, got %+v", got)
	}
	_, err := registry.Call(context.Background(), "jobs.submit", json.RawMessage(`{}`))
	if !errors.Is(err, ErrToolDisabled) {
		t.Fatalf("expected ErrToolDisabled, got %v", err)
	}
}

func TestResourceRegistrationAndRead(t *testing.T) {
	t.Parallel()
	registry := NewResourceRegistry()
	err := registry.Register(Resource{
		URI:      "cordum://status",
		Name:     "status",
		MIMEType: "application/json",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri, MIMEType: "application/json", Text: `{"ok":true}`}, nil
	})
	if err != nil {
		t.Fatalf("register resource failed: %v", err)
	}

	resources := registry.List()
	if len(resources) != 1 || resources[0].URI != "cordum://status" {
		t.Fatalf("unexpected resources list: %+v", resources)
	}

	content, err := registry.Read(context.Background(), "cordum://status")
	if err != nil {
		t.Fatalf("resource read failed: %v", err)
	}
	if content.URI != "cordum://status" {
		t.Fatalf("unexpected resource uri: %q", content.URI)
	}
}

func TestResourceDisabledByConfig(t *testing.T) {
	t.Parallel()
	registry := NewResourceRegistry()
	if err := registry.Register(Resource{
		URI:  "cordum://status",
		Name: "status",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri}, nil
	}); err != nil {
		t.Fatalf("register resource failed: %v", err)
	}
	registry.SetConfig(map[string]any{
		"mcp": map[string]any{
			"resources": map[string]any{
				"status": map[string]any{"enabled": false},
			},
		},
	})

	if got := registry.List(); len(got) != 0 {
		t.Fatalf("expected disabled resource to be omitted from list, got %+v", got)
	}
	_, err := registry.Read(context.Background(), "cordum://status")
	if !errors.Is(err, ErrResourceDisabled) {
		t.Fatalf("expected ErrResourceDisabled, got %v", err)
	}
}

func TestToolCallInvalidParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.validate",
			Description: "tool with required params",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []any{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Missing required field "name" — should fail validation.
	_, err := registry.Call(context.Background(), "test.validate", json.RawMessage(`{"other":"value"}`))
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("expected ErrInvalidParams, got: %v", err)
	}
}

func TestToolCallValidParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.valid",
			Description: "tool with schema",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []any{"name"},
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	result, err := registry.Call(context.Background(), "test.valid", json.RawMessage(`{"name":"alice"}`))
	if err != nil {
		t.Fatalf("expected valid params to pass: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestToolCallEmptyParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.empty",
			Description: "tool with no required fields",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Empty params {} should pass when no required fields.
	result, err := registry.Call(context.Background(), "test.empty", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("expected empty params to pass: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestToolCallNullParams(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.null",
			Description: "tool accepting null params",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// nil/empty params should be treated as empty map.
	_, err := registry.Call(context.Background(), "test.null", nil)
	if err != nil {
		t.Fatalf("expected nil params to pass: %v", err)
	}
}

func TestToolCallInvalidJSON(t *testing.T) {
	t.Parallel()
	registry := NewToolRegistry()
	if err := registry.Register(
		Tool{
			Name:        "test.badjson",
			Description: "tool for testing bad JSON",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
		func(_ context.Context, params json.RawMessage) (*ToolCallResult, error) {
			return &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}}, nil
		},
	); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Malformed JSON should return an error.
	_, err := registry.Call(context.Background(), "test.badjson", json.RawMessage(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON params")
	}
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("expected ErrInvalidParams, got: %v", err)
	}
}

func TestURITemplateMatching(t *testing.T) {
	t.Parallel()
	registry := NewResourceRegistry()
	if err := registry.RegisterTemplate(ResourceTemplate{
		URITemplate: "cordum://jobs/{id}",
		Name:        "job",
		MIMEType:    "application/json",
	}, func(_ context.Context, uri string) (*ResourceContents, error) {
		return &ResourceContents{URI: uri, MIMEType: "application/json", Text: `{"id":"123"}`}, nil
	}); err != nil {
		t.Fatalf("register template failed: %v", err)
	}

	templates := registry.ListTemplates()
	if len(templates) != 1 || templates[0].URITemplate != "cordum://jobs/{id}" {
		t.Fatalf("unexpected templates list: %+v", templates)
	}

	content, err := registry.Read(context.Background(), "cordum://jobs/123")
	if err != nil {
		t.Fatalf("template read failed: %v", err)
	}
	if content.URI != "cordum://jobs/123" {
		t.Fatalf("unexpected content uri: %q", content.URI)
	}
}
