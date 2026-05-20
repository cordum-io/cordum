package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	mcpresources "github.com/cordum/cordum/core/mcp/resources"
	mcptools "github.com/cordum/cordum/core/mcp/tools"
	"github.com/cordum/cordum/core/model"
)

// mcpAgentIDHeader is the request header that identifies the calling
// MCP agent. The gateway uses it (with a principal-ID fallback) to
// populate MCPCallMetadata so the approval gate can log who is asking.
const mcpAgentIDHeader = "X-Agent-Id"

type mcpGatewayConfig struct {
	Enabled   bool
	Transport string
	Port      int
	// PolicyGateEnabled gates the WithPolicyGate / WithApprovalHold
	// wiring at boot. Defaults to false so legacy deploys keep the
	// direct-dispatch path until operators explicitly opt in via
	// `mcp.policy_gate_enabled: true` in config. The "must explicitly
	// opt in" posture is mandated by feedback_dont_change_deployment_defaults.
	PolicyGateEnabled bool
	Raw               map[string]any
}

type mcpRuntimeState struct {
	startedAt        time.Time
	transport        string
	httpTransport    *mcp.HTTPTransport
	toolRegistry     *mcp.ToolRegistry
	resourceRegistry *mcp.ResourceRegistry
	promptRegistry   *mcp.PromptRegistry
	server           *mcp.MCPServer
	approvalStore    *MCPApprovalStore
	approvalHandler  *mcpApprovalHandler
	sweeperStop      chan struct{}
	stopOnce         sync.Once
}

var gatewayMCPState sync.Map // map[*server]*mcpRuntimeState

func (s *server) registerMCPRoutes(mux *http.ServeMux) error {
	if s == nil || mux == nil {
		return nil
	}

	// Always expose MCP routes so clients get explicit disabled/unavailable responses
	// instead of startup-time 404s when MCP config loads after route registration.
	s.registerRoute(mux, "GET /mcp/sse", s.instrumented("/mcp/sse", s.mcpAuth(s.handleMCPSSE)))
	s.registerRoute(mux, "POST /mcp/message", s.instrumented("/mcp/message", s.mcpAuth(s.handleMCPMessage)))
	s.registerRoute(mux, "GET /mcp/status", s.instrumented("/mcp/status", s.mcpAuth(s.handleMCPStatus)))

	cfg := s.loadMCPConfig(context.Background())
	return s.startMCPRuntimeFromConfig(cfg)
}

func mcpTransportSupportsHTTP(transport string) bool {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "http", "both":
		return true
	default:
		return false
	}
}

func mcpHTTPRuntimeWanted(cfg mcpGatewayConfig) bool {
	return cfg.Enabled && mcpTransportSupportsHTTP(cfg.Transport)
}

