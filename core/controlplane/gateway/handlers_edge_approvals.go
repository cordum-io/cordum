package gateway

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

type edgeApprovalPageResponse struct {
	Items      []edgecore.EdgeApproval `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

type edgeApprovalDecisionRequest struct {
	Reason string `json:"reason"`
}

// edgeApprovalWaitRequest is the optional body for POST .../wait. Callers may
// specify a wait budget; the gateway always clamps via boundEdgeEvaluateWaitTimeout
// so an unbounded request cannot hang the handler.
type edgeApprovalWaitRequest struct {
	TimeoutMS int `json:"timeout_ms"`
}

func (s *server) handleListEdgeApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	query, err := edgeApprovalListQueryFromRequest(r, tenantID)
	if err != nil {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge approval query", nil)
		return
	}
	page, err := store.ListApprovals(r.Context(), query)
	if err != nil {
		writeEdgeApprovalStoreError(w, r, err, "list edge approvals")
		return
	}
	writeJSON(w, edgeApprovalPageResponse{Items: page.Items, NextCursor: page.NextCursor})
}

func (s *server) handleGetEdgeApproval(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user", "viewer") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	approvalRef, ok := requireEdgePathParam(w, r, "approval_ref")
	if !ok {
		return
	}
	approval, found, err := store.GetApproval(r.Context(), tenantID, approvalRef)
	if err != nil {
		writeEdgeApprovalStoreError(w, r, err, "get edge approval")
		return
	}
	if !found || approval == nil || !s.edgeApprovalVisibleToCaller(r, approval) {
		writeEdgeApprovalNotFound(w, r)
		return
	}
	writeJSON(w, approval)
}

func (s *server) handleApproveEdgeApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveEdgeApproval(w, r, edgecore.ApprovalDecisionApprove)
}

func (s *server) handleRejectEdgeApproval(w http.ResponseWriter, r *http.Request) {
	s.handleResolveEdgeApproval(w, r, edgecore.ApprovalDecisionReject)
}

func (s *server) handleResolveEdgeApproval(w http.ResponseWriter, r *http.Request, decision edgecore.ApprovalDecision) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsApprove, "admin") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	approvalRef, ok := requireEdgePathParam(w, r, "approval_ref")
	if !ok {
		return
	}
	existing, found, err := store.GetApproval(r.Context(), tenantID, approvalRef)
	if err != nil {
		writeEdgeApprovalStoreError(w, r, err, "get edge approval for resolution")
		return
	}
	if !found || existing == nil {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge approval not found", nil)
		return
	}
	if edgeApprovalRequesterMatchesResolver(existing, r) {
		writeEdgeError(w, r, http.StatusForbidden, "self_approval_denied", "self-approval not permitted: the resolver cannot be the same principal as the approval requester", nil)
		return
	}
	var body edgeApprovalDecisionRequest
	if r.Body != nil && r.Body != http.NoBody {
		if err := decodeJSONBody(w, r, &body); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid edge approval decision request")
			return
		}
	}

	resolution := edgecore.ApprovalResolution{
		TenantID:    tenantID,
		ApprovalRef: strings.TrimSpace(approvalRef),
		ResolverID:  edgeApprovalResolverID(r),
		ResolvedBy:  edgeApprovalResolvedBy(r),
		Reason:      strings.TrimSpace(body.Reason),
		ResolvedAt:  time.Now().UTC(),
	}
	var approval *edgecore.EdgeApproval
	if decision == edgecore.ApprovalDecisionApprove {
		approval, err = store.ApproveApproval(r.Context(), resolution)
	} else {
		approval, err = store.RejectApproval(r.Context(), resolution)
	}
	if err != nil {
		writeEdgeApprovalStoreError(w, r, err, "resolve edge approval")
		return
	}
	// EDGE-014 step-10: emit best-effort approval-resolved audit event.
	// Severity follows decision: approved -> info, rejected -> high
	// (handled by SIEMEventForApprovalResolved).
	if approval != nil {
		outcome := "approved"
		if decision != edgecore.ApprovalDecisionApprove {
			outcome = "rejected"
		}
		resolvedAt := resolution.ResolvedAt
		if approval.ResolvedAt != nil && !approval.ResolvedAt.IsZero() {
			resolvedAt = *approval.ResolvedAt
		}
		edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForApprovalResolved(
			tenantID,
			approval.ApprovalRef,
			approval.RuleID,
			outcome,
			resolution.ResolverID,
			resolvedAt,
			nil,
		))
	}
	writeJSON(w, approval)
}

func edgeApprovalListQueryFromRequest(r *http.Request, tenantID string) (edgecore.ListApprovalsQuery, error) {
	query := edgecore.ListApprovalsQuery{
		TenantID:    tenantID,
		Status:      edgecore.ApprovalStatus(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))),
		SessionID:   strings.TrimSpace(r.URL.Query().Get("session_id")),
		ExecutionID: strings.TrimSpace(r.URL.Query().Get("execution_id")),
		ActionHash:  strings.TrimSpace(r.URL.Query().Get("action_hash")),
		Cursor:      strings.TrimSpace(r.URL.Query().Get("cursor")),
		Limit:       edgeQueryLimit(r),
	}
	if query.Status != "" {
		switch query.Status {
		case edgecore.ApprovalStatusPending, edgecore.ApprovalStatusApproved, edgecore.ApprovalStatusRejected, edgecore.ApprovalStatusExpired, edgecore.ApprovalStatusInvalidated:
		default:
			return edgecore.ListApprovalsQuery{}, errors.New("invalid status")
		}
	}
	if (query.SessionID != "" || query.ExecutionID != "" || query.ActionHash != "") &&
		(query.SessionID == "" || query.ExecutionID == "" || query.ActionHash == "") {
		return edgecore.ListApprovalsQuery{}, errors.New("session_id, execution_id, and action_hash are required together")
	}
	return query, nil
}

// handleWaitEdgeApproval is the agentd/demo-only blocking-wait endpoint.
// It bounds-waits for an approval to leave Pending and returns the resolved
// EdgeApproval, or the still-pending record if the timeout elapsed first.
//
// Tenant isolation is enforced by GetApproval being tenant-scoped (cross-tenant
// returns 404 with no metadata leakage). The timeout is clamped server-side via
// boundEdgeEvaluateWaitTimeout — same helper as the inline-wait evaluate path —
// so behavior is consistent and the handler cannot block indefinitely. Browser
// or dashboard approval UX must never be required to call this; it is for
// agentd/local-dev clients that prefer a single blocking RPC over poll-and-retry.
func (s *server) handleWaitEdgeApproval(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermJobsRead, "admin", "user") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	approvalRef, ok := requireEdgePathParam(w, r, "approval_ref")
	if !ok {
		return
	}

	approval, found, err := store.GetApproval(r.Context(), tenantID, approvalRef)
	if err != nil {
		writeEdgeApprovalStoreError(w, r, err, "get edge approval for wait")
		return
	}
	if !found || approval == nil || !s.edgeApprovalVisibleToCaller(r, approval) {
		writeEdgeApprovalNotFound(w, r)
		return
	}

	var body edgeApprovalWaitRequest
	if r.Body != nil && r.Body != http.NoBody {
		if err := decodeJSONBody(w, r, &body); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid edge approval wait request")
			return
		}
	}

	if approval.Status != edgecore.ApprovalStatusPending {
		writeJSON(w, approval)
		return
	}

	s.waitForEdgeApprovalResolution(r.Context(), store, tenantID, approvalRef, boundEdgeEvaluateWaitTimeout(body.TimeoutMS))

	final, found, err := store.GetApproval(r.Context(), tenantID, approvalRef)
	if err != nil {
		writeEdgeApprovalStoreError(w, r, err, "get edge approval after wait")
		return
	}
	if !found || final == nil || !s.edgeApprovalVisibleToCaller(r, final) {
		writeEdgeApprovalNotFound(w, r)
		return
	}
	writeJSON(w, final)
}

func (s *server) edgeApprovalVisibleToCaller(r *http.Request, approval *edgecore.EdgeApproval) bool {
	if approval == nil {
		return false
	}
	if authCtx := auth.FromRequest(r); authCtx != nil {
		if principal := strings.TrimSpace(authCtx.PrincipalID); principal != "" && principal == strings.TrimSpace(approval.PrincipalID) {
			return true
		}
	}
	return s.requireRole(r, "admin", "operator") == nil
}

func writeEdgeApprovalNotFound(w http.ResponseWriter, r *http.Request) {
	writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge approval not found", nil)
}

func writeEdgeApprovalStoreError(w http.ResponseWriter, r *http.Request, err error, operation string) {
	switch {
	case errors.Is(err, edgecore.ErrNotFound):
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "edge approval not found", nil)
	case errors.Is(err, edgecore.ErrApprovalConflict):
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeApprovalConflict, "edge approval conflict", nil)
	case isEdgeValidationError(err):
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "invalid edge approval request", nil)
	default:
		writeEdgeInternalError(w, r, operation, err)
	}
}

func edgeApprovalResolverID(r *http.Request) string {
	if authCtx := auth.FromRequest(r); authCtx != nil {
		if strings.TrimSpace(authCtx.PrincipalID) != "" {
			return strings.TrimSpace(authCtx.PrincipalID)
		}
	}
	if identity := submitterIdentity(r); strings.TrimSpace(identity) != "" {
		return strings.TrimSpace(identity)
	}
	return "unknown"
}

func edgeApprovalResolvedBy(r *http.Request) string {
	if authCtx := auth.FromRequest(r); authCtx != nil {
		parts := make([]string, 0, 2)
		if strings.TrimSpace(authCtx.PrincipalID) != "" {
			parts = append(parts, "principal:"+strings.TrimSpace(authCtx.PrincipalID))
		}
		if strings.TrimSpace(authCtx.Role) != "" {
			parts = append(parts, "role:"+auth.NormalizeRole(authCtx.Role))
		}
		if len(parts) > 0 {
			return strings.Join(parts, "|")
		}
	}
	if identity := submitterIdentity(r); strings.TrimSpace(identity) != "" {
		return strings.TrimSpace(identity)
	}
	return "unknown"
}

func edgeApprovalRequesterMatchesResolver(approval *edgecore.EdgeApproval, r *http.Request) bool {
	if approval == nil {
		return false
	}
	resolverIdentity := submitterIdentity(r)
	if strings.TrimSpace(resolverIdentity) == "" {
		return false
	}
	for _, requester := range []string{approval.Requester, approval.PrincipalID} {
		requester = strings.TrimSpace(requester)
		if requester == "" {
			continue
		}
		if requesterMatchesApprover(requester, resolverIdentity) {
			return true
		}
		if identitiesOverlap(requester, resolverIdentity) {
			return true
		}
		if identitiesOverlap("principal:"+requester, resolverIdentity) {
			return true
		}
	}
	return false
}
