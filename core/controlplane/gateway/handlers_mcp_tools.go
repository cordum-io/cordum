package gateway

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// denyEventRing is a bounded in-memory ring of mcp_tool_denied events.
// It feeds the dashboard's per-identity "recent denials" panel without
// requiring operators to stand up the SIEM pipeline. The SIEM sender
// still receives every event via audit.AuditSender — this ring is a
// local fan-out.
type denyEventRing struct {
	mu  sync.Mutex
	buf []denyEventRecord
	cap int
}

type denyEventRecord struct {
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agent_id"`
	ToolName  string    `json:"tool_name"`
	SubReason string    `json:"sub_reason"`
	Severity  string    `json:"severity"`
}

func newDenyEventRing(capSize int) *denyEventRing {
	if capSize <= 0 {
		capSize = 500
	}
	return &denyEventRing{cap: capSize, buf: make([]denyEventRecord, 0, capSize)}
}

func (r *denyEventRing) record(ev denyEventRecord) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.cap {
		// Drop the oldest entry. O(n) on overflow but the ring is
		// bounded to a few hundred entries — operator-scale, not event-
		// hose scale.
		copy(r.buf, r.buf[1:])
		r.buf = r.buf[:len(r.buf)-1]
	}
	r.buf = append(r.buf, ev)
}

// recent returns up to limit entries matching agentID, newest first.
func (r *denyEventRing) recent(agentID string, limit int) []denyEventRecord {
	if r == nil || limit <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]denyEventRecord, 0, limit)
	for i := len(r.buf) - 1; i >= 0 && len(out) < limit; i-- {
		if r.buf[i].AgentID != agentID {
			continue
		}
		out = append(out, r.buf[i])
	}
	return out
}

// handleListMCPTools serves GET /api/v1/mcp/tools. Without agent_id the
// endpoint returns the unfiltered catalogue (admin-only). With agent_id
// it returns the subset that identity would see.
func (s *server) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermMCPRead, "admin") {
		return
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.toolRegistry == nil {
		writeJSON(w, map[string]any{"tools": []mcp.Tool{}, "agent_id": "", "filtered": false})
		return
	}

	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		tools := runtime.toolRegistry.ListToolsUnfiltered()
		sortTools(tools)
		writeJSON(w, map[string]any{
			"tools":    tools,
			"agent_id": "",
			"filtered": false,
		})
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeMCPAgentIDRequired, err.Error())
		return
	}
	stored, err := s.agentIdentityStore.Get(r.Context(), tenant, agentID)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if stored == nil {
		writeJSONError(w, http.StatusNotFound, errorCodeMCPAgentIdentityNotFound, "agent identity not found")
		return
	}
	s.writeAgentToolVisibility(w, r, runtime, stored)
}

// handleAgentToolVisibility serves GET /api/v1/agents/{id}/tools.
func (s *server) handleAgentToolVisibility(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermMCPRead, "admin") {
		return
	}
	agentID := strings.TrimSpace(r.PathValue("id"))
	if agentID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeMCPAgentIDRequired, "agent id required")
		return
	}
	if s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "agent identity store unavailable")
		return
	}
	// Tenant scoping runs before the runtime/toolRegistry availability
	// check so a cross-tenant admin sees the same 404 regardless of MCP
	// runtime state — no existence oracle for AllowedTools + RiskTier
	// enumeration across tenants.
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeMCPAgentIDRequired, err.Error())
		return
	}
	stored, err := s.agentIdentityStore.Get(r.Context(), tenant, agentID)
	if err != nil {
		writeInternalError(w, r, "get agent identity", err)
		return
	}
	if stored == nil {
		writeJSONError(w, http.StatusNotFound, errorCodeMCPAgentIdentityNotFound, "agent identity not found")
		return
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.toolRegistry == nil {
		writeJSON(w, map[string]any{"tools": []mcp.Tool{}, "agent_id": agentID, "filtered": true})
		return
	}
	s.writeAgentToolVisibility(w, r, runtime, stored)
}

func (s *server) writeAgentToolVisibility(w http.ResponseWriter, r *http.Request, runtime *mcpRuntimeState, stored *store.AgentIdentity) {
	identity := mcpIdentityFromStore(stored)
	if identity == nil {
		writeJSON(w, map[string]any{
			"tools":    []mcp.Tool{},
			"agent_id": stored.ID,
			"filtered": true,
			"note":     "identity is revoked or suspended",
		})
		return
	}
	ctx := mcp.ContextWithIdentity(r.Context(), identity)
	tools := runtime.toolRegistry.ListTools(ctx)
	sortTools(tools)
	writeJSON(w, map[string]any{
		"tools":    tools,
		"agent_id": stored.ID,
		"filtered": true,
	})
}

// handleAgentDeniedEvents serves GET /api/v1/agents/{id}/denied-events.
// Returns up to 50 most recent mcp_tool_denied records for the
// identity from the in-memory ring.
func (s *server) handleAgentDeniedEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermMCPRead, "admin") {
		return
	}
	agentID := strings.TrimSpace(r.PathValue("id"))
	if agentID == "" {
		writeJSONError(w, http.StatusBadRequest, errorCodeMCPAgentIDRequired, "agent id required")
		return
	}
	events := []denyEventRecord{}
	if s.mcpDenyRing != nil {
		events = s.mcpDenyRing.recent(agentID, 50)
	}
	writeJSON(w, map[string]any{
		"agent_id": agentID,
		"events":   events,
		"limit":    50,
	})
}

func sortTools(tools []mcp.Tool) {
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
}

// recordDenyEvent is the hook the mcpDenyAuditor calls so denials flow
// both to the SIEM exporter AND into the local ring consumed by the
// dashboard panel.
func (s *server) recordDenyEvent(ev audit.SIEMEvent) {
	if s == nil || s.mcpDenyRing == nil {
		return
	}
	toolName := ""
	subReason := ""
	if ev.Extra != nil {
		toolName = ev.Extra["tool_name"]
		subReason = ev.Extra["sub_reason"]
	}
	if subReason == "" {
		subReason = ev.Reason
	}
	s.mcpDenyRing.record(denyEventRecord{
		Timestamp: ev.Timestamp,
		AgentID:   ev.AgentID,
		ToolName:  toolName,
		SubReason: subReason,
		Severity:  ev.Severity,
	})
}