func (s *server) startMCPRuntimeFromConfig(cfg mcpGatewayConfig) error {
	if s == nil {
		return nil
	}
	if !cfg.Enabled {
		slog.Info("mcp runtime disabled by config")
		return nil
	}
	if !mcpTransportSupportsHTTP(cfg.Transport) {
		slog.Info("mcp http runtime disabled", "transport", cfg.Transport)
		return nil
	}
	if runtime := s.getMCPRuntime(); runtime != nil && runtime.server != nil && runtime.httpTransport != nil && !runtime.httpTransport.IsClosed() {
		if runtime.toolRegistry != nil {
			runtime.toolRegistry.SetConfig(cfg.Raw)
		}
		if runtime.resourceRegistry != nil {
			runtime.resourceRegistry.SetConfig(cfg.Raw)
		}
		runtime.server.ReloadConfig(cfg.Raw)
		return nil
	}

	transport := mcp.NewHTTPTransport(mcp.DefaultMaxMessageBytes, mcp.DefaultHTTPResponseTimeout)
	toolRegistry := mcp.NewToolRegistry()
	resourceRegistry := mcp.NewResourceRegistry()
	toolRegistry.SetConfig(cfg.Raw)
	resourceRegistry.SetConfig(cfg.Raw)

	// Per-tool approval gate. Requires a working Redis client — on dev
	// deploys without one we skip wiring and the registry falls back to
	// permissive behaviour. Production refuses to start without Redis
	// elsewhere, so reaching this path in prod means degraded mode.
	var approvalStore *MCPApprovalStore
	var approvalHandler *mcpApprovalHandler
	// approvalGate is hoisted to function scope so the EDGE-102 boot
	// wiring below can hand it to attachMCPPolicyDeps. nil when Redis
	// is unavailable; the gate's nil-check inside BuildMCPPolicyDeps
	// fails closed without crashing the boot path.
	var approvalGate *gatewayApprovalGate
	if client := s.redisClient(); client != nil {
		approvalStore = NewMCPApprovalStore(client).WithAuditHook(s.mcpApprovalAuditHook())
		approvalHandler = newMCPApprovalHandler(approvalStore)
		// Scope-preapproval lookup — gate consults the agent identity
		// store first so a CI bot with PreapprovedMutatingTools set
		// can skip the human-approval step. Audit still fires; see
		// MarkApprovalPreapproved in core/mcp/audit_invocation.go.
		// EDGE-052 reopen #1 — production wiring of the MCP approval gate
		// MUST attach the invariant lookup so admin-authored secops/invariants
		// rules block at the gate (not just surface via RulesForMCPTool).
		// Extracted to wireMCPApprovalGate for testability; the test path
		// passes a stub invariant lookup, the production path passes the
		// bundle-reading closure below.
		invariantLookup := MCPInvariantLookupFunc(func(ctx context.Context) []config.PolicyRule {
			bundles, _, err := s.loadPolicyBundles(ctx)
			if err != nil {
				slog.Warn("mcp gate invariant lookup: load policy bundles failed", "err", err)
				return nil
			}
			rules, err := loadMCPInvariantsFromBundles(bundles)
			if err != nil {
				slog.Warn("mcp gate invariant lookup: parse invariants failed", "err", err)
				return nil
			}
			return rules
		})
		rawGate, gate := s.wireMCPApprovalGate(approvalStore, invariantLookup)
		toolRegistry.SetApprovalGate(rawGate)
		approvalGate = gate
		if gate != nil {
			slog.Info("mcp approval gate enabled",
				"preapproval", gate.preapproval != nil,
				"invariants", gate.invariants != nil)
		} else {
			// Future-proof: if NewGatewayApprovalGate is ever swapped to a
			// different concrete type, the helper falls back to attaching
			// the rawGate without the preapproval/invariants extras —
			// the gate is still installed, just degraded.
			slog.Warn("mcp approval gate enabled but preapproval+invariant lookup unavailable: unexpected concrete type")
		}
	} else {
		slog.Warn("mcp approval gate disabled: redis client unavailable — RequiresApproval tools will not be gated")
	}

	serviceBridge := s.newMCPServiceBridge()
	if err := mcptools.RegisterWithBridge(toolRegistry, serviceBridge); err != nil {
		return fmt.Errorf("register mcp tools: %w", err)
	}
	if err := mcpresources.RegisterWithBridge(resourceRegistry, s.newMCPDataBridge()); err != nil {
		return fmt.Errorf("register mcp resources: %w", err)
	}
	// cordum:// URI templates — 8 resource types (jobs, runs, runs/timeline,
	// workflows, packs, topics, agents, audit/{tenant}/{seq}) that let
	// MCP clients dereference Cordum records by stable URI. Must run on
	// the production bootstrap (not just in tests) or resources/list
	// omits the templates and dereference returns 404 — QA reopen fix
	// for task-466b6a6a.
	if err := mcp.RegisterCordumURIResources(resourceRegistry, serviceBridge); err != nil {
		return fmt.Errorf("register cordum:// resources: %w", err)
	}
	// mcp.tool_called audit hook. Every successful tools/call emits a
	// SIEMEvent through the gateway's audit exporter so it lands in the
	// Merkle chain. Without this wire-up the epic rail "Audit every
	// tool call via SIEMEvent mcp_tool_called" is violated — another
	// QA reopen fix.
	if hook := s.mcpToolCallAuditHook(); hook != nil {
		toolRegistry.WithToolCallAudit(hook)
		slog.Info("mcp tool-call audit hook enabled")
	}
	// Scope filter + deny auditor must be wired before Serve starts so
	// the very first tools/call produces an audit trail and respects
	// AllowedTools / RiskTier / DataClassifications. Without this the
	// HTTP gateway is a filter-less bypass around the core feature.
	if auditor := s.newMCPDenyAuditor(); auditor != nil {
		toolRegistry.SetDenyAuditor(auditor)
	}
	toolRegistry.SetScopeEnforcement(true)
	// Rich per-invocation auditor. Emits mcp.tool_invocation (inbound)
	// and mcp.tool_outbound_invocation for every terminal tools/call
	// with args_redacted, result_summary, latency_ms, approval_status.
	// Pairs with the DenyAuditor (denial-only) and the approval gate
	// audit hook to reconstruct the full lifecycle.
	var invocationAuditor mcp.ToolInvocationAuditor
	if s.auditExporter != nil {
		// Redactor merges DefaultRedactionRules (baseline secrets) with
		// any rules advertised by the active policy bundle at
		// `policy.mcp.argument_redaction.rules`. mergeMCPRedactor is
		// called again from a reload goroutine so bundle rotations
		// take effect without a gateway restart. Without the merge
		// the auditor ran with built-in heuristics only — QA reopen
		// fix for the DoD "Arguments redacted per policy rules".
		redactor := s.buildMCPArgumentRedactor(context.Background())
		invocationAuditor = mcp.NewToolInvocationAuditor(s.auditExporter, redactor)
		s.setMCPInvocationAuditor(invocationAuditor)
		go s.runMCPRedactionReload(invocationAuditor)
		slog.Info("mcp tool-invocation auditor enabled",
			"event_types", "mcp.tool_invocation,mcp.tool_outbound_invocation",
		)
	}
	slog.Info("mcp scope enforcement enabled",
		"mode", "http",
		"filter", "AllowedTools+RiskTier+DataClassifications",
	)

	// Register first-party prompts (draft_safety_rule, explain_denial,
	// summarize_approvals, policy_migration_helper) so prompts/list
	// returns them to any MCP client connected to the HTTP gateway.
	// Registration failure is logged but non-fatal — the server still
	// serves tools + resources without prompts, and operators have a
	// greppable signal to diagnose.
	promptRegistry := mcp.NewPromptRegistry()
	if err := mcp.RegisterAllPrompts(promptRegistry); err != nil {
		slog.Error("mcp prompt registration failed; prompts/list will be empty", "error", err)
	}
	mcpServer := mcp.NewServer(transport, toolRegistry, resourceRegistry, mcp.ServerConfig{
		Name:            "cordum",
		Version:         buildinfo.Version,
		ProtocolVersion: mcp.DefaultProtocolVersion,
		RequestTimeout:  30 * time.Second,
	}).WithAuditor(invocationAuditor).WithPrompts(promptRegistry)
	// EDGE-102 / EDGE-103 policy + approval-hold wiring. Gated by
	// `mcp.policy_gate_enabled` so legacy deploys keep the direct-dispatch
	// path until operators explicitly opt in (feedback_dont_change_deployment_defaults).
	// Three terminal states — operators grep one of these lines:
	//   * `mcp.policy_gate skipped` — flag off, by design.
	//   * `mcp.policy_gate wired`   — flag on, every required dep present,
	//                                HasPolicyGate()==true post-attach.
	//   * `mcp.policy_gate degraded` — flag on but attach refused to wire
	//                                because a required dep was missing
	//                                (reason names the dep). HasPolicyGate()
	//                                must read false in this branch.
	if cfg.PolicyGateEnabled {
		var skipReason string
		mcpServer, skipReason = s.attachMCPPolicyDeps(mcpServer, approvalGate)
		if mcpServer.HasPolicyGate() {
			slog.Info("mcp.policy_gate wired",
				"server_name", mcpServer.PolicyServerName(),
				"policy_gate_active", true,
				"approval_hold_active", mcpServer.HasApprovalHold(),
				"emitter", "edge.RedisStore",
				"artifact_store", "artifacts.RedisStore",
			)
		} else {
			if skipReason == "" {
				skipReason = "WithPolicyGate partial-wiring guard reset deps"
			}
			slog.Warn("mcp.policy_gate degraded",
				"policy_gate_active", false,
				"approval_hold_active", mcpServer.HasApprovalHold(),
				"reason", skipReason,
			)
		}
	} else {
		slog.Info("mcp.policy_gate skipped",
			"policy_gate_active", false,
			"reason", "policy_gate_enabled config flag is false",
		)
	}
	sweeperStop := make(chan struct{})
	if approvalStore != nil {
		go runMCPApprovalSweeper(approvalStore, sweeperStop)
	}
	state := &mcpRuntimeState{
		startedAt:        time.Now().UTC(),
		transport:        cfg.Transport,
		httpTransport:    transport,
		toolRegistry:     toolRegistry,
		resourceRegistry: resourceRegistry,
		promptRegistry:   promptRegistry,
		server:           mcpServer,
		approvalStore:    approvalStore,
		approvalHandler:  approvalHandler,
		sweeperStop:      sweeperStop,
	}
	s.setMCPRuntime(state)
	go func() {
		if err := mcpServer.Serve(); err != nil {
			slog.Error("mcp server loop failed", "error", err)
		}
	}()
	if s.shutdownCh != nil {
		go func() {
			<-s.shutdownCh
			s.stopMCPRuntime(state)
		}()
	}

	slog.Info(
		"mcp routes enabled",
		"transport", cfg.Transport,
		"port", cfg.Port,
	)
	return nil
}

