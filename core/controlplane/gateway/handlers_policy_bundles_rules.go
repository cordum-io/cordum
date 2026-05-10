package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
)

// addRuleToBundleRequest is the wire shape for
// POST /api/v1/policy/bundles/{id}/rules. The path supplies the bundle
// id; the body supplies the rule id. Idempotent on repeated calls.
type addRuleToBundleRequest struct {
	RuleID string `json:"rule_id"`
}

// handleAddRuleToBundle implements POST /api/v1/policy/bundles/{id}/rules.
// Looks up the rule via the injected ruleExists callback (which calls
// RuleStore.GetRule) and adds it to the bundle's RuleIDs[] set.
// Returns 200 + updated Bundle on success; 404 with `{"error":
// "rule_not_found"}` or `{"error": "bundle_not_found"}` to disambiguate
// the two missing-resource cases for the dashboard.
func (s *server) handleAddRuleToBundle(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicyBundleStore(w, r, auth.PermPolicyWrite) {
		return
	}
	if s.policyRuleStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "rule store unavailable")
		return
	}
	bundleID := strings.TrimSpace(r.PathValue("id"))
	if bundleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "bundle id required")
		return
	}
	var body addRuleToBundleRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	ruleID := strings.TrimSpace(body.RuleID)
	if ruleID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "rule_id required")
		return
	}
	updated, err := s.policyBundleStore.AddRuleToBundle(
		r.Context(), bundleID, ruleID,
		s.ruleExistsForBinding,
	)
	if err != nil {
		writeAddRuleToBundleError(w, r, err)
		return
	}
	writeJSON(w, updated)
}

// ruleExistsForBinding is the callback BundleStore.AddRuleToBundle
// uses to verify a rule exists before binding. We pass this as a
// closure rather than calling RuleStore directly inside BundleStore so
// the BundleStore stays decoupled from RuleStore. errors propagate;
// missing rules return false+nil.
func (s *server) ruleExistsForBinding(ctx context.Context, ruleID string) (bool, error) {
	if s.policyRuleStore == nil {
		return false, errors.New("rule store unavailable")
	}
	if _, err := s.policyRuleStore.GetRule(ctx, ruleID); err != nil {
		if errors.Is(err, policy.ErrRuleNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// writeAddRuleToBundleError disambiguates the two 404 cases (bundle vs
// rule) so the dashboard can present the right error copy without
// guessing. All other errors fall through to the bundle-store mapper.
func writeAddRuleToBundleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrBundleNotFound):
		writeJSONStatus(w, http.StatusNotFound, map[string]any{
			"error": "bundle_not_found",
		})
	case errors.Is(err, policy.ErrRuleNotFound):
		writeJSONStatus(w, http.StatusNotFound, map[string]any{
			"error": "rule_not_found",
		})
	default:
		writePolicyBundleStoreError(w, r, err)
	}
}
