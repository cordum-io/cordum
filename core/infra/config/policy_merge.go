package config

import (
	"log/slog"
	"strings"
	"time"
)

// This file is the SINGLE source of truth for policy-bundle merge + clone
// semantics shared by BOTH the safety kernel (safetykernel.policyLoader.
// loadFragments / mergePolicies) and the API gateway bundle compiler
// (policybundles.BuildPolicyFromBundles / MergeSafetyPolicies). Keep them
// unified here so the two never silently re-diverge.
//
// History:
//   - task-0ba2bc0d aligned the gateway merge to the kernel for fragment
//     ORDERING (installed_at ASC + bundle-id alpha tiebreak) and rule-ID DEDUP
//     (Rules/OutputRules/InputRules last-seen wins). The two were proven to
//     produce identical decisions for that matrix (policybundles/
//     bundles_recency_test.go) but remained two copies of the logic.
//   - task-48567e75 (this extraction) collapsed both into MergePolicies here.
//     Both callers delegate; their package-local funcs are thin shims.
//
// OutputPolicy cross-fragment semantics (task-198acafd): a guard enabled by ANY
// fragment stays enabled (OR-merge of OutputPolicy.Enabled) and FailMode fills
// first-non-empty with base precedence. This is the SINGLE canonical behavior
// for BOTH callers — the kernel loader adopted the gateway's any-enable rule so
// replay/evals predict what the kernel enforces. It is the fail-safe direction
// (no bundle can silently disable output scanning another bundle turned on) and
// matches the invariants overlay on both sides (kernel applyKernelInvariants /
// gateway ApplyInvariants). Earlier this diverged behind a PolicyMergeOptions.
// MergeOutputPolicy flag (kernel=false / gateway=true); task-198acafd collapsed
// it to the gateway's any-enable semantics with governor sign-off. See
// task-0ba2bc0d (aligned rule ORDERING + DEDUP) and task-48567e75 (extraction).

// MergePolicies merges fragment `extra` into base policy `base`, returning a new
// policy (inputs are never mutated). The caller controls fragment precedence via
// merge ORDER: cross-fragment duplicate rule IDs resolve last-seen-wins, so the
// fragment merged LAST wins (callers sort by install recency before folding).
//
// The merged policy is always tier=global with an empty selector; scalar fields
// (Version/DefaultTenant/DefaultDecision) fill first-non-empty with base
// precedence. Rules/OutputRules/InputRules dedup by ID (last-seen wins,
// replace-in-place). TierDefaults append; Tenants merge per MergeTenantPolicies.
// OutputPolicy OR-merges Enabled + fills first-non-empty FailMode (see the file
// header for the canonical cross-fragment semantics).
func MergePolicies(base, extra *SafetyPolicy) *SafetyPolicy {
	if base == nil {
		out := CloneSafetyPolicyWithTierMetadata(extra)
		if out != nil {
			out.Tier = PolicyTierGlobal
			out.Selector = PolicySelector{}
		}
		return out
	}
	if extra == nil {
		out := CloneSafetyPolicyWithTierMetadata(base)
		if out != nil {
			out.Tier = PolicyTierGlobal
			out.Selector = PolicySelector{}
		}
		return out
	}
	out := CloneSafetyPolicyWithTierMetadata(base)
	add := CloneSafetyPolicyWithTierMetadata(extra)
	out.Tier = PolicyTierGlobal
	out.Selector = PolicySelector{}
	if out.Version == "" {
		out.Version = add.Version
	}
	if out.DefaultTenant == "" {
		out.DefaultTenant = add.DefaultTenant
	}
	if strings.TrimSpace(out.DefaultDecision) == "" {
		out.DefaultDecision = strings.TrimSpace(add.DefaultDecision)
	}
	// RequireHuman: a later fragment introducing the deny→require-human
	// threshold must fill an unset base (first-non-empty, base precedence) —
	// otherwise the kernel keeps routing truly-ambiguous matches to hard DENY.
	if out.RequireHuman == (RequireHumanThreshold{}) {
		out.RequireHuman = add.RequireHuman
	}
	// OutputPolicy: any fragment enabling output scanning keeps it enabled
	// (fail-safe); FailMode fills first-non-empty with base precedence.
	if !out.OutputPolicy.Enabled && add.OutputPolicy.Enabled {
		out.OutputPolicy.Enabled = true
	}
	if strings.TrimSpace(out.OutputPolicy.FailMode) == "" {
		out.OutputPolicy.FailMode = strings.TrimSpace(add.OutputPolicy.FailMode)
	}
	out.Rules = MergeRulesByID(out.Rules, add.Rules, func(r PolicyRule) string { return r.ID })
	out.OutputRules = MergeRulesByID(out.OutputRules, CloneOutputPolicyRules(add.OutputRules), func(r OutputPolicyRule) string { return r.ID })
	out.InputRules = MergeRulesByID(out.InputRules, add.InputRules, func(r InputPolicyRule) string { return r.ID })
	out.TierDefaults = append(out.TierDefaults, add.TierDefaults...)
	out.Tenants = MergeTenantPolicies(out.Tenants, add.Tenants)
	return out
}