func (s *server) mcpAuth(next http.HandlerFunc) http.HandlerFunc {
	if next == nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			writeErrorJSON(w, http.StatusNotFound, "not found")
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.auth == nil {
			next(w, r)
			return
		}
		authCtx, err := s.auth.AuthenticateHTTP(r)
		if err != nil {
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), auth.ContextKey{}, authCtx)
		r = r.WithContext(ctx)

		tenantID := tenantFromRequest(r)
		if tenantID == "" {
			writeErrorJSON(w, http.StatusForbidden, "tenant id required")
			return
		}
		if authCtx.Tenant != "" && !authCtx.AllowCrossTenant {
			if strings.TrimSpace(authCtx.Tenant) != tenantID {
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
		// Stash MCP call metadata so the approval gate (wired into the
		// ToolRegistry) can evaluate per-tool approval policy without
		// knowing about gateway identity types. Tenant + principal come
		// from the auth context; agent_id prefers the X-Agent-Id header
		// and falls back to the principal so the self-approval guard
		// has something to compare against.
		agentID := strings.TrimSpace(r.Header.Get(mcpAgentIDHeader))
		if agentID == "" {
			agentID = strings.TrimSpace(authCtx.PrincipalID)
		}
		mcpCtx := WithMCPCallMetadata(r.Context(), MCPCallMetadata{
			Tenant:            tenantID,
			AgentID:           agentID,
			Principal:         strings.TrimSpace(authCtx.PrincipalID),
			RequesterIdentity: submitterIdentity(r),
		})
		// Also stash tenant for the mcp.tool_called audit hook, which
		// reads ctx via mcp.TenantFromContext (a separate ctx key from
		// MCPCallMetadata so core/mcp stays free of gateway-specific
		// identity types).
		mcpCtx = mcp.WithTenant(mcpCtx, tenantID)
		r = r.WithContext(mcpCtx)
		// Attach *mcp.AgentIdentity so ToolRegistry.ListTools and the
		// scope filter can evaluate the caller's AllowedTools /
		// RiskTier / DataClassifications. Without this the HTTP
		// transport sees msg.identity=nil and every tool list comes
		// back empty — which is the fail-closed outcome QA flagged.
		if identity := s.resolveMCPIdentity(r); identity != nil {
			r = r.WithContext(mcp.ContextWithIdentity(r.Context(), identity))
		}
		next(w, r)
	}
}

// resolveMCPIdentity looks up the agent identity for this MCP request.
// Resolution order:
//  1. X-Agent-Id header (explicit — matches the documented contract).
//  2. auth-principal fallback via agent_by_worker reverse index, so
//     credentialled workers inherit their linked identity without
//     needing to send the header explicitly.
//
// Returns nil when no identity could be resolved; ToolRegistry treats
// a nil identity as fail-closed (zero tools visible, all calls denied).
// Revoked/suspended identities also resolve to nil.
func (s *server) resolveMCPIdentity(r *http.Request) *mcp.AgentIdentity {
	if s == nil || s.agentIdentityStore == nil {
		return nil
	}
	ctx := r.Context()
	if id := strings.TrimSpace(r.Header.Get(mcpAgentIDHeader)); id != "" {
		identity, err := s.agentIdentityStore.Get(ctx, "", id)
		if err != nil || identity == nil {
			return nil
		}
		return mcpIdentityFromStore(identity)
	}
	authCtx := auth.FromContext(ctx)
	if authCtx == nil {
		return nil
	}
	principal := strings.TrimSpace(authCtx.PrincipalID)
	if principal == "" {
		return nil
	}
	identity, err := s.agentIdentityStore.GetByWorkerID(ctx, principal)
	if err != nil || identity == nil {
		return nil
	}
	return mcpIdentityFromStore(identity)
}

// runMCPApprovalSweeper expires PENDING MCP approvals whose TTL has
// passed. Runs in a dedicated goroutine started by registerMCPRoutes.
// Sweep interval is 30s — matching the cadence of the existing
// job-approval reaper.
func runMCPApprovalSweeper(store *MCPApprovalStore, stop <-chan struct{}) {
	if store == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			swept, err := store.SweepExpired(ctx, time.Now())
			cancel()
			if err != nil {
				slog.Warn("mcp approval sweeper failed", "error", err)
				continue
			}
			if swept > 0 {
				slog.Info("mcp approval sweeper transitioned expired records", "count", swept)
			}
		}
	}
}

// mcpToolCallAuditHook returns a ToolCallAuditHook that forwards every
// successful tools/call into the gateway's audit exporter. Nil-safe:
// when the exporter is not configured the hook returns nil so
// dev/stdio deploys don't emit duplicate logs. The hook itself sits
// inside core/mcp and invokes IdentityFromContext + TenantFromContext
// on the ctx the registry provides; middleware must stash both values
// for the Extra fields to populate.
func (s *server) mcpToolCallAuditHook() mcp.ToolCallAuditHook {
	if s == nil || s.auditExporter == nil {
		return nil
	}
	sender := s.auditExporter
	return func(event audit.SIEMEvent) {
		// Defense-in-depth: core/mcp/audit_invocation.go defaults the
		// handle's tenant at Start{Inbound,Outbound}, but a future
		// producer site that bypasses the auditor and constructs a
		// SIEMEvent directly would slip through to the chain sender's
		// slog.Warn fallback. Default here so the gateway boundary is
		// the explicit catch-all. task-3fad45d3.
		event.TenantID = model.ResolveTenantForAudit(event.TenantID, "")
		sender.Send(event)
	}
}

