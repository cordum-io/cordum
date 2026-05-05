package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	policyconfig "github.com/cordum/cordum/core/infra/config"
	wf "github.com/cordum/cordum/core/workflow"
)

const workflowPolicyBundleLimit = 10000

func validateWorkflowPolicyOverride(workflowID, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	sanitized := policybundles.SanitizePolicyBundleYAML(raw)
	policy, err := policyconfig.ParseSafetyPolicy([]byte(sanitized))
	if err != nil {
		return "", fmt.Errorf("invalid policy_override: %w", err)
	}
	if policy == nil {
		return "", nil
	}
	if policyconfig.NormalizePolicyTier(policy.Tier) != policyconfig.PolicyTierWorkflow {
		return "", fmt.Errorf("policy_override tier must be workflow")
	}
	selector := policyconfig.TrimPolicySelector(policy.Selector)
	if selector.WorkflowID != strings.TrimSpace(workflowID) {
		return "", fmt.Errorf("policy_override selector.workflow_id must match workflow id %q", workflowID)
	}
	if _, _, err := policybundles.BuildPolicyFromBundles(map[string]any{
		workflowPolicyBundleKey(workflowID): map[string]any{"content": sanitized},
	}); err != nil {
		return "", fmt.Errorf("invalid policy_override: %w", err)
	}
	return raw, nil
}

func (s *server) addWorkflowPolicyOverrideBundles(ctx context.Context, bundles map[string]any) error {
	if s == nil || s.workflowStore == nil {
		return nil
	}
	workflows, err := s.workflowStore.ListWorkflows(ctx, "", workflowPolicyBundleLimit)
	if err != nil {
		return fmt.Errorf("load workflow policy overrides: %w", err)
	}
	for _, wfDef := range workflows {
		key, bundle, ok := workflowPolicyOverrideBundle(wfDef)
		if ok {
			bundles[key] = bundle
		}
	}
	return nil
}

func workflowPolicyOverrideBundle(wfDef *wf.Workflow) (string, map[string]any, bool) {
	if wfDef == nil || strings.TrimSpace(wfDef.ID) == "" || strings.TrimSpace(wfDef.PolicyOverride) == "" {
		return "", nil, false
	}
	bundle := map[string]any{
		"content":     wfDef.PolicyOverride,
		"enabled":     true,
		"source":      "workflow",
		"workflow_id": wfDef.ID,
	}
	if !wfDef.UpdatedAt.IsZero() {
		bundle["updated_at"] = wfDef.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return workflowPolicyBundleKey(wfDef.ID), bundle, true
}

func workflowPolicyBundleKey(workflowID string) string {
	return "workflow/" + strings.TrimSpace(workflowID) + "/policy"
}
