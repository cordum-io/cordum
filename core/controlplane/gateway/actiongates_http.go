package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/cordum/cordum/core/policy/actiongates"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// actionGateHTTPStatus maps an ActionGateDecision.Code to its HTTP status
// code at the gateway boundary. The mapping is stable API surface for
// SDKs and SIEM rules; callers MUST NOT extend it without an explicit
// architecture decision.
//
// REQUIRE_HUMAN is mapped to 200 because the policy simulate endpoint is
// informational — REQUIRE_HUMAN signals "this would have needed approval"
// rather than blocking. The Edge evaluate endpoint maps the same code
// onto its own inline-approval workflow, not an HTTP error.
func actionGateHTTPStatus(code string) int {
	switch code {
	case actiongates.CodeUnauthorized:
		return http.StatusUnauthorized
	case actiongates.CodeAccessDenied:
		return http.StatusForbidden
	case actiongates.CodeNotFound:
		return http.StatusNotFound
	case actiongates.CodeConflict:
		return http.StatusConflict
	case actiongates.CodeServiceUnavailable, actiongates.CodeResolverError:
		return http.StatusServiceUnavailable
	case actiongates.CodeInternalError:
		return http.StatusInternalServerError
	case actiongates.CodeRequireHuman:
		return http.StatusOK
	default:
		return http.StatusInternalServerError
	}
}

// actionGateEdgeErrCode maps the gate Code to the Edge envelope `code`
// string. Falls back to internal_error when the gate code is unknown so
// the wire-shape stays consistent.
func actionGateEdgeErrCode(code string) string {
	switch code {
	case actiongates.CodeUnauthorized:
		return edgeErrCodeUnauthorized
	case actiongates.CodeAccessDenied:
		return edgeErrCodeAccessDenied
	case actiongates.CodeNotFound:
		return edgeErrCodeNotFound
	case actiongates.CodeConflict:
		return edgeErrCodeConflict
	case actiongates.CodeServiceUnavailable, actiongates.CodeResolverError:
		return edgeErrCodeServiceUnavailable
	case actiongates.CodeInternalError:
		return edgeErrCodeInternalError
	default:
		return edgeErrCodeInternalError
	}
}

// safeActionGateExtraKeys allowlists the Extra-map fields that may be
// echoed back to the client. The list is intentionally narrow:
//   - "gate" / "sub_reason" / "target_type": stable, sanitized identifiers.
//   - "claim_present" / "claim_chars": coarse signals on whether a claim
//     was offered, never the raw text.
//   - "auth_tenant": present-tenant identifier; never the cross-tenant
//     identifier (which would leak existence of another tenant).
//
// Raw target_path, target_url, args contents, claim text, and resolved
// approval_ref are NEVER included — they are PII / abuse vectors.
var safeActionGateExtraKeys = map[string]struct{}{
	"gate":          {},
	"sub_reason":    {},
	"target_type":   {},
	"auth_tenant":   {},
	"claim_present": {},
	"claim_chars":   {},
	"verb":          {},
	"kind":          {},
}

// sanitizeActionGateDetails returns the subset of dec.Extra that is safe
// to echo back to the client per safeActionGateExtraKeys. Returns nil
// when no safe fields remain so writeEdgeError does not emit an empty
// `details` object.
func sanitizeActionGateDetails(dec actiongates.ActionGateDecision) map[string]any {
	if len(dec.Extra) == 0 {
		return nil
	}
	out := make(map[string]any, len(dec.Extra))
	for k, v := range dec.Extra {
		if _, ok := safeActionGateExtraKeys[k]; ok {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeActionGatePolicyError emits the gateway response for a fired
// action-gate decision. For REQUIRE_HUMAN at the policy/simulate
// endpoint, it returns a 200 informational JSON shape (NOT an edge
// error envelope) — simulate is non-blocking; the require-human signal
// is informational. All other Codes route through writeEdgeError so the
// envelope wire-shape stays consistent.
func writeActionGatePolicyError(w http.ResponseWriter, r *http.Request, mode string, dec actiongates.ActionGateDecision) {
	if dec.Code == actiongates.CodeRequireHuman && mode == "simulate" {
		body := map[string]any{
			"decision":   pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN.String(),
			"reason":     dec.Reason,
			"rule_id":    dec.GateID,
			"gate":       dec.GateID,
			"sub_reason": dec.SubReason,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	if dec.Code == actiongates.CodeRequireHuman {
		// Non-simulate flows treat REQUIRE_HUMAN as a blocking policy
		// outcome: clients must defer the action to an approver. Map to
		// 409 Conflict so the wire shape carries a clear non-success
		// envelope instead of the default 200+internal_error fall-through.
		writeEdgeError(w, r, http.StatusConflict, edgeErrCodeConflict, dec.Reason, sanitizeActionGateDetails(dec))
		return
	}
	status := actionGateHTTPStatus(dec.Code)
	code := actionGateEdgeErrCode(dec.Code)
	details := sanitizeActionGateDetails(dec)
	writeEdgeError(w, r, status, code, dec.Reason, details)
}