// MergeRulesByID appends add's rules to base, but a rule whose id already exists
// in base REPLACES the existing one in place (last-seen wins) rather than being
// appended a second time. id extracts a rule's id. Rules with an empty id are
// always appended (no dedup key). Precedence is controlled by the caller via
// merge order; an empty add slice is a no-op.
func MergeRulesByID[T any](base, add []T, id func(T) string) []T {
	if len(add) == 0 {
		return base
	}
	seen := make(map[string]int, len(base))
	for i, r := range base {
		if rid := id(r); rid != "" {
			seen[rid] = i
		}
	}
	for _, r := range add {
		if rid := id(r); rid != "" {
			if idx, dup := seen[rid]; dup {
				slog.Warn("duplicate policy rule ID in merge — replacing with latest", "rule_id", rid)
				base[idx] = r
				continue
			}
			seen[rid] = len(base)
		}
		base = append(base, r)
	}
	return base
}

// PolicyInstalledAt parses a fragment/bundle's installed_at timestamp (RFC3339)
// used to order fragments by install recency. A missing, blank, unparseable,
// non-string, or non-map value parses to the zero time (oldest precedence) and
// never panics. Unifies the kernel's fragmentInstalledAt and the gateway's
// bundleInstalledAt.
func PolicyInstalledAt(value any) time.Time {
	m, ok := value.(map[string]any)
	if !ok {
		return time.Time{}
	}
	raw, ok := m["installed_at"].(string)
	if !ok {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

// CloneSafetyPolicyWithTierMetadata deep-clones a policy and normalizes its tier
// metadata (tier/selector normalization, scoped-default relocation into
// TierDefaults, per-rule tier/selector inheritance). nil-safe.
func CloneSafetyPolicyWithTierMetadata(policy *SafetyPolicy) *SafetyPolicy {
	out := CloneSafetyPolicy(policy)
	applyPolicyTierMetadata(out)
	return out
}

// CloneSafetyPolicy deep-copies a safety policy. All slice/map/pointer fields
// are independently allocated so the clone can be merged/mutated without
// aliasing the input. Returns nil for a nil input.
func CloneSafetyPolicy(policy *SafetyPolicy) *SafetyPolicy {
	if policy == nil {
		return nil
	}
	out := &SafetyPolicy{
		Version:         policy.Version,
		Tier:            policy.Tier,
		Selector:        TrimPolicySelector(policy.Selector),
		DefaultTenant:   policy.DefaultTenant,
		DefaultDecision: policy.DefaultDecision,
		InputPolicy:     policy.InputPolicy,
		InputRules:      append([]InputPolicyRule{}, policy.InputRules...),
		Rules:           ClonePolicyRules(policy.Rules),
		RequireHuman:    policy.RequireHuman,
		OutputPolicy:    policy.OutputPolicy,
		OutputRules:     CloneOutputPolicyRules(policy.OutputRules),
		TierDefaults:    ClonePolicyTierDefaults(policy.TierDefaults),
		Tenants:         map[string]TenantPolicy{},
	}
	if policy.Tenants != nil {
		for k, v := range policy.Tenants {
			out.Tenants[k] = CloneTenantPolicy(v)
		}
	}
	return out
}

func applyPolicyTierMetadata(policy *SafetyPolicy) {
	if policy == nil {
		return
	}
	policy.Tier = NormalizePolicyTier(policy.Tier)
	policy.Selector = TrimPolicySelector(policy.Selector)
	moveScopedDefaultDecision(policy)
	normalizeTierDefaults(policy)
	for idx, rule := range policy.Rules {
		ruleTier := rule.Tier
		if strings.TrimSpace(ruleTier) == "" {
			ruleTier = policy.Tier
		}
		rule.Tier = NormalizePolicyTier(ruleTier)
		rule.Selector = MergePolicySelector(policy.Selector, rule.Selector)
		if rule.Tier == PolicyTierGlobal {
			rule.Selector = PolicySelector{}
		}
		policy.Rules[idx] = rule
	}
	for idx, rule := range policy.InputRules {
		ruleTier := rule.Tier
		if strings.TrimSpace(ruleTier) == "" {
			ruleTier = policy.Tier
		}
		rule.Tier = NormalizePolicyTier(ruleTier)
		rule.Selector = MergePolicySelector(policy.Selector, rule.Selector)
		if rule.Tier == PolicyTierGlobal {
			rule.Selector = PolicySelector{}
		}
		policy.InputRules[idx] = rule
	}
	if policy.Tier == PolicyTierGlobal {
		policy.Selector = PolicySelector{}
	}
}

func moveScopedDefaultDecision(policy *SafetyPolicy) {
	decision := strings.TrimSpace(policy.DefaultDecision)
	if decision == "" || policy.Tier == PolicyTierGlobal {
		policy.DefaultDecision = decision
		return
	}
	policy.TierDefaults = append(policy.TierDefaults, PolicyTierDefault{
		Tier:     policy.Tier,
		Selector: policy.Selector,
		Decision: decision,
	})
	policy.DefaultDecision = ""
}

func normalizeTierDefaults(policy *SafetyPolicy) {
	defaults := make([]PolicyTierDefault, 0, len(policy.TierDefaults))
	for _, def := range policy.TierDefaults {
		decision := strings.TrimSpace(def.Decision)
		tier := NormalizePolicyTier(def.Tier)
		if decision == "" || tier == PolicyTierGlobal {
			continue
		}
		defaults = append(defaults, PolicyTierDefault{
			Tier:     tier,
			Selector: TrimPolicySelector(def.Selector),
			Decision: decision,
		})
	}
	policy.TierDefaults = defaults
}

// ClonePolicyTierDefaults deep-copies scoped default decision records.
func ClonePolicyTierDefaults(defaults []PolicyTierDefault) []PolicyTierDefault {
	if len(defaults) == 0 {
		return nil
	}
	out := make([]PolicyTierDefault, 0, len(defaults))
	for _, def := range defaults {
		out = append(out, PolicyTierDefault{
			Tier:     strings.TrimSpace(def.Tier),
			Selector: TrimPolicySelector(def.Selector),
			Decision: strings.TrimSpace(def.Decision),
		})
	}
	return out
}

// ClonePolicyRules deep-copies a slice of input/evaluation policy rules.
func ClonePolicyRules(rules []PolicyRule) []PolicyRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]PolicyRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, ClonePolicyRule(rule))
	}
	return out
}

