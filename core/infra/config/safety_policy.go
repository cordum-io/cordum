package config

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// SafetyPolicy defines allow/deny rules per tenant.
type SafetyPolicy struct {
	Version       string                  `yaml:"version"`
	Rules         []PolicyRule            `yaml:"rules"`
	DefaultTenant string                  `yaml:"default_tenant"`
	Tenants       map[string]TenantPolicy `yaml:"tenants"`
}

type PolicyRule struct {
	ID           string              `yaml:"id"`
	Match        PolicyMatch         `yaml:"match"`
	Decision     string              `yaml:"decision"` // allow|deny|require_approval|allow_with_constraints|throttle
	Reason       string              `yaml:"reason"`
	Constraints  PolicyConstraints   `yaml:"constraints"`
	Remediations []PolicyRemediation `yaml:"remediations"`
}

type PolicyMatch struct {
	Tenants        []string          `yaml:"tenants"`
	Topics         []string          `yaml:"topics"`
	Capabilities   []string          `yaml:"capabilities"`
	RiskTags       []string          `yaml:"risk_tags"`
	Requires       []string          `yaml:"requires"`
	PackIDs        []string          `yaml:"pack_ids"`
	ActorIDs       []string          `yaml:"actor_ids"`
	ActorTypes     []string          `yaml:"actor_types"`
	Labels         map[string]string `yaml:"labels"`
	SecretsPresent *bool             `yaml:"secrets_present"`
	MCP            MCPPolicy         `yaml:"mcp"`
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

// MCPPolicy defines allow/deny rules for MCP servers/tools/resources.
type MCPPolicy struct {
	AllowServers   []string `json:"allow_servers" yaml:"allow_servers"`
	DenyServers    []string `json:"deny_servers" yaml:"deny_servers"`
	AllowTools     []string `json:"allow_tools" yaml:"allow_tools"`
	DenyTools      []string `json:"deny_tools" yaml:"deny_tools"`
	AllowResources []string `json:"allow_resources" yaml:"allow_resources"`
	DenyResources  []string `json:"deny_resources" yaml:"deny_resources"`
	AllowActions   []string `json:"allow_actions" yaml:"allow_actions"`
	DenyActions    []string `json:"deny_actions" yaml:"deny_actions"`
}

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
}

// PolicyMeta captures structured job metadata for policy checks.
type PolicyMeta struct {
	ActorID        string
	ActorType      string
	IdempotencyKey string
	Capability     string
	RiskTags       []string
	Requires       []string
	PackID         string
}

// PolicyDecision is the result of policy evaluation.
type PolicyDecision struct {
	Decision         string
	Reason           string
	RuleID           string
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

// LoadSafetyPolicy reads YAML from the given path. If the file is missing or the path is empty, returns nil with no error (allow-all).
func LoadSafetyPolicy(path string) (*SafetyPolicy, error) {
	if path == "" {
		return nil, nil
	}
	// #nosec G304 -- policy path is operator-provided.
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read safety policy %s: %w", path, err)
	}
	policy, err := ParseSafetyPolicy(data)
	if err != nil {
		return nil, fmt.Errorf("parse safety policy %s: %w", path, err)
	}
	return policy, nil
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
	return &policy, nil
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
				Constraints:      rule.Constraints,
				ApprovalRequired: decision == "require_approval",
				Remediations:     rule.Remediations,
			}
		}
	}
	return PolicyDecision{Decision: "allow"}
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
		return "allow"
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
	if match.SecretsPresent != nil && input.SecretsPresent != *match.SecretsPresent {
		return false
	}
	if len(match.Labels) > 0 && !labelsMatch(match.Labels, input.Labels) {
		return false
	}
	if !mcpMatch(match.MCP, input.MCP) {
		return false
	}
	return true
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
