package gateway

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

// MCP approval HTTP handlers.
//
// Endpoints (all admin-only — enforced by requireRole elsewhere, same as
// the job-approval endpoints):
//   POST /api/v1/mcp/approvals/{id}/approve   body: {"reason":"..."}
//   POST /api/v1/mcp/approvals/{id}/reject    body: {"reason":"..."}
//   GET  /api/v1/mcp/approvals/{id}           returns the record (for UI)
//
// The self-approval guard mirrors handleApproveJob's pattern: the
// approver's composite identity (API-key hash + principal ID) is
// compared against the approval's Requester via identitiesOverlap.
// Match → HTTP 403 + code=self_approval_denied + audit entry.

// mcpApprovalHandler is a small struct so tests can inject a fake store
// without standing up the full server. Production code constructs it
// with the server's store + auth middleware.
type mcpApprovalHandler struct {
	store *MCPApprovalStore
	// getApproverIdentity returns the composite identity of the HTTP
	// caller. In production it is submitterIdentity(r); tests override
	// with a deterministic stub.
	getApproverIdentity func(r *http.Request) string
	// approverPrincipalID returns the display ID for logging/audit.
	approverPrincipalID func(r *http.Request) string
	// approverRole returns the role for audit.
	approverRole func(r *http.Request) string
}

// newMCPApprovalHandler wires the production handler. Used from
// server-setup code to register routes on the gateway mux.
func newMCPApprovalHandler(store *MCPApprovalStore) *mcpApprovalHandler {
	return &mcpApprovalHandler{
		store:               store,
		getApproverIdentity: submitterIdentity,
		approverPrincipalID: policybundles.PolicyActorID,
		approverRole:        policybundles.PolicyRole,
	}
}

type mcpApprovalDecisionBody struct {
	Reason string `json:"reason,omitempty"`
}

// Approve handles POST /api/v1/mcp/approvals/{id}/approve.
func (h *mcpApprovalHandler) Approve(w http.ResponseWriter, r *http.Request, id string) {
	h.resolve(w, r, id, model.ApprovalDecisionApprove)
}

// Reject handles POST /api/v1/mcp/approvals/{id}/reject.
func (h *mcpApprovalHandler) Reject(w http.ResponseWriter, r *http.Request, id string) {
	h.resolve(w, r, id, model.ApprovalDecisionReject)
}

// Get handles GET /api/v1/mcp/approvals/{id} — returns the record JSON
// for the dashboard modal.
func (h *mcpApprovalHandler) Get(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	rec, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeJSONError(w, http.StatusNotFound, "approval_not_found", "no such mcp approval")
			return
		}
		writeMCPApprovalStoreError(w, r, "get mcp approval", err, slog.String("approval_id", id))
		return
	}
	// Tenant scoping — callers can only see approvals for their own
	// tenant unless AllowCrossTenant is set on the auth context.
	if !h.callerMayViewTenant(r, rec.Tenant) {
		writeJSONError(w, http.StatusNotFound, "approval_not_found", "no such mcp approval")
		return
	}
	writeJSONObject(w, http.StatusOK, rec)
}

// mcpApprovalListMax bounds the page size to protect the gateway from
// a client requesting an unbounded Redis SCAN.
const mcpApprovalListMax = 200

// List handles GET /api/v1/mcp/approvals[?status=pending&limit=50].
// Returns `{items: []MCPApprovalRecord, next_cursor: "..."}`. The cursor
// is opaque to the client and round-trips through subsequent calls for
// pagination. Tenant-scoped: results are filtered to the caller's tenant.
func (h *mcpApprovalHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	limit := mcpApprovalListMax
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v < mcpApprovalListMax {
			limit = v
		}
	}
	cursor := uint64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			cursor = v
		}
	}

	items, next, err := h.store.ListByStatus(ctx, statusFilter, cursor, int64(limit))
	if err != nil {
		writeMCPApprovalStoreError(w, r, "list mcp approvals", err, slog.String("status", statusFilter))
		return
	}

	// Tenant scope — drop records the caller may not see.
	filtered := make([]*MCPApprovalRecord, 0, len(items))
	for _, rec := range items {
		if h.callerMayViewTenant(r, rec.Tenant) {
			filtered = append(filtered, rec)
		}
	}
	writeJSONObject(w, http.StatusOK, map[string]any{
		"items":       filtered,
		"next_cursor": next,
	})
}

// callerMayViewTenant returns true when the authenticated caller is
// allowed to see approvals for the given tenant. Cross-tenant admins
// always pass; otherwise the caller's tenant must match exactly.
func (h *mcpApprovalHandler) callerMayViewTenant(r *http.Request, tenant string) bool {
	auth := auth.FromRequest(r)
	if auth == nil {
		return false
	}
	if auth.AllowCrossTenant {
		return true
	}
	return strings.TrimSpace(auth.Tenant) == strings.TrimSpace(tenant)
}

