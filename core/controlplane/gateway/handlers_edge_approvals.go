package gateway

import (
	"encoding/json"
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
	if !s.edgeApprovalCallerCanListAll(r) {
		principalID := edgeApprovalAuthenticatedPrincipal(r)
		if principalID == "" {
			writeJSON(w, edgeApprovalPageResponse{Items: []edgecore.EdgeApproval{}, NextCursor: ""})
			return
		}
		query.PrincipalID = principalID
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
	if approval.Status == edgecore.ApprovalStatusPending && approval.ExpiresAt != nil {
		now := time.Now().UTC()
		if !approval.ExpiresAt.After(now) {
			if _, err := store.ExpireApprovals(r.Context(), tenantID, now); err != nil {
				writeEdgeApprovalStoreError(w, r, err, "expire edge approvals before get")
				return
			}
			approval, found, err = store.GetApproval(r.Context(), tenantID, approvalRef)
			if err != nil {
				writeEdgeApprovalStoreError(w, r, err, "get expired edge approval")
				return
			}
			if !found || approval == nil || !s.edgeApprovalVisibleToCaller(r, approval) {
				writeEdgeApprovalNotFound(w, r)
				return
			}
		}
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
		s.auditEdgeApprovalSelfApprovalDenied(tenantID, *existing, "caller_is_requester")
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

	resolverID := edgeApprovalResolverID(r)
	resolvedBy := edgeApprovalResolvedBy(r)
	reason := strings.TrimSpace(body.Reason)

	// EDGE-060 step 4 — idempotency hash payload AFTER tenant +
	// resolver-principal override per EDGE-008.7 invariant. ResolvedAt
	// is server-generated and intentionally OMITTED from the hash so a
	// retry hashes identically to its original. The (decision +
	// approvalRef + reason) tuple anchors the action; the resolver id
	// scopes it to a specific operator.
	endpoint := edgeApprovalApproveEndpoint
	if decision != edgecore.ApprovalDecisionApprove {
		endpoint = edgeApprovalRejectEndpoint
	}
	normalizedReq := struct {
		TenantID    string                    `json:"tenant_id"`
		ApprovalRef string                    `json:"approval_ref"`
		Decision    edgecore.ApprovalDecision `json:"decision"`
		ResolverID  string                    `json:"resolver_id"`
		ResolvedBy  string                    `json:"resolved_by"`
		Reason      string                    `json:"reason"`
	}{
		TenantID:    tenantID,
		ApprovalRef: strings.TrimSpace(approvalRef),
		Decision:    decision,
		ResolverID:  resolverID,
		ResolvedBy:  resolvedBy,
		Reason:      reason,
	}
	idempotencyReq, idempotent, handled := s.prepareEdgeIdempotencyRequest(w, r, tenantID, endpoint, normalizedReq)
	if handled {
		return
	}

	resolveFn := func() (edgeIdempotentWriteResult, error) {
		return s.executeResolveEdgeApproval(r, store, decision, tenantID, approvalRef, resolverID, resolvedBy, reason)
	}
	errFn := func(err error) {
		writeEdgeApprovalStoreError(w, r, err, "resolve edge approval")
	}
	if idempotent {
		s.applyEdgeIdempotency(w, r, store, idempotencyReq, resolveFn, errFn)
		return
	}
	result, err := resolveFn()
	if err != nil {
		errFn(err)
		return
	}
	w.Header().Set("Content-Type", result.ContentType)
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
}

// executeResolveEdgeApproval is the body of handleResolveEdgeApproval
// factored out for idempotency-wrapping reuse. Returns the marshalled
// approval response on success and the underlying store error on failure
// (mapped to the wire envelope by writeEdgeApprovalStoreError so the
// idempotent + non-idempotent paths emit identical responses).
//
// DoD #7: a same-key + same-body retry of an already-succeeded resolution
// returns the cached 200 response (replay) — NOT a 409 "already approved".
// The applyEdgeIdempotency wrapper handles replay automatically; this
// function is only invoked on the fresh path. A different idempotency key
// hitting an already-terminal approval still surfaces store-level
// "already approved" as a 409 via writeEdgeApprovalStoreError.
func (s *server) executeResolveEdgeApproval(r *http.Request, store edgecore.Store, decision edgecore.ApprovalDecision, tenantID, approvalRef, resolverID, resolvedBy, reason string) (edgeIdempotentWriteResult, error) {
	resolution := edgecore.ApprovalResolution{
		TenantID:    tenantID,
		ApprovalRef: strings.TrimSpace(approvalRef),
		ResolverID:  resolverID,
		ResolvedBy:  resolvedBy,
		Reason:      reason,
		ResolvedAt:  time.Now().UTC(),
	}
	var approval *edgecore.EdgeApproval
	var err error
	if decision == edgecore.ApprovalDecisionApprove {
		approval, err = store.ApproveApproval(r.Context(), resolution)
	} else {
		approval, err = store.RejectApproval(r.Context(), resolution)
	}
	if err != nil {
		return edgeIdempotentWriteResult{}, err
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
			resolverID,
			resolvedAt,
			edgeApprovalEvidenceAuditExtra(approval),
		))
	}
	body, err := json.Marshal(approval)
	if err != nil {
		return edgeIdempotentWriteResult{}, err
	}
	return edgeIdempotentWriteResult{
		StatusCode:  http.StatusOK,
		ContentType: "application/json",
		Body:        body,
	}, nil
}