// ClonePolicyRule deep-copies a PolicyRule.
func ClonePolicyRule(rule PolicyRule) PolicyRule {
	cloned := rule
	cloned.ID = strings.TrimSpace(rule.ID)
	cloned.Tier = strings.TrimSpace(rule.Tier)
	cloned.Selector = TrimPolicySelector(rule.Selector)
	cloned.Decision = strings.TrimSpace(rule.Decision)
	cloned.Match = clonePolicyMatch(rule.Match)
	cloned.Constraints = clonePolicyConstraints(rule.Constraints)
	cloned.Remediations = clonePolicyRemediations(rule.Remediations)
	if rule.Velocity != nil {
		velocity := *rule.Velocity
		cloned.Velocity = &velocity
	}
	return cloned
}

func clonePolicyMatch(match PolicyMatch) PolicyMatch {
	cloned := PolicyMatch{
		Tenants:                  append([]string{}, match.Tenants...),
		Topics:                   append([]string{}, match.Topics...),
		Capabilities:             append([]string{}, match.Capabilities...),
		RiskTags:                 append([]string{}, match.RiskTags...),
		Requires:                 append([]string{}, match.Requires...),
		PackIDs:                  append([]string{}, match.PackIDs...),
		ActorIDs:                 append([]string{}, match.ActorIDs...),
		ActorTypes:               append([]string{}, match.ActorTypes...),
		AgentRiskTiers:           append([]string{}, match.AgentRiskTiers...),
		AgentDataClassifications: append([]string{}, match.AgentDataClassifications...),
		Labels:                   cloneStringMap(match.Labels),
		LabelAllowlist:           cloneStringSliceMap(match.LabelAllowlist),
		LabelThreshold:           cloneFloatMap(match.LabelThreshold),
		Predicate:                match.Predicate,
		MCP:                      cloneMCPPolicy(match.MCP),
	}
	if match.SecretsPresent != nil {
		secretsPresent := *match.SecretsPresent
		cloned.SecretsPresent = &secretsPresent
	}
	cloned.Delegation = cloneDelegationMatch(match.Delegation)
	return cloned
}

