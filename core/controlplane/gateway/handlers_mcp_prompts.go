package gateway

import (
	"net/http"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/mcp"
)

// handleListMCPPrompts serves GET /api/v1/mcp/prompts. Returns the live
// prompt catalogue straight off the running MCP server's PromptRegistry
// so the dashboard PromptCatalog page renders what the HTTP MCP server
// actually exposes (not a bundled JSON constant that can drift).
//
// Admin-gated because the catalogue reveals server-internal prompt
// argument contracts operators rely on for policy decisions; surfacing
// them to unauthenticated callers would leak detection signal even if
// the prompts themselves don't execute without a live MCP session.
//
// Empty list on disabled / unwired MCP runtime — the dashboard treats
// empty as "no prompts configured" rather than as an error, matching
// the tools endpoint contract.
func (s *server) handleListMCPPrompts(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermMCPRead, "admin") {
		return
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.promptRegistry == nil {
		writeJSON(w, map[string]any{"prompts": []mcp.Prompt{}})
		return
	}
	prompts := runtime.promptRegistry.List(r.Context())
	writeJSON(w, map[string]any{"prompts": prompts})
}
