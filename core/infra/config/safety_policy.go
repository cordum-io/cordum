package config

import (
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SafetyPolicy defines allow/deny rules per tenant.
type SafetyPolicy struct {
	Version     string            `yaml:"version"`
	Tier        string            `yaml:"tier,omitempty" json:"tier,omitempty"`
	Selector    PolicySelector    `yaml:"selector,omitempty" json:"selector,omitempty"`
	Rules       []PolicyRule      `yaml:"rules"`
	InputPolicy InputPolicyConfig `yaml:"input_policy"`
	InputRules  []InputPolicyRule `yaml:"input_rules"`
	// RequireHuman controls DENY → REQUIRE_HUMAN downgrade thresholds
	// applied during input-rule evaluation. Per architect amendment
	// comment-79a9e609 on task-96f931fe.
	RequireHuman    RequireHumanThreshold   `yaml:"require_human,omitempty" json:"require_human,omitempty"`
	OutputPolicy    OutputPolicyConfig      `yaml:"output_policy"`
	OutputRules     []OutputPolicyRule      `yaml:"output_rules"`
	DefaultTenant   string                  `yaml:"default_tenant,omitempty"`
	DefaultDecision string                  `yaml:"default_decision,omitempty" json:"default_decision,omitempty"` // allow|deny (default: deny = fail-closed)
	Tenants         map[string]TenantPolicy `yaml:"tenants"`
	TierDefaults    []PolicyTierDefault     `yaml:"-" json:"-"`
}

// InputPolicyConfig controls input-policy evaluation behavior.
type InputPolicyConfig struct {
	Enabled      bool   `yaml:"enabled"`
	FailMode     string `yaml:"fail_mode,omitempty"`      // open|closed (default: closed = requeue when kernel down)
	MaxScanBytes int    `yaml:"max_scan_bytes,omitempty"` // default 2 MiB
}

// RequireHumanThreshold defines when a matched input-rule whose authored
// decision is "deny" should be downgraded to REQUIRE_HUMAN instead. The
// safety kernel reads the threshold from the policy snapshot and consults
// it inside the input-rule dispatch loop.
//
// Per architect amendment comment-79a9e609 (task-96f931fe): an input rule
// whose finding falls below either floor — OR a prompt-only request that
// lacks an ActionDescriptor — is "truly ambiguous" and resolves to a
// human approval rather than a hard deny. DoD #4 ("FP examples allowed
// or require-human only when truly ambiguous") authorizes this routing.
//
// The threshold is intentionally a 2-output dial: rules below the floor
// route to REQUIRE_HUMAN, rules at-or-above stay DENY. No third "ALLOW"
// branch — adding one would require a session-metadata educational-context
// carrier that does not exist (carved out by amendment §(1)).
//
// Zero values fall back to the strictest interpretation: empty
// MinSeverityForDeny means any severity floor is acceptable for DENY;
// zero MinConfidenceForDeny means any confidence is acceptable. This
// preserves the legacy DENY-everything behavior when an operator has
// not opted in.
type RequireHumanThreshold struct {
	// MinSeverityForDeny is the minimum finding severity that a "deny"
	// rule must produce to remain DENY. Severities below this floor
	// downgrade to REQUIRE_HUMAN. Uses the existing Severity string
	// vocabulary: "low", "medium", "high", "critical".
	MinSeverityForDeny string `yaml:"min_severity_for_deny,omitempty"`
	// MinConfidenceForDeny is the minimum finding confidence (0.0–1.0)
	// that a "deny" rule must produce to remain DENY. Lower values
	// downgrade to REQUIRE_HUMAN.
	MinConfidenceForDeny float32 `yaml:"min_confidence_for_deny,omitempty"`
	// DowngradeWhenPromptOnly downgrades a "deny" rule to REQUIRE_HUMAN
	// when the request has no ActionDescriptor (prompt-only — no
	// action-bound target). Default false preserves legacy behavior.
	DowngradeWhenPromptOnly bool `yaml:"downgrade_when_prompt_only,omitempty"`
}

// InputPolicyRule defines policy checks on job input content.
// Mirrors OutputPolicyRule — same scanner/pattern infrastructure applied pre-execution.
type InputPolicyRule struct {
	ID       string           `yaml:"id"`
	Tier     string           `yaml:"tier,omitempty" json:"tier,omitempty"`
	Selector PolicySelector   `yaml:"selector,omitempty" json:"selector,omitempty"`
	Enabled  *bool            `yaml:"enabled,omitempty"`
	Severity string           `yaml:"severity"` // low|medium|high|critical
	Desc     string           `yaml:"description"`
	Match    InputPolicyMatch `yaml:"match"`
	Decision string           `yaml:"decision"` // deny|require_approval
	Reason   string           `yaml:"reason"`
}

// InputPolicyMatch captures matching criteria for input content checks.
// Mirrors OutputPolicyMatch with input-specific field names.
type InputPolicyMatch struct {
	Tenants         []string     `yaml:"tenants"`
	Topics          []string     `yaml:"topics"`
	Capabilities    []string     `yaml:"capabilities"`
	RiskTags        []string     `yaml:"risk_tags"`
	Scanners        []string     `yaml:"scanners"`
	ContentPatterns []string     `yaml:"content_patterns"`
	Keywords        []string     `yaml:"keywords"`
	ContentTypes    []string     `yaml:"content_types"`
	Detectors       []string     `yaml:"detectors"`
	InputSizeGt     int64        `yaml:"input_size_gt"`
	MaxInputBytes   int64        `yaml:"max_input_bytes"`
	Scope           *ScopeConfig `yaml:"scope,omitempty"`
}

// ScopeConfig defines a deterministic instruction-vs-cart scope evaluator.
// It compares the declared instruction (what the user asked for) against the
// items in the cart/payload to detect unauthorized modifications (e.g., TX2
// adding gift_card to a grocery purchase). The evaluator is not a keyword
// blocklist — it performs structured comparison with category normalization.
type ScopeConfig struct {
	// InstructionPath is a dot-separated JSON path to the instruction field
	// in the input payload (e.g., "instruction" or "request.instruction").
	InstructionPath string `yaml:"instruction_path" json:"instruction_path"`
	// ItemsPath is a dot-separated JSON path to the items array
	// (e.g., "items" or "cart.items").
	ItemsPath string `yaml:"items_path" json:"items_path"`
	// CategoryPath is the field name within each item that holds the category
	// (e.g., "category" or "type"). Defaults to "category".
	CategoryPath string `yaml:"category_path,omitempty" json:"category_path,omitempty"`
	// NamePath is the field name within each item that holds the item name
	// (e.g., "name" or "product"). Defaults to "name".
	NamePath string `yaml:"name_path,omitempty" json:"name_path,omitempty"`
	// AllowedCategories lists the categories that are permitted when the
	// instruction matches specific intents. Map of normalized intent keyword
	// to list of allowed category strings. Empty means all categories allowed
	// for that intent.
	AllowedCategories map[string][]string `yaml:"allowed_categories,omitempty" json:"allowed_categories,omitempty"`
	// Aliases maps alternative category names to their canonical form for
	// normalization (e.g., "gift-card" -> "gift_card", "giftcard" -> "gift_card").
	Aliases map[string]string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	// OnMissingInput controls behavior when required fields (instruction or items)
	// are absent from the payload. "deny" (default) or "allow".
	OnMissingInput string `yaml:"on_missing_input,omitempty" json:"on_missing_input,omitempty"`
	// OnAmbiguous controls behavior when the instruction cannot be confidently
	// classified into a known intent. "deny" (default) or "allow".
	OnAmbiguous string `yaml:"on_ambiguous,omitempty" json:"on_ambiguous,omitempty"`
}

// OutputPolicyConfig controls output-policy evaluation behavior.
type OutputPolicyConfig struct {
	Enabled  bool   `yaml:"enabled"`
	FailMode string `yaml:"fail_mode,omitempty"` // open|closed (open is current runtime behavior)
}

type PolicyRule struct {
	ID           string              `yaml:"id"`
	Tier         string              `yaml:"tier,omitempty" json:"tier,omitempty"`
	Selector     PolicySelector      `yaml:"selector,omitempty" json:"selector,omitempty"`
	Match        PolicyMatch         `yaml:"match"`
	Velocity     *VelocityConfig     `yaml:"velocity,omitempty"`
	Decision     string              `yaml:"decision"` // allow|deny|require_approval|allow_with_constraints|throttle
	Reason       string              `yaml:"reason"`
	Constraints  PolicyConstraints   `yaml:"constraints"`
	Remediations []PolicyRemediation `yaml:"remediations"`
}

const (
	PolicyTierGlobal   = "global"
	PolicyTierWorkflow = "workflow"
	PolicyTierJob      = "job"
)

// PolicySelector scopes workflow/job-tier policy fragments and rules.
type PolicySelector struct {
	WorkflowID string `yaml:"workflow_id,omitempty" json:"workflow_id,omitempty"`
	JobID      string `yaml:"job_id,omitempty" json:"job_id,omitempty"`
	SessionID  string `yaml:"session_id,omitempty" json:"session_id,omitempty"`
}

// PolicyTierDefault preserves scoped default_decision values through bundle
// merges. Global default_decision remains SafetyPolicy.DefaultDecision.
type PolicyTierDefault struct {
	Tier     string         `json:"tier"`
	Selector PolicySelector `json:"selector"`
	Decision string         `json:"decision"`
}

// NormalizePolicyTier returns the canonical policy tier. Empty means global
// for backward compatibility with existing bundles.
func NormalizePolicyTier(raw string) string {
	tier := strings.ToLower(strings.TrimSpace(raw))
	if tier == "" {
		return PolicyTierGlobal
	}
	return tier
}

// IsValidPolicyTier reports whether raw is one of the supported policy tiers.
func IsValidPolicyTier(raw string) bool {
	switch NormalizePolicyTier(raw) {
	case PolicyTierGlobal, PolicyTierWorkflow, PolicyTierJob:
		return true
	default:
		return false
	}
}

// MergePolicySelector overlays non-empty override fields onto base.
func MergePolicySelector(base, override PolicySelector) PolicySelector {
	out := TrimPolicySelector(base)
	override = TrimPolicySelector(override)
	if override.WorkflowID != "" {
		out.WorkflowID = override.WorkflowID
	}
	if override.JobID != "" {
		out.JobID = override.JobID
	}
	if override.SessionID != "" {
		out.SessionID = override.SessionID
	}
	return out
}

// TrimPolicySelector trims all selector fields.
func TrimPolicySelector(selector PolicySelector) PolicySelector {
	return PolicySelector{
		WorkflowID: strings.TrimSpace(selector.WorkflowID),
		JobID:      strings.TrimSpace(selector.JobID),
		SessionID:  strings.TrimSpace(selector.SessionID),
	}
}

// PolicySelectorKey returns the lookup key for the requested tier.
func PolicySelectorKey(tier string, selector PolicySelector) string {
	selector = TrimPolicySelector(selector)
	switch NormalizePolicyTier(tier) {
	case PolicyTierWorkflow:
		return selector.WorkflowID
	case PolicyTierJob:
		if selector.JobID != "" {
			return selector.JobID
		}
		return selector.SessionID
	default:
		return ""
	}
}

// VelocityConfig defines sliding-window rate limiting for a policy rule.
// When configured on a rule, the rule only fires if the rate limit is exceeded.
type VelocityConfig struct {
	MaxRequests   int    `yaml:"max_requests" json:"max_requests"`
	WindowSeconds int    `yaml:"window_seconds" json:"window_seconds"`
	Key           string `yaml:"key" json:"key"` // e.g. "labels.session_id", "actor_id", "tenant", "topic", "tenant:topic"
}

// Validate checks that VelocityConfig has valid values.
func (v *VelocityConfig) Validate(ruleID string) error {
	if v == nil {
		return nil
	}
	if v.MaxRequests <= 0 {
		return fmt.Errorf("rule %q: velocity.max_requests must be >= 1, got %d", ruleID, v.MaxRequests)
	}
	if v.WindowSeconds <= 0 {
		return fmt.Errorf("rule %q: velocity.window_seconds must be >= 1, got %d", ruleID, v.WindowSeconds)
	}
	if strings.TrimSpace(v.Key) == "" {
		return fmt.Errorf("rule %q: velocity.key must be non-empty", ruleID)
	}
	return nil
}

// ResolveKey extracts the velocity bucket key from the policy input.
func (v *VelocityConfig) ResolveKey(input PolicyInput) string {
	if v == nil || v.Key == "" {
		return ""
	}
	// Compound keys: "tenant:topic" → "default:job.visa.evaluate"
	if strings.Contains(v.Key, ":") {
		parts := strings.Split(v.Key, ":")
		resolved := make([]string, 0, len(parts))
		for _, part := range parts {
			val := resolveKeyPart(strings.TrimSpace(part), input)
			if val == "" {
				return ""
			}
			resolved = append(resolved, val)
		}
		return strings.Join(resolved, ":")
	}
	return resolveKeyPart(v.Key, input)
}

func resolveKeyPart(key string, input PolicyInput) string {
	// Label lookup: "labels.session_id" → input.Labels["session_id"]
	if strings.HasPrefix(key, "labels.") {
		labelKey := strings.TrimPrefix(key, "labels.")
		return input.Labels[labelKey]
	}
	switch key {
	case "actor_id":
		return input.Meta.ActorID
	case "actor_type":
		return input.Meta.ActorType
	case "tenant":
		return input.Tenant
	case "topic":
		return input.Topic
	case "pack_id":
		return input.Meta.PackID
	case "capability":
		return input.Meta.Capability
	default:
		return ""
	}
}

// OutputPolicyRule defines policy checks on job outputs.
type OutputPolicyRule struct {
	ID       string            `yaml:"id"`
	Enabled  *bool             `yaml:"enabled,omitempty"`
	Severity string            `yaml:"severity"` // low|medium|high|critical
	Desc     string            `yaml:"description"`
	Match    OutputPolicyMatch `yaml:"match"`
	Decision string            `yaml:"decision"` // allow|deny|quarantine|redact
	Reason   string            `yaml:"reason"`
}

// OutputPolicyMatch captures matching criteria for output content checks.
type OutputPolicyMatch struct {
	Tenants         []string `yaml:"tenants"`
	Topics          []string `yaml:"topics"`
	Capabilities    []string `yaml:"capabilities"`
	RiskTags        []string `yaml:"risk_tags"`
	Scanners        []string `yaml:"scanners"`
	ContentPatterns []string `yaml:"content_patterns"`
	Keywords        []string `yaml:"keywords"`
	ContentTypes    []string `yaml:"content_types"`
	Detectors       []string `yaml:"detectors"` // secret_leak|pii|code_injection|custom
	OutputSizeGt    int64    `yaml:"output_size_gt"`
	MaxOutputBytes  int64    `yaml:"max_output_bytes"`
	HasError        *bool    `yaml:"has_error,omitempty"`
}

type PolicyMatch struct {
	Tenants                  []string            `yaml:"tenants"`
	Topics                   []string            `yaml:"topics"`
	Capabilities             []string            `yaml:"capabilities"`
	RiskTags                 []string            `yaml:"risk_tags"`
	Requires                 []string            `yaml:"requires"`
	PackIDs                  []string            `yaml:"pack_ids"`
	ActorIDs                 []string            `yaml:"actor_ids"`
	ActorTypes               []string            `yaml:"actor_types"`
	AgentRiskTiers           []string            `yaml:"agent_risk_tiers"`
	AgentDataClassifications []string            `yaml:"agent_data_classifications"`
	Labels                   map[string]string   `yaml:"labels"`
	LabelAllowlist           map[string][]string `yaml:"label_allowlist,omitempty"` // deny when label value NOT in list
	LabelThreshold           map[string]float64  `yaml:"label_threshold,omitempty"` // deny when label value > threshold
	SecretsPresent           *bool               `yaml:"secrets_present,omitempty"`
	Predicate                string              `yaml:"predicate,omitempty"`
	Delegation               *DelegationMatch    `yaml:"delegation,omitempty"`
	MCP                      MCPPolicy           `yaml:"mcp"`
}

type PolicyConstraints struct {
	Budgets        BudgetConstraints    `yaml:"budgets"`
	Sandbox        SandboxProfile       `yaml:"sandbox"`
	Toolchain      ToolchainConstraints `yaml:"toolchain"`
	Diff           DiffConstraints      `yaml:"diff"`
	RedactionLevel string               `yaml:"redaction_level"`
}

// PolicyRemediation suggests a safer alternative when a request is denied.
type PolicyRemediation struct {
	ID                    string            `yaml:"id"`
	Title                 string            `yaml:"title"`
	Summary               string            `yaml:"summary"`
	ReplacementTopic      string            `yaml:"replacement_topic"`
	ReplacementCapability string            `yaml:"replacement_capability"`
	AddLabels             map[string]string `yaml:"add_labels"`
	RemoveLabels          []string          `yaml:"remove_labels"`
}

type BudgetConstraints struct {
	MaxRuntimeMs      int64 `yaml:"max_runtime_ms"`
	MaxRetries        int32 `yaml:"max_retries"`
	MaxArtifactBytes  int64 `yaml:"max_artifact_bytes"`
	MaxConcurrentJobs int32 `yaml:"max_concurrent_jobs"`
}

type SandboxProfile struct {
	Isolated         bool     `yaml:"isolated"`
	NetworkAllowlist []string `yaml:"network_allowlist"`
	FsReadOnly       []string `yaml:"fs_read_only"`
	FsReadWrite      []string `yaml:"fs_read_write"`
}

type ToolchainConstraints struct {
	AllowedTools    []string `yaml:"allowed_tools"`
	AllowedCommands []string `yaml:"allowed_commands"`
}

type DiffConstraints struct {
	MaxFiles      int32    `yaml:"max_files"`
	MaxLines      int32    `yaml:"max_lines"`
	DenyPathGlobs []string `yaml:"deny_path_globs"`
}

// MCPPolicy defines allow/deny rules for MCP servers/tools/resources, plus
// the per-tenant EDGE-100 MCP Gateway enable flag.
// TenantPolicy captures legacy allow/deny topics per tenant.
type TenantPolicy struct {
	AllowTopics      []string  `yaml:"allow_topics"`
	DenyTopics       []string  `yaml:"deny_topics"`
	AllowedRepoHosts []string  `yaml:"allowed_repo_hosts"`
	DeniedRepoHosts  []string  `yaml:"denied_repo_hosts"`
	MaxConcurrent    int       `yaml:"max_concurrent_jobs"`
	MCP              MCPPolicy `yaml:"mcp"`
}

// PolicyInput captures the info needed to evaluate a policy rule.
type PolicyInput struct {
	Tenant         string
	Topic          string
	Labels         map[string]string
	Meta           PolicyMeta
	SecretsPresent bool
	MCP            MCPRequest
	Delegation     *DelegationContext
	// Action carries structured request metadata for deterministic pre-dispatch
	// action-layer gates (file/url/tenant/mutation/mcp/provenance). nil means
	// no action-layer evaluation; existing rule evaluation runs unchanged.
	Action *ActionDescriptor
}

// ActionKind enumerates the action-gate dispatch families. Gates inspect this
// to short-circuit when an action does not target their domain.
type ActionKind string

const (
	ActionKindFile            ActionKind = "file"
	ActionKindURL             ActionKind = "url"
	ActionKindTenantQuery     ActionKind = "tenant_query"
	ActionKindMutation        ActionKind = "mutation"
	ActionKindMCPCall         ActionKind = "mcp_call"
	ActionKindProvenanceCheck ActionKind = "provenance_check"
)

// ActionVerb names the operation an actor is requesting. Free-form by design:
// new packs/tools must be able to declare verbs without a config bump. The
// destructive set is enforced by the mutation gate, not by parser validation.
type ActionVerb string

const (
	ActionVerbRead          ActionVerb = "read"
	ActionVerbWrite         ActionVerb = "write"
	ActionVerbDelete        ActionVerb = "delete"
	ActionVerbDrop          ActionVerb = "drop"
	ActionVerbTruncate      ActionVerb = "truncate"
	ActionVerbExport        ActionVerb = "export"
	ActionVerbPayment       ActionVerb = "payment"
	ActionVerbAdminGrant    ActionVerb = "admin_grant"
	ActionVerbAdminRevoke   ActionVerb = "admin_revoke"
	ActionVerbRoleAssign    ActionVerb = "role_assign"
	ActionVerbRoleRemove    ActionVerb = "role_remove"
	ActionVerbLicenseCreate ActionVerb = "license_create"
	ActionVerbLicenseRevoke ActionVerb = "license_revoke"
	ActionVerbLicenseChange ActionVerb = "license_change"
	ActionVerbKeyRotate     ActionVerb = "key_rotate"
	ActionVerbKeyDelete     ActionVerb = "key_delete"
	ActionVerbSecretsWrite  ActionVerb = "secrets_write"
	ActionVerbSecretsDelete ActionVerb = "secrets_delete"
	ActionVerbConfigWrite   ActionVerb = "config_write"
	ActionVerbConfigDelete  ActionVerb = "config_delete"
	ActionVerbBackupRestore ActionVerb = "backup_restore"
	ActionVerbTenantCreate  ActionVerb = "tenant_create"
	ActionVerbTenantDelete  ActionVerb = "tenant_delete"
)

// ActionDescriptor carries the structured request payload that action-layer
// gates evaluate. Every field is optional; gates inspect Kind+Verb first and
// short-circuit when no relevant data is present. Untrusted body fields
// (TargetPath, TargetURL, Args, ApprovalClaim.ClaimText) MUST NOT be the sole
// basis for authorization — auth-derived tenant and backend-resolved approval
// records take precedence.
type ActionDescriptor struct {
	Kind   ActionKind `json:"kind,omitempty"`
	Verb   ActionVerb `json:"verb,omitempty"`
	Server string     `json:"server,omitempty"` // MCP server identifier (mcp_call)
	Tool   string     `json:"tool,omitempty"`   // MCP tool identifier (mcp_call)

	// TargetPath is a filesystem path (file kind). Untrusted; gates canonicalize
	// before matching.
	TargetPath string `json:"target_path,omitempty"`
	// TargetURL is an absolute URL (url kind). Untrusted; gates parse + resolve.
	TargetURL string `json:"target_url,omitempty"`
	// TargetResource describes the object a mutation/tenant-query operates on.
	// OwnerTenant is server-derived (not from request body) when populated by
	// the gateway; otherwise gates compare against AuthContext.Tenant.
	TargetResource *ActionTargetResource `json:"target_resource,omitempty"`

	// Filters captures structured query predicates (column => literal). Wildcards
	// lists predicates whose value was '*' or otherwise unbounded; tenant gate
	// blocks wildcards on owner_id/tenant_id.
	Filters   map[string]string `json:"filters,omitempty"`
	Wildcards []string          `json:"wildcards,omitempty"`

	// Args is arbitrary tool/MCP arg map (mcp_call), capped at 64KB serialized
	// upstream. Gates inspect known sensitive keys (force, no_confirm, recursive).
	Args map[string]any `json:"args,omitempty"`

	// RiskTags is a free-form set surfaced by the upstream classifier (e.g.
	// "requires_provenance", "data:pii"). Used by gates to widen denials.
	RiskTags []string `json:"risk_tags,omitempty"`

	// RequiredEntitlement is the license/capability token that the calling
	// identity must hold for an mcp_call action. Resolved server-side from
	// per-server registry metadata, not user-supplied. Empty means the
	// action carries no entitlement requirement (e.g. read-only tools).
	RequiredEntitlement string `json:"required_entitlement,omitempty"`

	// ApprovalClaim carries the user-presented approval reference. ClaimText is
	// untrusted prose ("approved by CFO") and MUST NOT pass on its own.
	// ApprovalRef is looked up server-side against Cordum approval records.
	ApprovalClaim *ActionApprovalClaim `json:"approval_claim,omitempty"`
}

// ActionTargetResource identifies an object referenced by an action. OwnerTenant
// is authoritative only when gateway-supplied; gates that need it for tenant
// boundaries cross-check against AuthContext.Tenant.
type ActionTargetResource struct {
	Type        string `json:"type,omitempty"`
	ID          string `json:"id,omitempty"`
	OwnerTenant string `json:"owner_tenant,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
}

// ActionApprovalClaim represents a caller's claim of human approval for a
// destructive action. The provenance gate requires ApprovalRef to resolve to a
// Cordum EdgeApproval; ClaimText is logged for audit only and never grants.
type ActionApprovalClaim struct {
	ClaimText   string `json:"claim_text,omitempty"`
	ApprovalRef string `json:"approval_ref,omitempty"`
}

// ActionArgsMaxSerializedBytes caps the wire size of ActionDescriptor.Args.
// The gateway rejects oversize requests before reaching the kernel.
const ActionArgsMaxSerializedBytes = 64 * 1024

// PolicyMeta captures structured job metadata for policy checks.
type PolicyMeta struct {
	ActorID        string
	ActorType      string
	IdempotencyKey string
	Capability     string
	RiskTags       []string
	Requires       []string
	PackID         string
	// Agent identity fields — populated from label-based lookup when agent_id is present.
	AgentID                  string
	AgentRiskTier            string
	AgentDataClassifications []string
	AgentName                string
	AgentTeam                string
}

// PolicyDecision is the result of policy evaluation.
type PolicyDecision struct {
	Decision         string
	Reason           string
	RuleID           string
	RuleTier         string
	Constraints      PolicyConstraints
	ApprovalRequired bool
	Remediations     []PolicyRemediation
}

// MCPRequest describes an MCP invocation for policy evaluation.
type MCPRequest struct {
	Server   string
	Tool     string
	Resource string
	Action   string
}

// ParseSafetyPolicy parses a policy bundle from YAML bytes.
func ParseSafetyPolicy(data []byte) (*SafetyPolicy, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if err := validateConfigSchema("safety policy", safetyPolicySchemaFile, data); err != nil {
		return nil, err
	}
	var policy SafetyPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("parse safety policy: %w", err)
	}
	if policy.Tenants == nil {
		policy.Tenants = map[string]TenantPolicy{}
	}
	if err := validatePolicyTierSelector("policy", policy.Tier, policy.Selector); err != nil {
		return nil, fmt.Errorf("parse safety policy: %w", err)
	}
	for _, rule := range policy.InputRules {
		tier := rule.Tier
		if strings.TrimSpace(tier) == "" {
			tier = policy.Tier
		}
		selector := MergePolicySelector(policy.Selector, rule.Selector)
		if err := validatePolicyTierSelector(fmt.Sprintf("input_rule %q", rule.ID), tier, selector); err != nil {
			return nil, fmt.Errorf("parse safety policy: %w", err)
		}
	}
	// Validate velocity configs on all rules.
	for _, rule := range policy.Rules {
		tier := rule.Tier
		if strings.TrimSpace(tier) == "" {
			tier = policy.Tier
		}
		selector := MergePolicySelector(policy.Selector, rule.Selector)
		if err := validatePolicyTierSelector(fmt.Sprintf("rule %q", rule.ID), tier, selector); err != nil {
			return nil, fmt.Errorf("parse safety policy: %w", err)
		}
		if rule.Velocity != nil {
			if err := rule.Velocity.Validate(rule.ID); err != nil {
				return nil, fmt.Errorf("parse safety policy: %w", err)
			}
		}
		if err := rule.Match.Delegation.Validate(); err != nil {
			return nil, fmt.Errorf("parse safety policy: rule %q: %w", rule.ID, err)
		}
		if err := validateDelegationPredicate(rule.Match.Predicate); err != nil {
			return nil, fmt.Errorf("parse safety policy: rule %q: %w", rule.ID, err)
		}
	}
	return &policy, nil
}

func validatePolicyTierSelector(scope, tier string, selector PolicySelector) error {
	normalized := NormalizePolicyTier(tier)
	if !IsValidPolicyTier(normalized) {
		return fmt.Errorf("%s tier %q must be one of: global, workflow, job", scope, tier)
	}
	key := PolicySelectorKey(normalized, selector)
	if normalized != PolicyTierGlobal && key == "" {
		return fmt.Errorf("%s selector is required for %s tier", scope, normalized)
	}
	return nil
}

// Evaluate returns the decision for the provided input, using rules or legacy tenant config.
func (p *SafetyPolicy) Evaluate(input PolicyInput) PolicyDecision {
	rules := p.Rules
	if len(rules) == 0 {
		rules = legacyRules(p)
	}
	for _, rule := range rules {
		if matchRule(rule.Match, input) {
			decision := normalizeDecision(rule.Decision)
			return PolicyDecision{
				Decision:         decision,
				Reason:           rule.Reason,
				RuleID:           rule.ID,
				RuleTier:         NormalizePolicyTier(rule.Tier),
				Constraints:      rule.Constraints,
				ApprovalRequired: decision == "require_approval",
				Remediations:     rule.Remediations,
			}
		}
	}
	dd := strings.ToLower(strings.TrimSpace(p.DefaultDecision))
	if dd == "allow" || dd == "permit" {
		return PolicyDecision{Decision: "allow", Reason: "no matching rule — default policy: allow", RuleTier: NormalizePolicyTier(p.Tier)}
	}
	// Fail-closed: empty, "deny", or any unrecognized default_decision value
	// results in deny. This prevents typos like "alow" from silently allowing.
	if dd != "" && dd != "deny" {
		slog.Warn("unrecognized default_decision value, defaulting to deny (fail-closed)", "raw", p.DefaultDecision)
	}
	return PolicyDecision{
		Decision: "deny",
		Reason:   "no matching rule — default policy: deny",
		RuleTier: NormalizePolicyTier(p.Tier),
	}
}

// EffectiveRules returns the active rule list (rules or legacy-generated rules).
// Used by the kernel for velocity-aware evaluation that needs to iterate rules directly.
func (p *SafetyPolicy) EffectiveRules() []PolicyRule {
	if len(p.Rules) > 0 {
		return p.Rules
	}
	return legacyRules(p)
}

// DefaultPolicyDecision returns the default decision when no rule matches.
func (p *SafetyPolicy) DefaultPolicyDecision() PolicyDecision {
	dd := strings.ToLower(strings.TrimSpace(p.DefaultDecision))
	if dd == "allow" || dd == "permit" {
		return PolicyDecision{Decision: "allow", Reason: "no matching rule — default policy: allow", RuleTier: NormalizePolicyTier(p.Tier)}
	}
	if dd != "" && dd != "deny" {
		slog.Warn("unrecognized default_decision value, defaulting to deny (fail-closed)", "raw", p.DefaultDecision)
	}
	return PolicyDecision{Decision: "deny", Reason: "no matching rule — default policy: deny", RuleTier: NormalizePolicyTier(p.Tier)}
}

// MatchRule checks if a policy rule's match criteria are satisfied by the input.
func MatchRule(match PolicyMatch, input PolicyInput) bool {
	return matchRule(match, input)
}

// NormalizeDecision normalizes a raw decision string to a canonical form.
func NormalizeDecision(raw string) string {
	return normalizeDecision(raw)
}

// BuildDecision constructs a PolicyDecision from a matched rule.
func BuildDecision(rule PolicyRule) PolicyDecision {
	decision := normalizeDecision(rule.Decision)
	return PolicyDecision{
		Decision:         decision,
		Reason:           rule.Reason,
		RuleID:           rule.ID,
		RuleTier:         NormalizePolicyTier(rule.Tier),
		Constraints:      rule.Constraints,
		ApprovalRequired: decision == "require_approval",
		Remediations:     rule.Remediations,
	}
}

func normalizeDecision(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "allow", "permit":
		return "allow"
	case "deny", "block":
		return "deny"
	case "require_approval", "require-approval", "require_human":
		return "require_approval"
	case "allow_with_constraints", "allow-with-constraints":
		return "allow_with_constraints"
	case "throttle":
		return "throttle"
	default:
		slog.Warn("normalizeDecision: unrecognized decision value, defaulting to deny (fail-closed)", "raw", raw)
		return "deny"
	}
}

func legacyRules(p *SafetyPolicy) []PolicyRule {
	if p == nil || len(p.Tenants) == 0 {
		return nil
	}
	out := []PolicyRule{}
	for tenant, tp := range p.Tenants {
		for idx, pat := range tp.DenyTopics {
			out = append(out, PolicyRule{
				ID:       fmt.Sprintf("legacy:%s:deny:%d", tenant, idx+1),
				Decision: "deny",
				Reason:   fmt.Sprintf("topic %q denied by tenant policy", pat),
				Match: PolicyMatch{
					Tenants: []string{tenant},
					Topics:  []string{pat},
					MCP:     tp.MCP,
				},
			})
		}
		for idx, pat := range tp.AllowTopics {
			out = append(out, PolicyRule{
				ID:       fmt.Sprintf("legacy:%s:allow:%d", tenant, idx+1),
				Decision: "allow",
				Reason:   "",
				Match: PolicyMatch{
					Tenants: []string{tenant},
					Topics:  []string{pat},
					MCP:     tp.MCP,
				},
			})
		}
	}
	return out
}

func matchRule(match PolicyMatch, input PolicyInput) bool {
	if len(match.Tenants) > 0 && !containsString(match.Tenants, input.Tenant) {
		return false
	}
	if len(match.Topics) > 0 && !matchAnyTopic(match.Topics, input.Topic) {
		return false
	}
	if len(match.Capabilities) > 0 && !containsString(match.Capabilities, input.Meta.Capability) {
		return false
	}
	if len(match.RiskTags) > 0 && !containsAny(match.RiskTags, input.Meta.RiskTags) {
		return false
	}
	if len(match.Requires) > 0 && !containsAll(input.Meta.Requires, match.Requires) {
		return false
	}
	if len(match.PackIDs) > 0 && !containsString(match.PackIDs, input.Meta.PackID) {
		return false
	}
	if len(match.ActorIDs) > 0 && !containsString(match.ActorIDs, input.Meta.ActorID) {
		return false
	}
	if len(match.ActorTypes) > 0 && !containsString(match.ActorTypes, input.Meta.ActorType) {
		return false
	}
	if len(match.AgentRiskTiers) > 0 && !containsString(match.AgentRiskTiers, input.Meta.AgentRiskTier) {
		return false
	}
	if len(match.AgentDataClassifications) > 0 && !containsAny(match.AgentDataClassifications, input.Meta.AgentDataClassifications) {
		return false
	}
	if match.SecretsPresent != nil && input.SecretsPresent != *match.SecretsPresent {
		return false
	}
	if len(match.Labels) > 0 && !labelsMatch(match.Labels, input.Labels) {
		return false
	}
	if len(match.LabelAllowlist) > 0 && !labelAllowlistMatch(match.LabelAllowlist, input.Labels) {
		return false
	}
	if len(match.LabelThreshold) > 0 && !labelThresholdMatch(match.LabelThreshold, input.Labels) {
		return false
	}
	if !delegationPredicateMatch(match.Predicate, input.Delegation) {
		return false
	}
	if !evaluateDelegationMatch(match.Delegation, input.Delegation) {
		return false
	}
	if !mcpMatch(match.MCP, input.MCP) {
		return false
	}
	return true
}

// labelAllowlistMatch returns true when ANY label value is NOT in its allowlist.
// This is an inverse match: the rule fires (returns true) when a value is OUTSIDE the list.
// If the label is missing from input, skip that check (fail-open).
func labelAllowlistMatch(allowlists map[string][]string, labels map[string]string) bool {
	for key, allowed := range allowlists {
		actual, exists := labels[key]
		if !exists {
			continue // label not present → skip check (fail-open)
		}
		found := false
		lower := strings.ToLower(strings.TrimSpace(actual))
		for _, v := range allowed {
			if strings.ToLower(strings.TrimSpace(v)) == lower {
				found = true
				break
			}
		}
		if !found {
			return true // value NOT in allowlist → rule matches (deny)
		}
	}
	return false // all present labels are in their allowlists → rule does NOT match
}

// labelThresholdMatch returns true when ANY label value exceeds its threshold.
// If the label is missing or not a valid number, skip that check (fail-open).
func labelThresholdMatch(thresholds map[string]float64, labels map[string]string) bool {
	for key, maxVal := range thresholds {
		actual, exists := labels[key]
		if !exists {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(actual), 64)
		if err != nil {
			continue // not a number → skip (fail-open)
		}
		if parsed > maxVal {
			return true // exceeds threshold → rule matches (deny)
		}
	}
	return false
}

func containsString(list []string, value string) bool {
	if value == "" {
		return false
	}
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func containsAny(list []string, values []string) bool {
	if len(list) == 0 || len(values) == 0 {
		return false
	}
	for _, v := range values {
		if containsString(list, v) {
			return true
		}
	}
	return false
}

func containsAll(values []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	for _, v := range required {
		if !containsString(values, v) {
			return false
		}
	}
	return true
}

func labelsMatch(required, actual map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	}
	for k, v := range required {
		if actual[k] != v {
			return false
		}
	}
	return true
}

func matchAnyTopic(patterns []string, topic string) bool {
	for _, pat := range patterns {
		if matchTopic(pat, topic) {
			return true
		}
	}
	return false
}

func matchTopic(pattern, topic string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, _ := path.Match(pattern, topic)
	return ok
}

func mcpMatch(policy MCPPolicy, req MCPRequest) bool {
	if !mcpUsed(req) {
		return true
	}
	if ok, _ := matchMCPField("server", req.Server, policy.AllowServers, policy.DenyServers); !ok {
		return false
	}
	if ok, _ := matchMCPField("tool", req.Tool, policy.AllowTools, policy.DenyTools); !ok {
		return false
	}
	if ok, _ := matchMCPField("resource", req.Resource, policy.AllowResources, policy.DenyResources); !ok {
		return false
	}
	if ok, _ := matchMCPField("action", req.Action, policy.AllowActions, policy.DenyActions); !ok {
		return false
	}
	return true
}

// MCPAllowed evaluates an MCP request against allow/deny lists, returning false with a reason when blocked.
func MCPAllowed(policy MCPPolicy, req MCPRequest) (bool, string) {
	if !mcpUsed(req) {
		return true, ""
	}
	if ok, reason := matchMCPField("server", req.Server, policy.AllowServers, policy.DenyServers); !ok {
		return false, reason
	}
	if ok, reason := matchMCPField("tool", req.Tool, policy.AllowTools, policy.DenyTools); !ok {
		return false, reason
	}
	if ok, reason := matchMCPField("resource", req.Resource, policy.AllowResources, policy.DenyResources); !ok {
		return false, reason
	}
	if ok, reason := matchMCPField("action", req.Action, policy.AllowActions, policy.DenyActions); !ok {
		return false, reason
	}
	return true, ""
}

func mcpUsed(req MCPRequest) bool {
	return strings.TrimSpace(req.Server) != "" || strings.TrimSpace(req.Tool) != "" || strings.TrimSpace(req.Resource) != "" || strings.TrimSpace(req.Action) != ""
}

func matchMCPField(field, value string, allow, deny []string) (bool, string) {
	if containsString(deny, value) {
		return false, fmt.Sprintf("mcp %s %q denied", field, value)
	}
	if len(allow) > 0 && !containsString(allow, value) {
		return false, fmt.Sprintf("mcp %s %q not allowed", field, value)
	}
	return true, ""
}