func clonePolicyConstraints(c PolicyConstraints) PolicyConstraints {
	return PolicyConstraints{
		Budgets: c.Budgets,
		Sandbox: SandboxProfile{
			Isolated:         c.Sandbox.Isolated,
			NetworkAllowlist: append([]string{}, c.Sandbox.NetworkAllowlist...),
			FsReadOnly:       append([]string{}, c.Sandbox.FsReadOnly...),
			FsReadWrite:      append([]string{}, c.Sandbox.FsReadWrite...),
		},
		Toolchain: ToolchainConstraints{
			AllowedTools:    append([]string{}, c.Toolchain.AllowedTools...),
			AllowedCommands: append([]string{}, c.Toolchain.AllowedCommands...),
		},
		Diff: DiffConstraints{
			MaxFiles:      c.Diff.MaxFiles,
			MaxLines:      c.Diff.MaxLines,
			DenyPathGlobs: append([]string{}, c.Diff.DenyPathGlobs...),
		},
		RedactionLevel: c.RedactionLevel,
	}
}

func clonePolicyRemediations(remediations []PolicyRemediation) []PolicyRemediation {
	if len(remediations) == 0 {
		return nil
	}
	out := make([]PolicyRemediation, 0, len(remediations))
	for _, remediation := range remediations {
		cloned := remediation
		cloned.AddLabels = cloneStringMap(remediation.AddLabels)
		cloned.RemoveLabels = append([]string{}, remediation.RemoveLabels...)
		out = append(out, cloned)
	}
	return out
}

func cloneDelegationMatch(match *DelegationMatch) *DelegationMatch {
	if match == nil {
		return nil
	}
	cloned := *match
	cloned.Issuers = append([]string{}, match.Issuers...)
	cloned.RequiredScope = append([]string{}, match.RequiredScope...)
	if match.MaxDepth != nil {
		maxDepth := *match.MaxDepth
		cloned.MaxDepth = &maxDepth
	}
	if match.DelegationRequired != nil {
		required := *match.DelegationRequired
		cloned.DelegationRequired = &required
	}
	return &cloned
}

// CloneOutputPolicyRules deep-copies a slice of output policy rules.
func CloneOutputPolicyRules(rules []OutputPolicyRule) []OutputPolicyRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]OutputPolicyRule, 0, len(rules))
	for _, rule := range rules {
		cloned := OutputPolicyRule{
			ID:       strings.TrimSpace(rule.ID),
			Severity: strings.TrimSpace(rule.Severity),
			Desc:     rule.Desc,
			Decision: strings.TrimSpace(rule.Decision),
			Reason:   rule.Reason,
			Match: OutputPolicyMatch{
				Tenants:         append([]string{}, rule.Match.Tenants...),
				Topics:          append([]string{}, rule.Match.Topics...),
				Capabilities:    append([]string{}, rule.Match.Capabilities...),
				RiskTags:        append([]string{}, rule.Match.RiskTags...),
				Scanners:        append([]string{}, rule.Match.Scanners...),
				ContentPatterns: append([]string{}, rule.Match.ContentPatterns...),
				Keywords:        append([]string{}, rule.Match.Keywords...),
				ContentTypes:    append([]string{}, rule.Match.ContentTypes...),
				Detectors:       append([]string{}, rule.Match.Detectors...),
				OutputSizeGt:    rule.Match.OutputSizeGt,
				MaxOutputBytes:  rule.Match.MaxOutputBytes,
			},
		}
		if rule.Enabled != nil {
			enabled := *rule.Enabled
			cloned.Enabled = &enabled
		}
		if rule.Match.HasError != nil {
			hasError := *rule.Match.HasError
			cloned.Match.HasError = &hasError
		}
		out = append(out, cloned)
	}
	return out
}