// resolve is the shared approve/reject body that enforces the self-
// approval guard before delegating to MCPApprovalStore.Resolve.
func (h *mcpApprovalHandler) resolve(w http.ResponseWriter, r *http.Request, id string, decision model.ApprovalDecision) {
	ctx := r.Context()
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "approval id is required")
		return
	}
	rec, err := h.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			writeJSONError(w, http.StatusNotFound, "approval_not_found", "no such mcp approval")
			return
		}
		writeMCPApprovalStoreError(w, r, "load mcp approval for resolution", err, slog.String("approval_id", id))
		return
	}

	// Self-approval guard — mirrors handleApproveJob. The approver's
	// composite identity (API-key hash + principal ID) must not overlap
	// the approval's Requester. New records carry the same composite
	// requester shape; legacy records may still contain a plain principal.
	approverIdentity := h.getApproverIdentity(r)
	if rec.Requester != "" && approverIdentity != "" {
		if requesterMatchesApprover(rec.Requester, approverIdentity) ||
			identitiesOverlap(rec.Requester, approverIdentity) {
			writeJSONError(w, http.StatusForbidden, "self_approval_denied",
				"self-approval not permitted: the approver cannot be the same principal as the call requester")
			return
		}
	}

	var body mcpApprovalDecisionBody
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&body) // best-effort; body is optional
	}

	resolverID := h.approverPrincipalID(r)
	resolved, err := h.store.Resolve(ctx, id, decision, resolverID, strings.TrimSpace(body.Reason))
	if err != nil {
		var stateConflict *MCPApprovalStateConflictError
		if errors.As(err, &stateConflict) || errors.Is(err, ErrMCPApprovalAlreadyResolved) {
			status := model.ApprovalStatus("")
			if stateConflict != nil {
				status = stateConflict.Status
			}
			slog.Warn("mcp approval resolve conflict", "approval_id", id, "status", status, "error", err)
			writeJSONError(w, http.StatusConflict, "approval_already_resolved", "approval already resolved")
			return
		}
		writeMCPApprovalStoreError(w, r, "resolve mcp approval", err, slog.String("approval_id", id))
		return
	}
	writeJSONObject(w, http.StatusOK, resolved)
}

func writeMCPApprovalStoreError(w http.ResponseWriter, r *http.Request, operation string, err error, attrs ...slog.Attr) {
	args := []any{"method", r.Method, "path", r.URL.Path, "error", err}
	for _, attr := range attrs {
		args = append(args, attr.Key, attr.Value.Any())
	}
	slog.Warn(operation+" failed", args...)
	writeJSONError(w, http.StatusInternalServerError, "mcp_approval_store_error", "mcp approval store unavailable")
}

// requesterMatchesApprover covers legacy MCP requester values recorded as a
// plain principal ID. Composite requester values are handled by
// identitiesOverlap.
func requesterMatchesApprover(requester, approverIdentity string) bool {
	if requester == "" || approverIdentity == "" {
		return false
	}
	// The approver's identity is composite: e.g. "apikey:abcd|principal:alice".
	// Legacy Requester is a plain principal ID. Compare the principal segment.
	for _, part := range strings.Split(approverIdentity, "|") {
		if strings.HasPrefix(part, "principal:") {
			if strings.TrimPrefix(part, "principal:") == requester {
				return true
			}
		}
	}
	return false
}

func writeJSONObject(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// requireMCPApprovalHandler returns the active handler or writes a 503.
// All HTTP shims share this gate so the routes respond consistently
// when MCP is disabled. Admin role is enforced on write paths only —
// callers with a tenant scope may still list/view their own tenant's
// approvals.
func (s *server) requireMCPApprovalHandler(w http.ResponseWriter, r *http.Request, adminOnly bool) *mcpApprovalHandler {
	if adminOnly {
		if !s.requirePermissionOrRole(w, r, auth.PermJobsApprove, "admin") {
			return nil
		}
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.approvalHandler == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "mcp_approvals_unavailable",
			"mcp approval engine is not running — check CORDUM_ENV/redis wiring")
		return nil
	}
	return runtime.approvalHandler
}

func (s *server) handleMCPApprovalList(w http.ResponseWriter, r *http.Request) {
	h := s.requireMCPApprovalHandler(w, r, false)
	if h == nil {
		return
	}
	h.List(w, r)
}

func (s *server) handleMCPApprovalGet(w http.ResponseWriter, r *http.Request) {
	h := s.requireMCPApprovalHandler(w, r, false)
	if h == nil {
		return
	}
	h.Get(w, r, r.PathValue("id"))
}

func (s *server) handleMCPApprovalApprove(w http.ResponseWriter, r *http.Request) {
	h := s.requireMCPApprovalHandler(w, r, true)
	if h == nil {
		return
	}
	h.Approve(w, r, r.PathValue("id"))
}

func (s *server) handleMCPApprovalReject(w http.ResponseWriter, r *http.Request) {
	h := s.requireMCPApprovalHandler(w, r, true)
	if h == nil {
		return
	}
	h.Reject(w, r, r.PathValue("id"))
}