// mcpApprovalAuditHook bridges MCPApprovalStore lifecycle events into
// the gateway's audit exporter. Nil-safe: when the exporter is not
// configured the hook returns a no-op so dev deploys work without
// audit plumbing.
func (s *server) mcpApprovalAuditHook() MCPAuditHook {
	if s == nil || s.auditExporter == nil {
		return nil
	}
	sender := s.auditExporter
	return func(event audit.SIEMEvent) {
		// Same defense-in-depth as mcpToolCallAuditHook above:
		// MCPApprovalStore populates Tenant from validated request
		// records, but a future enqueue path that skips the validator
		// would emit a tenantless event. Default at the gateway
		// boundary. task-3fad45d3.
		event.TenantID = model.ResolveTenantForAudit(event.TenantID, "")
		sender.Send(event)
	}
}

func (s *server) loadMCPConfig(ctx context.Context) mcpGatewayConfig {
	cfg := mcpGatewayConfig{
		Enabled:   false,
		Transport: "http",
		Port:      0,
		Raw:       nil,
	}
	if s == nil || s.configSvc == nil {
		return cfg
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	effective, err := s.configSvc.Effective(cctx, "", "", "", "")
	if err != nil || effective == nil {
		return cfg
	}
	cfg.Raw = effective
	if enabled, ok := lookupBoolPath(effective, "mcp", "enabled"); ok {
		cfg.Enabled = enabled
	}
	if transport, ok := lookupStringPath(effective, "mcp", "transport"); ok && transport != "" {
		cfg.Transport = transport
	}
	if port := lookupIntPath(effective, "mcp", "port"); port > 0 {
		cfg.Port = port
	}
	if enabled, ok := lookupBoolPath(effective, "mcp", "policy_gate_enabled"); ok {
		cfg.PolicyGateEnabled = enabled
	}
	return cfg
}

func (s *server) mcpHTTPTransport() *mcp.HTTPTransport {
	runtime := s.getMCPRuntime()
	if runtime == nil || !mcpTransportSupportsHTTP(runtime.transport) || runtime.httpTransport == nil || runtime.httpTransport.IsClosed() {
		return nil
	}
	return runtime.httpTransport
}

// mcpSSEReauthInterval is the interval at which long-lived MCP SSE sessions
// re-validate the client's credentials. If re-auth fails (key revoked,
// expired token, etc.) the SSE connection is terminated.
const mcpSSEReauthInterval = 5 * time.Minute

// mcpSSEReauthFirstTick fires the first re-auth soon after SSE establishment
// so credentials revoked after the initial handshake disconnect within seconds.
const mcpSSEReauthFirstTick = time.Second

func (s *server) handleMCPSSE(w http.ResponseWriter, r *http.Request) {
	transport := s.mcpHTTPTransport()
	if transport == nil {
		writeJSONError(w, http.StatusServiceUnavailable, errorCodeMCPHTTPTransportUnavailable, "mcp http transport unavailable")
		return
	}

	// When no auth provider is configured, skip periodic re-auth.
	if s.auth == nil {
		transport.HandleSSE(w, r)
		return
	}

	// Wrap the request context with a cancel so we can terminate the SSE
	// stream if re-authentication fails during the session's lifetime.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)

	// Start a background goroutine that periodically re-validates the
	// original credentials. If validation fails, the context is cancelled
	// which causes the SSE event loop in the transport to exit cleanly.
	go func() {
		ticker := time.NewTicker(mcpSSEReauthFirstTick)
		defer ticker.Stop()
		firstTickFired := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.auth.AuthenticateHTTP(r); err != nil {
					slog.Warn("mcp sse re-auth failed, disconnecting session",
						"error", err,
						"remote", r.RemoteAddr,
					)
					cancel()
					return
				}
				if !firstTickFired {
					firstTickFired = true
					ticker.Reset(mcpSSEReauthInterval)
				}
			}
		}
	}()

	transport.HandleSSE(w, r)
}

func (s *server) handleMCPMessage(w http.ResponseWriter, r *http.Request) {
	transport := s.mcpHTTPTransport()
	if transport == nil {
		writeJSONError(w, http.StatusServiceUnavailable, errorCodeMCPHTTPTransportUnavailable, "mcp http transport unavailable")
		return
	}
	transport.HandleMessage(w, r)
}

func (s *server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.loadMCPConfig(r.Context())
	resp := map[string]any{
		"running":           false,
		"connected_clients": 0,
		"uptime_seconds":    int64(0),
		"transport":         cfg.Transport,
		"enabled_tools":     0,
		"enabled_resources": 0,
	}
	if runtime := s.getMCPRuntime(); runtime != nil {
		running := runtime.server != nil
		if runtime.httpTransport != nil {
			running = running && !runtime.httpTransport.IsClosed()
			resp["connected_clients"] = runtime.httpTransport.ActiveSessionCount()
		}
		if !runtime.startedAt.IsZero() && running {
			resp["uptime_seconds"] = int64(time.Since(runtime.startedAt).Seconds())
		}
		if runtime.transport != "" {
			resp["transport"] = runtime.transport
		}
		if runtime.toolRegistry != nil {
			resp["enabled_tools"] = len(runtime.toolRegistry.List())
		}
		if runtime.resourceRegistry != nil {
			resp["enabled_resources"] = len(runtime.resourceRegistry.List()) + len(runtime.resourceRegistry.ListTemplates())
		}
		resp["running"] = running
	}
	writeJSON(w, resp)
}

// gatewayMCPAuditor stores the live *mcp.ToolInvocationAuditor per
// server so the redaction-reload goroutine can call SetRedactor on it
// without the auditor bleeding into every handler signature.
var gatewayMCPAuditor sync.Map

func (s *server) setMCPInvocationAuditor(a mcp.ToolInvocationAuditor) {
	if s == nil || a == nil {
		return
	}
	gatewayMCPAuditor.Store(s, a)
}