func (s *server) auditEdgeApprovalSelfApprovalDenied(tenantID string, approval edgecore.EdgeApproval, reasonCode string) {
	extra := edgeApprovalEvidenceAuditExtra(&approval)
	if extra == nil {
		extra = map[string]string{}
	}
	extra["conflict_kind"] = string(edgecore.ApprovalConflictKindSelfApproval)
	extra["reason_code"] = reasonCode
	edgecore.SendSIEMEvent(s.auditExporter, edgecore.SIEMEventForApprovalResolved(
		tenantID,
		approval.ApprovalRef,
		approval.RuleID,
		"rejected",
		"",
		time.Now().UTC(),
		extra,
	))
}

func edgeApprovalEvidenceAuditExtra(approval *edgecore.EdgeApproval) map[string]string {
	if approval == nil {
		return nil
	}
	extra := map[string]string{}
	if v := strings.TrimSpace(approval.ActionHash); v != "" {
		extra["action_hash"] = v
	}
	if v := strings.TrimSpace(approval.InputHash); v != "" {
		extra["input_hash"] = v
	}
	if v := strings.TrimSpace(approval.PolicySnapshot); v != "" {
		extra["policy_snapshot"] = v
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
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
	// action_hash requires all three tuple fields (session_id + execution_id + action_hash).
	// session_id alone (without execution_id or action_hash) is valid — used by the
	// EdgeApprovalsDrawer to list all approvals for a session.
	if query.ActionHash != "" && (query.SessionID == "" || query.ExecutionID == "") {
		return edgecore.ListApprovalsQuery{}, errors.New("action_hash requires session_id and execution_id")
	}
	if query.ExecutionID != "" && query.SessionID == "" {
		return edgecore.ListApprovalsQuery{}, errors.New("execution_id requires session_id")
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
	if principal := edgeApprovalAuthenticatedPrincipal(r); principal != "" && principal == strings.TrimSpace(approval.PrincipalID) {
		return true
	}
	return s.edgeApprovalCallerCanListAll(r)
}

func (s *server) edgeApprovalCallerCanListAll(r *http.Request) bool {
	return s.requireRole(r, "admin", "operator") == nil
}

func edgeApprovalAuthenticatedPrincipal(r *http.Request) string {
	if authCtx := auth.FromRequest(r); authCtx != nil {
		return strings.TrimSpace(authCtx.PrincipalID)
	}
	return ""
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
	case errors.Is(err, edgecore.ErrEventListTooLarge):
		// EDGE-058 — fail-closed safety guard: parent execution's event list
		// exceeds the inline-validation cap. 422 distinguishes lifecycle-state
		// rejection from request-shape (400) or auth (401/403) failures so
		// callers can present a meaningful UX (e.g. "this session has too
		// many events; archive and start a new session").
		writeEdgeError(w, r, http.StatusUnprocessableEntity, edgeErrCodeEventListTooLarge, "edge execution event list exceeds approval validation cap", nil)
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
