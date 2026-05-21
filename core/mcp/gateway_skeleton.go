package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cordum/cordum/core/edge"
)

// EDGE-100 MCP Gateway skeleton.
//
// This file is intentionally a skeleton: it registers the route family
// `/api/v1/mcp/gateway/*` on a supplied `*http.ServeMux`, wires
// EdgeSession + AgentExecution + connect-event creation on client connect,
// and keeps upstream forwarding disabled by default. EDGE-101 will populate
// the upstream registry; EDGE-104 wires the client attach flow; EDGE-105
// adds dashboard surfaces. None of those concerns belong here.
//
// Per governor amendment comment-a305f5a3:
//   - Routes register from `core/controlplane/gateway/gateway.go` main mux
//     by calling RegisterGatewayRoutes; no new gateway_mcp.go or
//     handlers_mcp_gateway.go is introduced under core/controlplane/gateway.
//   - GatewayEnabled gating is per-tenant via MCPPolicy.GatewayEnabled
//     (core/infra/config/safety_policy.go); deps surface it as a callback
//     so we don't couple the library to the config package.

// GatewayDeps is the injectable dependency surface for the MCP Gateway
// skeleton. Use function fields rather than interfaces for the boundary
// pieces (tenant resolution, gateway gating) so callers can adapt their
// existing gateway helpers without inventing wrapper types; use the
// edge.Store interface directly for the persistence boundary so the
// gateway shares the same evidence store as the rest of /api/v1/edge.
type GatewayDeps struct {
	// Store is the Edge evidence store. The gateway only invokes
	// CreateSession, CreateExecution, and AppendEvent on it.
	Store edge.Store

	// UpstreamRegistry stores approved tenant upstreams. When configured,
	// HandleUpstream revalidates the selected enabled upstream immediately
	// before use so DNS rebinding cannot bypass registration-time checks.
	UpstreamRegistry edge.MCPUpstreamRegistry

	// GatewayEnabled returns the per-tenant EDGE-100 gateway flag. Callers
	// typically resolve this through MCPPolicy.GatewayEnabled.
	GatewayEnabled func(ctx context.Context, tenantID string) (bool, error)

	// ResolveTenant maps an incoming request to (tenantID, principalID)
	// using the API gateway's existing X-Tenant-ID + auth-context plumbing.
	// Returning a non-nil error MUST be treated as a 401/403 by the caller
	// — the gateway will not infer a tenant from request body claims.
	ResolveTenant func(r *http.Request) (tenantID, principalID string, err error)

	// Logger is optional; if nil a discard logger is substituted.
	Logger *slog.Logger
}

