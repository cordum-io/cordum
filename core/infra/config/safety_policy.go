package config

import (
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

// Evaluate returns decision (allow=true) and reason for the provided tenant/topic.
func (p *SafetyPolicy) Evaluate(tenant, topic string) (bool, string) {
	if p == nil {
		return true, ""
	}
	t := strings.TrimSpace(tenant)
	if t == "" {
		if p.DefaultTenant != "" {
			t = p.DefaultTenant
		} else {
			return false, "missing tenant"
		}
	}
	policy, ok := p.Tenants[t]
	if !ok {
		policy = p.Tenants["default"]
	}

	// deny overrides
	for _, pat := range policy.DenyTopics {
		if matchTopic(pat, topic) {
			return false, "topic denied by policy"
		}
	}
	if len(policy.AllowTopics) > 0 {
		for _, pat := range policy.AllowTopics {
			if matchTopic(pat, topic) {
				return true, ""
			}
		}
		return false, "topic not allowed for tenant"
	}
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