// MergeTenantPolicies merges two tenant policy maps. Topic/host allow/deny lists
// append; MaxConcurrent takes the smaller positive bound; MCP lists merge.
// Tenants present only in extra are added.
func MergeTenantPolicies(base map[string]TenantPolicy, extra map[string]TenantPolicy) map[string]TenantPolicy {
	out := map[string]TenantPolicy{}
	for k, v := range base {
		out[k] = CloneTenantPolicy(v)
	}
	for tenant, add := range extra {
		current, ok := out[tenant]
		if !ok {
			out[tenant] = CloneTenantPolicy(add)
			continue
		}
		merged := current
		merged.AllowTopics = append(merged.AllowTopics, add.AllowTopics...)
		merged.DenyTopics = append(merged.DenyTopics, add.DenyTopics...)
		merged.AllowedRepoHosts = append(merged.AllowedRepoHosts, add.AllowedRepoHosts...)
		merged.DeniedRepoHosts = append(merged.DeniedRepoHosts, add.DeniedRepoHosts...)
		if add.MaxConcurrent > 0 && (merged.MaxConcurrent == 0 || add.MaxConcurrent < merged.MaxConcurrent) {
			merged.MaxConcurrent = add.MaxConcurrent
		}
		merged.MCP = MergeMCPPolicy(merged.MCP, add.MCP)
		out[tenant] = merged
	}
	return out
}

// CloneTenantPolicy deep-copies a tenant policy.
func CloneTenantPolicy(policy TenantPolicy) TenantPolicy {
	return TenantPolicy{
		AllowTopics:      append([]string{}, policy.AllowTopics...),
		DenyTopics:       append([]string{}, policy.DenyTopics...),
		AllowedRepoHosts: append([]string{}, policy.AllowedRepoHosts...),
		DeniedRepoHosts:  append([]string{}, policy.DeniedRepoHosts...),
		MaxConcurrent:    policy.MaxConcurrent,
		MCP:              cloneMCPPolicy(policy.MCP),
	}
}

// MergeMCPPolicy merges two MCP policies (base elements precede extra elements
// in each list; inputs are not aliased).
func MergeMCPPolicy(base, extra MCPPolicy) MCPPolicy {
	return MCPPolicy{
		AllowServers:   append(append([]string{}, base.AllowServers...), extra.AllowServers...),
		DenyServers:    append(append([]string{}, base.DenyServers...), extra.DenyServers...),
		AllowTools:     append(append([]string{}, base.AllowTools...), extra.AllowTools...),
		DenyTools:      append(append([]string{}, base.DenyTools...), extra.DenyTools...),
		AllowResources: append(append([]string{}, base.AllowResources...), extra.AllowResources...),
		DenyResources:  append(append([]string{}, base.DenyResources...), extra.DenyResources...),
		AllowActions:   append(append([]string{}, base.AllowActions...), extra.AllowActions...),
		DenyActions:    append(append([]string{}, base.DenyActions...), extra.DenyActions...),
	}
}

func cloneMCPPolicy(policy MCPPolicy) MCPPolicy {
	return MCPPolicy{
		AllowServers:   append([]string{}, policy.AllowServers...),
		DenyServers:    append([]string{}, policy.DenyServers...),
		AllowTools:     append([]string{}, policy.AllowTools...),
		DenyTools:      append([]string{}, policy.DenyTools...),
		AllowResources: append([]string{}, policy.AllowResources...),
		DenyResources:  append([]string{}, policy.DenyResources...),
		AllowActions:   append([]string{}, policy.AllowActions...),
		DenyActions:    append([]string{}, policy.DenyActions...),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneStringSliceMap(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[key] = append([]string{}, value...)
	}
	return out
}

func cloneFloatMap(values map[string]float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]float64, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