// RegisterGatewayRoutes binds the EDGE-100 MCP Gateway skeleton route family
// onto the supplied mux. Returns an error if deps are missing pieces required
// for safe operation. Idempotent across separate mux instances; not safe to
// call twice on the same mux (http.ServeMux panics on duplicate patterns).
func RegisterGatewayRoutes(mux *http.ServeMux, deps GatewayDeps) error {
	if mux == nil {
		return errors.New("mcp gateway: mux is required")
	}
	if deps.Store == nil {
		return errors.New("mcp gateway: deps.Store is required")
	}
	if deps.GatewayEnabled == nil {
		return errors.New("mcp gateway: deps.GatewayEnabled is required")
	}
	if deps.ResolveTenant == nil {
		return errors.New("mcp gateway: deps.ResolveTenant is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}

	g := &Gateway{deps: deps}
	mux.HandleFunc("GET /api/v1/mcp/gateway/health", g.HandleHealth)
	mux.HandleFunc("GET /api/v1/mcp/gateway/config", g.HandleConfig)
	mux.HandleFunc("POST /api/v1/mcp/gateway/upstream/", g.HandleUpstream)
	mux.HandleFunc("POST /api/v1/mcp/gateway/clients/connect", g.HandleClientConnect)
	return nil
}

// NewGateway constructs the EDGE-100 MCP Gateway skeleton's handler state
// without registering routes. Callers that need to wire each handler
// through the API gateway's existing s.registerRoute + s.instrumented
// helpers (rather than RegisterGatewayRoutes' raw mux.HandleFunc path)
// should use this constructor and bind each Handle* method individually.
func NewGateway(deps GatewayDeps) (*Gateway, error) {
	if deps.Store == nil {
		return nil, errors.New("mcp gateway: deps.Store is required")
	}
	if deps.GatewayEnabled == nil {
		return nil, errors.New("mcp gateway: deps.GatewayEnabled is required")
	}
	if deps.ResolveTenant == nil {
		return nil, errors.New("mcp gateway: deps.ResolveTenant is required")
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	return &Gateway{deps: deps}, nil
}

// Gateway is the EDGE-100 MCP Gateway skeleton's exported handler state.
// All four Handle* methods are bound by RegisterGatewayRoutes for direct
// testing, and individually wirable by the API gateway's main mux for
// production deployment.
type Gateway struct {
	deps GatewayDeps
}

type gatewayEvidenceRoot struct {
	Session   edge.EdgeSession
	Execution edge.AgentExecution
}

// handleHealth reports whether upstream forwarding is enabled for the
// authenticated requesting tenant. Tenant resolution failures are 401s,
// matching the other gateway handlers so unauthenticated probes do not
// learn this endpoint's tenant-scoped gateway state. Never touches the Store.
func (g *Gateway) HandleHealth(w http.ResponseWriter, r *http.Request) {
	tenantID, _, err := g.deps.ResolveTenant(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant_missing", "X-Tenant-ID required")
		return
	}
	enabled := false
	ok, err := g.deps.GatewayEnabled(r.Context(), tenantID)
	if err == nil {
		enabled = ok
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"gateway_enabled": enabled,
		"component":       "mcp-gateway",
	})
}

// handleConfig returns the effective per-tenant gateway config. Only
// non-secret fields are emitted; this route never echoes upstream
// credentials, bearer tokens, or API keys.
func (g *Gateway) HandleConfig(w http.ResponseWriter, r *http.Request) {
	tenantID, _, err := g.deps.ResolveTenant(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant_missing", "X-Tenant-ID required")
		return
	}
	enabled, err := g.deps.GatewayEnabled(r.Context(), tenantID)
	if err != nil {
		g.deps.Logger.Warn("mcp gateway: enable-lookup failed", "tenant", tenantID, "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway_enabled":     enabled,
		"upstream_count":      g.upstreamCount(r.Context(), tenantID),
		"upstream_forwarding": "no-op", // skeleton only; EDGE-101 enables
	})
}

// handleUpstream covers the /upstream/* route family. The skeleton always
// returns 503 with one of two diagnostic strings:
//   - "gateway disabled" when GatewayEnabled is false for the tenant
//   - "no upstream configured" when GatewayEnabled is true (registry is
//     empty in P1 until EDGE-101 populates it)
//
// Disabled tenants produce no Store writes. Enabled tenants with no upstream
// registry produce one evidence root plus a failed event so the skeleton's
// production-shaped failure path is auditable.
func (g *Gateway) HandleUpstream(w http.ResponseWriter, r *http.Request) {
	tenantID, principalID, err := g.deps.ResolveTenant(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant_missing", "X-Tenant-ID required")
		return
	}
	enabled, _ := g.deps.GatewayEnabled(r.Context(), tenantID)
	if !enabled {
		writeError(w, http.StatusServiceUnavailable, "gateway_disabled",
			"mcp gateway disabled for tenant — enable via MCPPolicy.GatewayEnabled")
		return
	}
	now := time.Now().UTC()
	root, err := g.createGatewayEvidenceRoot(r.Context(), tenantID, principalID, now)
	if err != nil {
		g.deps.Logger.Warn("mcp gateway: upstream evidence root create failed", "tenant", tenantID, "err", err)
		writeError(w, http.StatusInternalServerError, "evidence_create_failed", "could not create gateway evidence root")
		return
	}
	if _, err := g.appendGatewayEvent(r.Context(), root, edge.EventKindMCPServerFailed, edge.DecisionDeny, edge.ActionStatusFailed, now); err != nil {
		g.deps.Logger.Warn("mcp gateway: upstream failed-event append failed", "tenant", tenantID, "session", root.Session.SessionID, "err", err)
		writeError(w, http.StatusInternalServerError, "event_append_failed", "could not persist mcp server failed event")
		return
	}
	if upstream, ok, err := g.selectUsableUpstream(r.Context(), tenantID); err != nil {
		g.deps.Logger.Warn("mcp gateway: upstream revalidation failed", "tenant", tenantID, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":        strings.ToLower(http.StatusText(http.StatusBadGateway)),
			"code":         "unsafe_upstream_endpoint",
			"message":      "mcp gateway: upstream endpoint failed use-time validation",
			"session_id":   root.Session.SessionID,
			"execution_id": root.Execution.ExecutionID,
		})
		return
	} else if ok {
		g.deps.Logger.Warn("mcp gateway: upstream forwarding remains disabled", "tenant", tenantID, "upstream", upstream.Name)
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error":        strings.ToLower(http.StatusText(http.StatusServiceUnavailable)),
		"code":         "no_upstream_configured",
		"message":      "mcp gateway: no upstream configured — register via EDGE-101 upstream registry",
		"session_id":   root.Session.SessionID,
		"execution_id": root.Execution.ExecutionID,
	})
}