// mcpArgumentRedactionConfigKey is the configsvc key the gateway reads
// to pull policy-bundle-sourced MCP argument-redaction rules. Stored
// as a JSON blob matching the LoadRulesFromPolicyBundle envelope.
// Admins rotate rules by writing this key; the reload goroutine picks
// them up within mcpRedactionReloadInterval.
const (
	mcpArgumentRedactionConfigScope = "gateway"
	mcpArgumentRedactionConfigKey   = "mcp.argument_redaction"
	mcpRedactionReloadInterval      = 30 * time.Second
)

// buildMCPArgumentRedactor returns a redactor whose rules are the
// baseline defaults merged with any policy-bundle overrides present
// in configsvc. When configsvc is unavailable or the key is empty,
// falls back to DefaultRedactor() — the auditor is never left with
// an empty rule set.
func (s *server) buildMCPArgumentRedactor(ctx context.Context) mcp.ArgumentRedactor {
	defaults := mcp.DefaultRedactionRules()
	overrides := s.loadMCPPolicyRedactionRules(ctx)
	if len(overrides) == 0 {
		return mcp.NewPolicyRedactor(defaults)
	}
	return mcp.NewPolicyRedactor(mcp.MergeRedactionRules(defaults, overrides))
}

// loadMCPPolicyRedactionRules pulls the configsvc document carrying
// the MCP argument-redaction rules, re-marshals it, and decodes it
// through LoadRulesFromPolicyBundle. Any failure path (no configsvc,
// missing document, malformed payload) returns nil so the caller
// merges with defaults only.
func (s *server) loadMCPPolicyRedactionRules(ctx context.Context) []mcp.RedactionRule {
	raw, err := s.loadMCPPolicyRedactionRaw(ctx)
	if err != nil || len(raw) == 0 {
		return nil
	}
	rules, err := mcp.LoadRulesFromPolicyBundle(raw)
	if err != nil {
		slog.Warn("mcp redaction rules malformed; using defaults only",
			"scope", mcpArgumentRedactionConfigScope,
			"key", mcpArgumentRedactionConfigKey,
			"error", err,
		)
		return nil
	}
	return rules
}

// loadMCPPolicyRedactionRaw returns the policy-bundle JSON bytes for
// the MCP argument-redaction document, or (nil, nil) when absent.
// Factored for the change-detection path.
func (s *server) loadMCPPolicyRedactionRaw(ctx context.Context) ([]byte, error) {
	if s == nil || s.configSvc == nil {
		return nil, nil
	}
	doc, err := s.configSvc.Get(ctx, configsvc.Scope(mcpArgumentRedactionConfigScope), mcpArgumentRedactionConfigKey)
	if err != nil {
		// Missing key is not an error — fall back to defaults silently.
		return nil, nil
	}
	if doc == nil || len(doc.Data) == 0 {
		return nil, nil
	}
	// Re-marshal the decoded document so LoadRulesFromPolicyBundle
	// can parse it via its tolerant envelope (either
	// policy.mcp.argument_redaction.rules or top-level rules).
	return json.Marshal(doc.Data)
}

// runMCPRedactionReload polls configsvc every
// mcpRedactionReloadInterval and calls auditor.SetRedactor when the
// loaded rules change. Runs until the gateway shuts down. Without
// this loop a new policy bundle would not take effect until the
// gateway restarted — violating the "Agent identity changes must
// immediately affect tool availability" class of rail.
func (s *server) runMCPRedactionReload(a mcp.ToolInvocationAuditor) {
	type setRedactor interface {
		SetRedactor(mcp.ArgumentRedactor)
	}
	setter, ok := a.(setRedactor)
	if !ok {
		return
	}
	ticker := time.NewTicker(mcpRedactionReloadInterval)
	defer ticker.Stop()
	var lastHash string
	for {
		select {
		case <-s.shutdownCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			raw, err := s.configSvcGetRaw(ctx)
			cancel()
			if err != nil {
				continue
			}
			hash := fnvHash(raw)
			if hash == lastHash {
				continue
			}
			lastHash = hash
			redactor := s.buildMCPArgumentRedactor(context.Background())
			setter.SetRedactor(redactor)
			slog.Info("mcp argument-redaction rules reloaded", "rules_bytes", len(raw))
		}
	}
}

// configSvcGetRaw is a thin wrapper so runMCPRedactionReload stays
// testable without reaching into configsvc directly.
func (s *server) configSvcGetRaw(ctx context.Context) ([]byte, error) {
	return s.loadMCPPolicyRedactionRaw(ctx)
}

// fnvHash is the 64-bit FNV-1a of the input, hex-encoded. Used only
// for change detection; not a security primitive.
func fnvHash(b []byte) string {
	h := fnv.New64a()
	if _, err := h.Write(b); err != nil {
		return ""
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func (s *server) setMCPRuntime(state *mcpRuntimeState) {
	if s == nil {
		return
	}
	if state == nil {
		gatewayMCPState.Delete(s)
		return
	}
	gatewayMCPState.Store(s, state)
}

func (s *server) stopMCPRuntime(state *mcpRuntimeState) {
	if s == nil || state == nil {
		return
	}
	state.stopOnce.Do(func() {
		if state.sweeperStop != nil {
			close(state.sweeperStop)
		}
		if state.httpTransport != nil {
			if err := state.httpTransport.Close(); err != nil {
				slog.Warn("mcp transport close failed", "error", err)
			}
		}
		if current := s.getMCPRuntime(); current == state {
			s.clearMCPRuntime()
		}
	})
}

func (s *server) getMCPRuntime() *mcpRuntimeState {
	if s == nil {
		return nil
	}
	raw, ok := gatewayMCPState.Load(s)
	if !ok {
		return nil
	}
	state, ok := raw.(*mcpRuntimeState)
	if !ok {
		slog.Error("mcp runtime state: sync.Map held unexpected type; returning nil")
		return nil
	}
	return state
}

func (s *server) clearMCPRuntime() {
	if s == nil {
		return
	}
	gatewayMCPState.Delete(s)
}

func mcpConfigTouched(data map[string]any) bool {
	if len(data) == 0 {
		return false
	}
	if _, ok := data["mcp"]; ok {
		return true
	}
	for key := range data {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "mcp.") {
			return true
		}
	}
	return false
}

func (s *server) reloadMCPConfig(ctx context.Context) {
	cfg := s.loadMCPConfig(ctx)
	runtime := s.getMCPRuntime()
	if !mcpHTTPRuntimeWanted(cfg) {
		if runtime != nil {
			s.stopMCPRuntime(runtime)
		}
		return
	}
	if runtime == nil || runtime.server == nil || (runtime.httpTransport != nil && runtime.httpTransport.IsClosed()) {
		if err := s.startMCPRuntimeFromConfig(cfg); err != nil {
			slog.Error("mcp runtime start after config change failed", "error", err)
		}
		return
	}
	if runtime.toolRegistry != nil {
		runtime.toolRegistry.SetConfig(cfg.Raw)
	}
	if runtime.resourceRegistry != nil {
		runtime.resourceRegistry.SetConfig(cfg.Raw)
	}
	runtime.server.ReloadConfig(cfg.Raw)
}

func lookupBoolPath(data map[string]any, keys ...string) (bool, bool) {
	raw, ok := lookupAnyPath(data, keys...)
	if !ok {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true, true
		case "0", "false", "no", "off":
			return false, true
		}
	}
	return false, false
}

