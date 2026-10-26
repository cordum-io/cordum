package gateway

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// handleAdoptRetentionAnchor implements
// POST /api/v1/audit/chain/retention-anchor: an admin adopts a retention
// anchor for a tenant chain trimmed outside the retention sweeper, so verify
// reads the missing prefix as retention_trimmed. See
// audit.Chainer.AdoptRetentionAnchor; every refusal is a 409.
func (s *server) handleAdoptRetentionAnchor(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAdminAll, "admin") {
		return
	}
	if s.auditChainer == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "audit chainer not installed")
		return
	}
	var req struct {
		TenantID          string `json:"tenant_id"`
		ExpectedOldestSeq int64  `json:"expected_oldest_seq"`
		ReplaceInvalid    bool   `json:"replace_invalid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ExpectedOldestSeq < 2 {
		writeErrorJSON(w, http.StatusBadRequest, "expected_oldest_seq required: the seq of the oldest retained event (verify first_seq), at least 2")
		return
	}
	tenant, err := s.resolveTenant(r, req.TenantID)
	if err != nil || s.requireTenantAccess(r, tenant) != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	actor := "admin"
	if authCtx := auth.FromRequest(r); authCtx != nil && authCtx.PrincipalID != "" {
		actor = authCtx.PrincipalID
	}

	res, err := s.auditChainer.AdoptRetentionAnchor(r.Context(), tenant, audit.AdoptAnchorRequest{
		ExpectedOldestSeq: req.ExpectedOldestSeq,
		ReplaceInvalid:    req.ReplaceInvalid,
		Actor:             actor,
	})
	switch {
	case errors.Is(err, audit.ErrAdoptRefused):
		writeErrorJSON(w, http.StatusConflict, err.Error())
	case err != nil:
		writeInternalError(w, r, "audit adopt retention anchor", err)
	default:
		writeJSON(w, res)
	}
}
