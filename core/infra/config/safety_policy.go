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
	DefaultTenant string                  `yaml:"default_tenant"`
	Tenants       map[string]TenantPolicy `yaml:"tenants"`
}

type TenantPolicy struct {
	AllowTopics      []string `yaml:"allow_topics"`
	DenyTopics       []string `yaml:"deny_topics"`
	AllowedRepoHosts []string `yaml:"allowed_repo_hosts"`
	MaxConcurrent    int      `yaml:"max_concurrent_jobs"`
}

// LoadSafetyPolicy reads YAML from the given path. If the file is missing or the path is empty, returns nil with no error (allow-all).
func LoadSafetyPolicy(path string) (*SafetyPolicy, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var policy SafetyPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return nil, err
	}
	if policy.Tenants == nil {
		policy.Tenants = map[string]TenantPolicy{}
	}
	return &policy, nil
}

// Evaluate returns decision (allow=true) and reason for the provided effective safety config and topic.
func (p *SafetyPolicy) Evaluate(effectiveSafetyConfig SafetyConfig, topic string) (bool, string) {
	// The SafetyPolicy struct itself (p) becomes less relevant if all rules are in effectiveSafetyConfig.
	// However, we can use p.DefaultTenant or other global settings if needed.

	// Deny overrides
	for _, pat := range effectiveSafetyConfig.DeniedTopics {
		if matchTopic(pat, topic) {
			return false, fmt.Sprintf("topic '%s' denied by policy", topic)
		}
	}

	// Allow only if specified
	if len(effectiveSafetyConfig.AllowedTopics) > 0 {
		for _, pat := range effectiveSafetyConfig.AllowedTopics {
			if matchTopic(pat, topic) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("topic '%s' not explicitly allowed by policy", topic)
	}

	// If no explicit allow rules and no deny rules, allow by default
	return true, ""
}

func matchTopic(pattern, topic string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	ok, _ := path.Match(pattern, topic)
	return ok
}