func lookupStringPath(data map[string]any, keys ...string) (string, bool) {
	raw, ok := lookupAnyPath(data, keys...)
	if !ok {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v), true
	case []byte:
		return strings.TrimSpace(string(v)), true
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", raw)), true
	}
}

func lookupAnyPath(data map[string]any, keys ...string) (any, bool) {
	if data == nil || len(keys) == 0 {
		return nil, false
	}
	var cur any = data
	for _, key := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// ---------------------------------------------------------------------------
// MCP service bridge — closure-based bridge from MCP tools to HTTP handlers
// ---------------------------------------------------------------------------

// wireMCPApprovalGate constructs the production MCP approval gate with
// per-tool wiring (preapproval lookup + invariant lookup). EDGE-052
// reopen #1 extracted this from the inline registerMCPHandlers block so
// the regression test in mcp_gate_invariants_test.go can verify that the
// production wiring path attaches the invariant lookup automatically —
// previously the gate was constructed inline and the call to
// WithInvariantLookup was omitted in production, leaving every
// admin-authored secops/invariants MCP rule silently ignored at the
// gate (DoD #4 failure).
//
// invariantLookup MUST be non-nil for the security-floor contract; the
// production caller passes a closure reading from loadPolicyBundles +
// loadMCPInvariantsFromBundles, the test caller passes a static stub.
//
// Returns the rawGate (mcp.ApprovalGate interface — what the registry
// actually consumes) AND the underlying *gatewayApprovalGate (for the
// caller's logging + the test's field-level assertions). The concrete
// pointer is nil when NewGatewayApprovalGate ever returns a different
// type — caller treats that as the degraded path.
func (s *server) wireMCPApprovalGate(approvalStore *MCPApprovalStore, invariantLookup MCPInvariantLookup) (mcp.ApprovalGate, *gatewayApprovalGate) {
	rawGate := NewGatewayApprovalGate(approvalStore)
	gate, ok := rawGate.(*gatewayApprovalGate)
	if !ok {
		return rawGate, nil
	}
	if s.agentIdentityStore != nil {
		gate.preapproval = newAgentIdentityPreapprovalLookup(s.agentIdentityStore)
	}
	if invariantLookup != nil {
		gate.WithInvariantLookup(invariantLookup)
	}
	// EDGE-103 reopen #1: wire Edge approval mint so the gate can
	// dual-write a consumable EdgeApproval alongside the legacy
	// MCPApprovalStore record. Falls back to legacy-only when the
	// Edge store is nil (dev/test) or when the request lacks
	// mcp.CallMetadata SessionID/ExecutionID (transports without an
	// EdgeSession). The same snapshot provider used by WithApprovalHold
	// is reused so the mint + consume paths bind to identical
	// PolicySnapshot strings.
	if s.edgeStore != nil {
		gate.WithEdgeApprovalMint(s.edgeStore, s.mcpPolicySnapshotFunc(), mcpPolicyServerName)
	}
	return gate, gate
}

func (s *server) newMCPServiceBridge() mcp.ServiceBridge {
	if s == nil {
		return mcp.NewDirectServiceBridge(mcp.DirectServiceBridgeConfig{})
	}
	return mcp.NewDirectServiceBridge(mcp.DirectServiceBridgeConfig{
		SubmitJobFunc: func(ctx context.Context, req mcp.SubmitJobInput) (*mcp.SubmitJobOutput, error) {
			body := map[string]any{
				"prompt":   req.Prompt,
				"topic":    req.Topic,
				"priority": req.Priority,
			}
			if strings.TrimSpace(req.Capability) != "" {
				body["capability"] = strings.TrimSpace(req.Capability)
			}
			if len(req.RiskTags) > 0 {
				body["risk_tags"] = append([]string{}, req.RiskTags...)
			}
			if len(req.Labels) > 0 {
				body["labels"] = req.Labels
			}
			if strings.TrimSpace(req.MemoryID) != "" {
				body["memory_id"] = strings.TrimSpace(req.MemoryID)
			}
			if strings.TrimSpace(req.PackID) != "" {
				body["pack_id"] = strings.TrimSpace(req.PackID)
			}

			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodPost, "/api/v1/jobs", nil, nil, body, s.handleSubmitJobHTTP)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			jobID := strings.TrimSpace(mcpBridgeString(payload["job_id"]))
			if jobID == "" {
				return nil, mcp.NewBridgeError(http.StatusBadGateway, "invalid_response", "missing job_id in submit response", payload)
			}
			return &mcp.SubmitJobOutput{
				JobID:   jobID,
				TraceID: strings.TrimSpace(mcpBridgeString(payload["trace_id"])),
			}, nil
		},
		CancelJobFunc: func(ctx context.Context, jobID string, reason string) error {
			body := map[string]any{}
			if strings.TrimSpace(reason) != "" {
				body["reason"] = strings.TrimSpace(reason)
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/jobs/"+jobID+"/cancel",
				nil,
				map[string]string{"id": jobID},
				body,
				s.handleCancelJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			if state := strings.TrimSpace(mcpBridgeString(payload["state"])); state != "" && !strings.EqualFold(state, "cancelled") {
				return mcp.NewBridgeError(http.StatusConflict, "job_already_completed", "job already completed", payload)
			}
			return nil
		},
		TriggerWorkflowFunc: func(ctx context.Context, req mcp.TriggerWorkflowInput) (*mcp.TriggerOutput, error) {
			target := "/api/v1/workflows/" + req.WorkflowID + "/runs"
			if req.DryRun {
				target += "?dry_run=true"
			}
			headers := map[string]string{}
			if strings.TrimSpace(req.IdempotencyKey) != "" {
				headers["Idempotency-Key"] = strings.TrimSpace(req.IdempotencyKey)
			}
			input := req.Input
			if input == nil {
				input = map[string]any{}
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				target,
				headers,
				map[string]string{"id": req.WorkflowID},
				input,
				s.handleStartRun,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				be := mcp.NewBridgeErrorFromHTTP(status, raw)
				if status == http.StatusBadRequest && strings.Contains(strings.ToLower(be.Message), "input schema validation failed") {
					return nil, mcp.NewBridgeError(http.StatusUnprocessableEntity, "input_validation_failed", be.Message, be.Details)
				}
				return nil, be
			}
			runID := strings.TrimSpace(mcpBridgeString(payload["run_id"]))
			if runID == "" {
				return nil, mcp.NewBridgeError(http.StatusBadGateway, "invalid_response", "missing run_id in workflow response", payload)
			}
			return &mcp.TriggerOutput{
				RunID:      runID,
				WorkflowID: strings.TrimSpace(req.WorkflowID),
			}, nil
		},
		ApproveJobFunc: func(ctx context.Context, jobID string, note string) error {
			body := map[string]any{}
			if strings.TrimSpace(note) != "" {
				body["note"] = strings.TrimSpace(note)
			}
			status, _, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/approvals/"+jobID+"/approve",
				nil,
				map[string]string{"job_id": jobID},
				body,
				s.handleApproveJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			return nil
		},
		RejectJobFunc: func(ctx context.Context, jobID string, reason string) error {
			body := map[string]any{"reason": strings.TrimSpace(reason)}
			status, _, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodPost,
				"/api/v1/approvals/"+jobID+"/reject",
				nil,
				map[string]string{"job_id": jobID},
				body,
				s.handleRejectJob,
			)
			if err != nil {
				return err
			}
			if status < 200 || status >= 300 {
				return mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			return nil
		},
		SimulatePolicyFunc: func(ctx context.Context, req mcp.PolicySimInput) (*mcp.PolicySimOutput, error) {
			tenantID := s.mcpTenantFromContext(ctx)
			body := map[string]any{
				"topic":    req.Topic,
				"tenant":   tenantID,
				"org_id":   tenantID,
				"priority": req.Priority,
				"meta": map[string]any{
					"tenant_id":  tenantID,
					"capability": strings.TrimSpace(req.Capability),
					"risk_tags":  append([]string{}, req.RiskTags...),
					"labels":     req.Labels,
				},
			}
			if len(req.Labels) > 0 {
				body["labels"] = req.Labels
			}
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodPost, "/api/v1/policy/simulate", nil, nil, body, s.handlePolicySimulate)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := &mcp.PolicySimOutput{
				Decision: strings.TrimSpace(mcpBridgeString(payload["decision"])),
				Reason:   strings.TrimSpace(mcpBridgeString(payload["reason"])),
				RuleID:   strings.TrimSpace(mcpBridgeFirstString(payload, "ruleId", "rule_id")),
			}
			if constraints, ok := payload["constraints"].(map[string]any); ok {
				out.Constraints = constraints
			} else {
				out.Constraints = map[string]any{}
			}
			if rems, ok := payload["remediations"].([]any); ok {
				out.Remediations = make([]map[string]any, 0, len(rems))
				for _, item := range rems {
					if m, ok := item.(map[string]any); ok {
						out.Remediations = append(out.Remediations, m)
					}
				}
			} else {
				out.Remediations = []map[string]any{}
			}
			return out, nil
		},
	})
}

