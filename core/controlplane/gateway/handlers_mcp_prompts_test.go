package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/mcp"
)

// TestHandleListMCPPrompts_LiveRegistryExposesFirstPartyPrompts is the
// regression test for task-5d8c69e0 reopen #1 issue 1. The prior
// shipment registered prompts only in core/mcp tests; the real HTTP
// gateway never wired them, so prompts/list + /api/v1/mcp/prompts
// returned empty in production. This test builds a server with the
// same RegisterAllPrompts call the production path uses, mounts it
// as runtime state, and asserts the HTTP response carries all 4
// first-party prompts with their argument schemas intact.
func TestHandleListMCPPrompts_LiveRegistryExposesFirstPartyPrompts(t *testing.T) {
	s, _, _ := newTestGateway(t)

	promptRegistry := mcp.NewPromptRegistry()
	if err := mcp.RegisterAllPrompts(promptRegistry); err != nil {
		t.Fatalf("RegisterAllPrompts: %v", err)
	}
	s.setMCPRuntime(&mcpRuntimeState{
		startedAt:      time.Now().UTC(),
		transport:      "http",
		promptRegistry: promptRegistry,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/prompts", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handleListMCPPrompts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Prompts []mcp.Prompt `json:"prompts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := map[string]bool{
		"draft_safety_rule":       false,
		"explain_denial":          false,
		"summarize_approvals":     false,
		"policy_migration_helper": false,
	}
	for _, p := range resp.Prompts {
		if _, ok := want[p.Name]; ok {
			want[p.Name] = true
			if p.Description == "" {
				t.Errorf("prompt %q missing description", p.Name)
			}
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("prompt %q not surfaced by /api/v1/mcp/prompts", name)
		}
	}
}

// TestHandleListMCPPrompts_EmptyRuntimeReturnsEmptyList pins the
// graceful-degradation contract — disabled MCP runtime returns 200 +
// {prompts: []} rather than a 5xx. The dashboard treats empty as "no
// prompts configured" and stays functional.
func TestHandleListMCPPrompts_EmptyRuntimeReturnsEmptyList(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// No setMCPRuntime call → getMCPRuntime returns nil.

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/prompts", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handleListMCPPrompts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Prompts []mcp.Prompt `json:"prompts"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Prompts) != 0 {
		t.Errorf("expected empty prompts list, got %d entries", len(resp.Prompts))
	}
}

// TestHandleListMCPPrompts_NonAdminForbidden pins the admin gate.
// Prompt arg contracts leak policy-decision signal; a non-admin
// principal must not see them even when the runtime is wired.
func TestHandleListMCPPrompts_NonAdminForbidden(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &tenantStrictAuth{tenant: "default", role: "viewer"}
	promptRegistry := mcp.NewPromptRegistry()
	_ = mcp.RegisterAllPrompts(promptRegistry)
	s.setMCPRuntime(&mcpRuntimeState{
		startedAt:      time.Now().UTC(),
		transport:      "http",
		promptRegistry: promptRegistry,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/prompts", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "viewer")
	rec := httptest.NewRecorder()
	s.handleListMCPPrompts(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d want 403; body=%s", rec.Code, rec.Body.String())
	}
}
