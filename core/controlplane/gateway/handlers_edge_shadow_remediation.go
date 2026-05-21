// EDGE-142 — Shadow remediation generator handler.
//
// HTTP surface mounted under /api/v1/edge/shadow-agents/:
//
//	POST /api/v1/edge/shadow-agents/{finding_id}/remediation
//
// The handler is read-only: it loads the finding via the EDGE-141
// store (tenant-scoped), generates an advisory plan via the pure
// shadow.GenerateForFinding generator, and returns the plan. No state
// transition; no audit event (the finding state remains untouched);
// no Cordum Job creation; no Safety Kernel interaction.
//
// Body shape is optional. Empty body yields the generator's default
// audience (both) and full commands. Provided body fields:
//
//	{
//	  "audience": "dev|enterprise|both",
//	  "omit_commands": true|false
//	}
//
// Auth: `PermAuditRead` or `admin` role — same gate as
// handleGetShadowAgentFinding because the response carries no extra
// privilege over the finding itself.
package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge/shadow"
)

// shadowRemediationRequest is the optional body shape. Both fields
// default to their generator-defined zero values when absent.
type shadowRemediationRequest struct {
	Audience     string `json:"audience,omitempty"`
	OmitCommands bool   `json:"omit_commands,omitempty"`
}

// shadowRemediationResponse mirrors the response envelope. We wrap
// the plan with finding_id + tenant_id at the top level so dashboard
// clients can attach the plan to the finding without re-parsing.
type shadowRemediationResponse struct {
	FindingID   string                  `json:"finding_id"`
	TenantID    string                  `json:"tenant_id"`
	Remediation *shadow.RemediationPlan `json:"remediation"`
}

// handleGenerateShadowAgentRemediation responds with an advisory
// RemediationPlan for the requested finding. Side-effect free.
func (s *server) handleGenerateShadowAgentRemediation(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}
	store := s.shadowFindingStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	findingID, ok := requireEdgePathParam(w, r, "finding_id")
	if !ok {
		return
	}

	// Body is optional. Empty body parses to the zero-value struct.
	var body shadowRemediationRequest
	if r.ContentLength > 0 {
		if err := decodeJSONBody(w, r, &body); err != nil {
			writeEdgeJSONDecodeError(w, r, err, "invalid shadow remediation request")
			return
		}
	}

	audience, ok := parseRemediationAudience(w, r, body.Audience)
	if !ok {
		return
	}

	finding, err := store.GetFinding(r.Context(), tenantID, findingID)
	if err != nil {
		writeShadowFindingStoreError(w, r, err, "get shadow finding for remediation")
		return
	}

	plan, err := shadow.GenerateForFinding(finding, shadow.GeneratorOptions{
		Audience:     audience,
		OmitCommands: body.OmitCommands,
	})
	if err != nil {
		if errors.Is(err, shadow.ErrRemediationValidation) {
			// Defence in depth: the store-loaded finding is well-shaped,
			// so this path is unreachable today. Map to 400 instead of
			// 500 so a future generator extension cannot leak internals
			// via a panicky 500 envelope.
			writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "remediation could not be generated", nil)
			return
		}
		writeEdgeInternalError(w, r, "generate shadow remediation", err)
		return
	}

	writeJSON(w, shadowRemediationResponse{
		FindingID:   finding.FindingID,
		TenantID:    finding.TenantID,
		Remediation: plan,
	})
}

// parseRemediationAudience normalises the optional audience param and
// rejects unknown values with a 400 envelope. Empty string passes
// through (generator applies its own default of "both").
func parseRemediationAudience(w http.ResponseWriter, r *http.Request, raw string) (shadow.RemediationAudience, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", true
	}
	switch shadow.RemediationAudience(v) {
	case shadow.RemediationAudienceDev, shadow.RemediationAudienceEnterprise, shadow.RemediationAudienceBoth:
		return shadow.RemediationAudience(v), true
	default:
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, "audience must be one of dev|enterprise|both", nil)
		return "", false
	}
}