func (s *server) invokeMCPJSONHandler(
	ctx context.Context,
	method string,
	target string,
	headers map[string]string,
	pathValues map[string]string,
	body any,
	handler http.HandlerFunc,
) (int, map[string]any, []byte, error) {
	if handler == nil {
		return 0, nil, nil, fmt.Errorf("handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode body: %w", err)
		}
		payload = encoded
	}

	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req = req.WithContext(ctx)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if status, payload, raw, ok := s.stampMCPBridgeTenant(req, ctx); !ok {
		return status, payload, raw, nil
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range pathValues {
		req.SetPathValue(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	rr := httptest.NewRecorder()
	handler(rr, req)

	raw := rr.Body.Bytes()
	decoded := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return rr.Code, decoded, raw, nil
}

func (s *server) invokeMCPAnyHandler(
	ctx context.Context,
	method string,
	target string,
	headers map[string]string,
	pathValues map[string]string,
	body any,
	handler http.HandlerFunc,
) (int, any, []byte, error) {
	if handler == nil {
		return 0, nil, nil, fmt.Errorf("handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("encode body: %w", err)
		}
		payload = encoded
	}

	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req = req.WithContext(ctx)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if status, payload, raw, ok := s.stampMCPBridgeTenant(req, ctx); !ok {
		return status, payload, raw, nil
	}
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	for key, value := range pathValues {
		req.SetPathValue(strings.TrimSpace(key), strings.TrimSpace(value))
	}

	rr := httptest.NewRecorder()
	handler(rr, req)

	raw := rr.Body.Bytes()
	var decoded any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return rr.Code, decoded, raw, nil
}

func (s *server) stampMCPBridgeTenant(req *http.Request, ctx context.Context) (int, map[string]any, []byte, bool) {
	if req.Header.Get("X-Tenant-ID") != "" {
		return 0, nil, nil, true
	}
	tenant := s.mcpTenantFromContext(ctx)
	if tenant == "" {
		status, payload, raw := mcpMissingTenantBridgeResponse()
		return status, payload, raw, false
	}
	req.Header.Set("X-Tenant-ID", tenant)
	return 0, nil, nil, true
}

func mcpMissingTenantBridgeResponse() (int, map[string]any, []byte) {
	payload := map[string]any{
		"code":  "unauthorized",
		"error": "mcp bridge requires tenant context",
	}
	raw, _ := json.Marshal(payload)
	return http.StatusUnauthorized, payload, raw
}

// mcpTenantFromContext resolves only explicit auth/server tenant state.
// It deliberately returns blank rather than model.DefaultTenant so bridge
// invocations fail closed when middleware/auth context is missing.
func (s *server) mcpTenantFromContext(ctx context.Context) string {
	if auth := auth.FromContext(ctx); auth != nil {
		if tenant := strings.TrimSpace(auth.Tenant); tenant != "" {
			return tenant
		}
	}
	if s != nil {
		if tenant := strings.TrimSpace(s.tenant); tenant != "" {
			return tenant
		}
	}
	return ""
}

func mcpBridgeString(v any) string {
	if v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", v)
	}
}

func mcpBridgeFirstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if val := strings.TrimSpace(mcpBridgeString(data[key])); val != "" {
			return val
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// MCP data bridge — closure-based bridge from MCP resources to HTTP handlers
// ---------------------------------------------------------------------------

func (s *server) newMCPDataBridge() mcp.DataBridge {
	if s == nil {
		return mcp.NewDirectDataBridge(mcp.DirectDataBridgeConfig{})
	}
	return mcp.NewDirectDataBridge(mcp.DirectDataBridgeConfig{
		GetJobFunc: func(ctx context.Context, id string) (*mcp.JobDetail, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodGet,
				"/api/v1/jobs/"+strings.TrimSpace(id),
				nil,
				map[string]string{"id": strings.TrimSpace(id)},
				nil,
				s.handleGetJob,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := mcp.JobDetail(payload)
			return &out, nil
		},
		ListJobsFunc: func(ctx context.Context, opts mcp.JobListOpts) (*mcp.JobList, error) {
			values := []string{}
			if opts.Limit > 0 {
				values = append(values, "limit="+strconv.Itoa(opts.Limit))
			}
			if status := strings.TrimSpace(opts.Status); status != "" {
				values = append(values, "state="+status)
			}
			if opts.Cursor > 0 {
				values = append(values, "cursor="+strconv.FormatInt(opts.Cursor, 10))
			}
			target := "/api/v1/jobs"
			if len(values) > 0 {
				target += "?" + strings.Join(values, "&")
			}

			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, target, nil, nil, nil, s.handleListJobs)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(payload["items"])
			out := &mcp.JobList{Items: items}
			if next, ok := mcpGatewayToInt64(payload["next_cursor"]); ok {
				out.NextCursor = &next
			}
			return out, nil
		},
		ListWorkflowRunsFunc: func(ctx context.Context, wfID string, limit int) (*mcp.RunList, error) {
			target := "/api/v1/workflows/" + strings.TrimSpace(wfID) + "/runs"
			if limit > 0 {
				target += "?limit=" + strconv.Itoa(limit)
			}
			status, payload, raw, err := s.invokeMCPAnyHandler(
				ctx,
				http.MethodGet,
				target,
				nil,
				map[string]string{"id": strings.TrimSpace(wfID)},
				nil,
				s.handleListRuns,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := []map[string]any{}
			switch typed := payload.(type) {
			case []any:
				items = mcpGatewayToMapSlice(typed)
			case map[string]any:
				items = mcpGatewayToMapSlice(typed["items"])
			}
			return &mcp.RunList{
				WorkflowID: strings.TrimSpace(wfID),
				Items:      items,
			}, nil
		},
		GetWorkflowRunFunc: func(ctx context.Context, wfID, runID string) (*mcp.RunDetail, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(
				ctx,
				http.MethodGet,
				"/api/v1/workflow-runs/"+strings.TrimSpace(runID),
				nil,
				map[string]string{"id": strings.TrimSpace(runID)},
				nil,
				s.handleGetRun,
			)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			if expected := strings.TrimSpace(wfID); expected != "" {
				if actual := strings.TrimSpace(mcpBridgeString(payload["workflow_id"])); actual != "" && actual != expected {
					return nil, mcp.NewBridgeError(http.StatusNotFound, "not_found", "workflow run not found", nil)
				}
			}
			out := mcp.RunDetail(payload)
			return &out, nil
		},
		ListAuditEntriesFunc: func(ctx context.Context, limit int) ([]mcp.AuditEntry, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/audit", nil, nil, nil, s.handleListPolicyAudit)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(payload["items"])
			if limit <= 0 {
				limit = 50
			}
			if len(items) > limit {
				items = items[:limit]
			}
			out := make([]mcp.AuditEntry, 0, len(items))
			for _, item := range items {
				out = append(out, mcp.AuditEntry(item))
			}
			return out, nil
		},
		GetSystemHealthFunc: func(ctx context.Context) (*mcp.HealthStatus, error) {
			status, payload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/status", nil, nil, nil, s.handleStatus)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			out := mcp.HealthStatus(payload)
			return &out, nil
		},
		ListPoliciesSummaryFn: func(ctx context.Context) (*mcp.PolicySummary, error) {
			status, bundlesPayload, raw, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/bundles", nil, nil, nil, s.handlePolicyBundles)
			if err != nil {
				return nil, err
			}
			if status < 200 || status >= 300 {
				return nil, mcp.NewBridgeErrorFromHTTP(status, raw)
			}
			items := mcpGatewayToMapSlice(bundlesPayload["items"])

			currentSnapshot := ""
			if status, snapshotsPayload, _, err := s.invokeMCPJSONHandler(ctx, http.MethodGet, "/api/v1/policy/snapshots", nil, nil, nil, s.handlePolicySnapshots); err == nil {
				if status >= 200 && status < 300 {
					if snapshots, ok := snapshotsPayload["snapshots"].([]any); ok && len(snapshots) > 0 {
						currentSnapshot = strings.TrimSpace(mcpBridgeString(snapshots[0]))
					}
				}
			}

			summary := mcp.PolicySummary{
				"active_bundles":      items,
				"current_snapshot_id": currentSnapshot,
				"safety_stance":       mcpGatewayInferSafetyStance(items),
			}
			return &summary, nil
		},
	})
}

func mcpGatewayToMapSlice(raw any) []map[string]any {
	if raw == nil {
		return []map[string]any{}
	}
	list, ok := raw.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func mcpGatewayToInt64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func mcpGatewayInferSafetyStance(items []map[string]any) string {
	if len(items) == 0 {
		return "permissive"
	}
	enabled := 0
	var totalRules int64
	for _, item := range items {
		if v, ok := item["enabled"].(bool); ok && v {
			enabled++
		}
		if rc, ok := mcpGatewayToInt64(item["rule_count"]); ok {
			totalRules += rc
		}
	}
	if enabled == 0 || totalRules == 0 {
		return "permissive"
	}
	if totalRules >= 20 {
		return "strict"
	}
	return "balanced"
}