// handleClientConnect resolves tenant + principal from the request, creates
// an EdgeSession + AgentExecution, and emits an
// EventKindMCPServerConnected event with the resolved tenant/principal —
// never from request-body claims (task rail #3). Store failures before the
// evidence root exists are logged and surfaced as 500; they are not claimed
// as persisted orphan events because RedisStore rejects missing executions.
func (g *Gateway) HandleClientConnect(w http.ResponseWriter, r *http.Request) {
	tenantID, principalID, err := g.deps.ResolveTenant(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "tenant_missing", "X-Tenant-ID required")
		return
	}

	now := time.Now().UTC()
	root, err := g.createGatewayEvidenceRoot(r.Context(), tenantID, principalID, now)
	if err != nil {
		g.deps.Logger.Warn("mcp gateway: evidence root create failed", "tenant", tenantID, "err", err)
		writeError(w, http.StatusInternalServerError, "evidence_create_failed", "could not create gateway evidence root")
		return
	}
	if _, err := g.appendGatewayEvent(r.Context(), root, edge.EventKindMCPServerConnected, edge.DecisionRecorded, edge.ActionStatusOK, now); err != nil {
		g.deps.Logger.Warn("mcp gateway: connect event append failed", "tenant", tenantID, "session", root.Session.SessionID, "err", err)
		writeError(w, http.StatusInternalServerError, "event_append_failed", "could not persist mcp server connected event")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":   root.Session.SessionID,
		"execution_id": root.Execution.ExecutionID,
		"tenant_id":    tenantID,
	})
}

func (g *Gateway) createGatewayEvidenceRoot(ctx context.Context, tenantID, principalID string, ts time.Time) (gatewayEvidenceRoot, error) {
	sessionID, sErr := newGatewayID("egs")
	if sErr != nil {
		return gatewayEvidenceRoot{}, fmt.Errorf("generate session id: %w", sErr)
	}
	executionID, eErr := newGatewayID("aex")
	if eErr != nil {
		return gatewayEvidenceRoot{}, fmt.Errorf("generate execution id: %w", eErr)
	}

	session := edge.EdgeSession{
		SessionID:     sessionID,
		TenantID:      tenantID,
		PrincipalID:   principalID,
		PrincipalType: edge.PrincipalTypeService,
		AgentProduct:  "cordum-mcp-gateway",
		Mode:          edge.SessionModeLocalDev,
		PolicyMode:    edge.PolicyModeObserve,
		StartedAt:     ts,
		Status:        edge.SessionStatusRunning,
		RiskSummary: edge.RiskSummary{
			MaxRisk: edge.RiskLevelLow,
		},
	}
	if err := g.deps.Store.CreateSession(ctx, session); err != nil {
		return gatewayEvidenceRoot{}, fmt.Errorf("create session: %w", err)
	}
	execution := edge.AgentExecution{
		ExecutionID: executionID,
		SessionID:   sessionID,
		TenantID:    tenantID,
		Adapter:     edge.AdapterMCPGateway,
		Mode:        edge.ExecutionModeLocalDev,
		StartedAt:   ts,
		Status:      edge.ExecutionStatusRunning,
	}
	if err := g.deps.Store.CreateExecution(ctx, execution); err != nil {
		return gatewayEvidenceRoot{}, fmt.Errorf("create execution: %w", err)
	}
	return gatewayEvidenceRoot{Session: session, Execution: execution}, nil
}

func (g *Gateway) appendGatewayEvent(ctx context.Context, root gatewayEvidenceRoot, kind edge.EventKind, decision edge.EdgeDecision, status edge.ActionStatus, ts time.Time) (edge.AgentActionEvent, error) {
	event := edge.AgentActionEvent{
		EventID:     mustGatewayID("aev"),
		SessionID:   root.Session.SessionID,
		ExecutionID: root.Execution.ExecutionID,
		TenantID:    root.Session.TenantID,
		PrincipalID: root.Session.PrincipalID,
		Timestamp:   ts,
		Layer:       edge.LayerMCP,
		Kind:        kind,
		Decision:    decision,
		Status:      status,
	}
	return g.deps.Store.AppendEvent(ctx, event)
}

// writeJSON serialises v as JSON with Content-Type and the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits the gateway's standard {error, code, message} JSON
// envelope at the given status.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error":   strings.ToLower(http.StatusText(status)),
		"code":    code,
		"message": message,
	})
}

// newGatewayID returns a short prefixed hex id. crypto/rand is the source so
// IDs are unguessable even when emitted in audit-readable form.
var gatewayRandRead = rand.Read
var gatewayIDCounter atomic.Uint64

func newGatewayID(prefix string) (string, error) {
	var b [12]byte
	if _, err := gatewayRandRead(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}

// mustGatewayID returns a newGatewayID or falls back to a PID+counter suffix.
// Only used on the event-emission path where a Store-side validation will
// still catch malformed IDs.
func mustGatewayID(prefix string) string {
	id, err := newGatewayID(prefix)
	if err == nil {
		return id
	}
	return prefix + "_fb_" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatUint(gatewayIDCounter.Add(1), 36)
}
