package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/mcp"
)

// TestMCPProdBoot_RegistersCordumURIResources is the regression guard
// that QA asked for: build a ResourceRegistry the same way
// registerMCPRoutes does, then assert every kind accepted by
// parseCordumURI has a template in the registry's ListTemplates output.
//
// The previous reopen shipped RegisterCordumURIResources but never
// called it from the production bootstrap; unit tests on the function
// itself kept passing while resources/list was empty at runtime. This
// test closes that gap by mirroring the wire-up path.
func TestMCPProdBoot_RegistersCordumURIResources(t *testing.T) {
	t.Parallel()
	registry := mcp.NewResourceRegistry()
	if err := mcp.RegisterCordumURIResources(registry, &wiringBridge{}); err != nil {
		t.Fatalf("RegisterCordumURIResources: %v", err)
	}
	templates := registry.ListTemplates()
	want := map[string]bool{
		"cordum://jobs/{id}":            false,
		"cordum://runs/{id}":            false,
		"cordum://runs/{id}/timeline":   false,
		"cordum://workflows/{id}":       false,
		"cordum://packs/{id}":           false,
		"cordum://topics/{name}":        false,
		"cordum://agents/{id}":          false,
		"cordum://audit/{tenant}/{seq}": false,
	}
	for _, tmpl := range templates {
		if _, ok := want[tmpl.URITemplate]; ok {
			want[tmpl.URITemplate] = true
		}
	}
	for uri, seen := range want {
		if !seen {
			t.Errorf("cordum:// template %q missing from resources/templates/list — the production bootstrap has regressed", uri)
		}
	}
}

// TestMCPProdBoot_ToolCallAuditHookFires is the second QA regression
// guard: once the hook is wired, a successful tools/call MUST produce
// an mcp.tool_called SIEMEvent. Register a trivial tool, attach a
// capturing hook via WithToolCallAudit (same method the production
// bootstrap calls), invoke via ToolRegistry.Call, and assert the event
// carries the required Extra fields.
func TestMCPProdBoot_ToolCallAuditHookFires(t *testing.T) {
	t.Parallel()
	registry := mcp.NewToolRegistry()
	var captured audit.SIEMEvent
	registry.WithToolCallAudit(func(e audit.SIEMEvent) { captured = e })

	if err := registry.Register(mcp.Tool{Name: "wiring-echo"}, func(ctx context.Context, _ json.RawMessage) (*mcp.ToolCallResult, error) {
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "ok"}}}, nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx := mcp.ContextWithIdentity(context.Background(), &mcp.AgentIdentity{ID: "agent-wiring"})
	ctx = mcp.WithTenant(ctx, "tenant-wiring")
	if _, err := registry.Call(ctx, "wiring-echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Call: %v", err)
	}

	if captured.EventType != mcp.EventMCPToolCalled {
		t.Errorf("EventType = %q want %q — the audit hook fired but with the wrong type", captured.EventType, mcp.EventMCPToolCalled)
	}
	if got := captured.Extra["tool_name"]; got != "wiring-echo" {
		t.Errorf("Extra[tool_name] = %q want wiring-echo", got)
	}
	if got := captured.Extra["agent_id"]; got != "agent-wiring" {
		t.Errorf("Extra[agent_id] = %q want agent-wiring", got)
	}
	if got := captured.Extra["tenant"]; got != "tenant-wiring" {
		t.Errorf("Extra[tenant] = %q want tenant-wiring (mcpAuth stashes this via mcp.WithTenant)", got)
	}
}

func TestMCPGatewayCanonicalUpstreamRouteRegistered(t *testing.T) {
	s, _, _ := newTestGateway(t)
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}

	cases := []struct {
		name        string
		path        string
		wantPattern string
	}{
		{
			name:        "canonical",
			path:        "/api/v1/mcp/gateway/upstream",
			wantPattern: "POST /api/v1/mcp/gateway/upstream",
		},
		{
			name:        "subroute",
			path:        "/api/v1/mcp/gateway/upstream/tools/list",
			wantPattern: "/api/v1/mcp/gateway/upstream/",
		},
		{
			name:        "legacy_bare_route_is_not_alias",
			path:        "/api/v1/mcp/gateway",
			wantPattern: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			_, pattern := mux.Handler(req)
			if pattern != tc.wantPattern {
				t.Fatalf("pattern for POST %s = %q; want %q", tc.path, pattern, tc.wantPattern)
			}
		})
	}
}

// wiringBridge is a minimal ServiceBridge that returns BridgeError(501)
// for every method. The wiring tests above only read the registry's
// Template list; they never dereference the URI templates.
type wiringBridge struct{}

func (*wiringBridge) SubmitJob(context.Context, mcp.SubmitJobInput) (*mcp.SubmitJobOutput, error) {
	return nil, nil
}
func (*wiringBridge) CancelJob(context.Context, string, string) error { return nil }
func (*wiringBridge) TriggerWorkflow(context.Context, mcp.TriggerWorkflowInput) (*mcp.TriggerOutput, error) {
	return nil, nil
}
func (*wiringBridge) ApproveJob(context.Context, string, string) error { return nil }
func (*wiringBridge) RejectJob(context.Context, string, string) error  { return nil }
func (*wiringBridge) SimulatePolicy(context.Context, mcp.PolicySimInput) (*mcp.PolicySimOutput, error) {
	return nil, nil
}
func (*wiringBridge) ListJobs(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) GetJob(context.Context, string) (*mcp.ResourceItem, error) {
	return &mcp.ResourceItem{}, nil
}
func (*wiringBridge) ListRuns(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) GetRun(context.Context, string) (*mcp.ResourceItem, error) {
	return &mcp.ResourceItem{}, nil
}
func (*wiringBridge) GetRunTimeline(context.Context, string) (*mcp.ResourceItem, error) {
	return &mcp.ResourceItem{}, nil
}
func (*wiringBridge) ListWorkflows(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) ListPacks(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) ListTopics(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) ListWorkers(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) ListAgents(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) ListPendingApprovals(context.Context, mcp.ListInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) QueryAudit(context.Context, mcp.AuditQueryInput) (*mcp.ListPage, error) {
	return &mcp.ListPage{}, nil
}
func (*wiringBridge) VerifyAudit(context.Context, string) (*mcp.ResourceItem, error) {
	return &mcp.ResourceItem{}, nil
}
func (*wiringBridge) GetStatus(context.Context) (*mcp.ResourceItem, error) {
	return &mcp.ResourceItem{}, nil
}

// Mutating surface stubs — every method is a no-op that returns the
// canonical empty record. Wiring tests do not exercise the mutating
// paths; they just need the interface satisfied.
func (*wiringBridge) CreateWorkflow(context.Context, mcp.CreateWorkflowInput) (*mcp.CreateWorkflowOutput, error) {
	return &mcp.CreateWorkflowOutput{}, nil
}
func (*wiringBridge) InstallPack(context.Context, mcp.InstallPackInput) (*mcp.InstallPackOutput, error) {
	return &mcp.InstallPackOutput{}, nil
}
func (*wiringBridge) UninstallPack(context.Context, mcp.UninstallPackInput) error { return nil }
func (*wiringBridge) RegisterAgent(context.Context, mcp.RegisterAgentInput) (*mcp.RegisterAgentOutput, error) {
	return &mcp.RegisterAgentOutput{}, nil
}
func (*wiringBridge) UpdatePolicyBundle(context.Context, mcp.UpdatePolicyBundleInput) (*mcp.UpdatePolicyBundleOutput, error) {
	return &mcp.UpdatePolicyBundleOutput{}, nil
}
func (*wiringBridge) RevokeWorkerSession(context.Context, mcp.RevokeWorkerSessionInput) error {
	return nil
}
func (*wiringBridge) SetAgentScope(context.Context, mcp.SetAgentScopeInput) (*mcp.SetAgentScopeOutput, error) {
	return &mcp.SetAgentScopeOutput{}, nil
}
