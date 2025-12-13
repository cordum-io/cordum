package config

import (
	"time"
)

// ConfigScope defines where a config applies
type ConfigScope string

const (
    ScopeSystem   ConfigScope = "system"
    ScopeOrg      ConfigScope = "org"
    ScopeTeam     ConfigScope = "team"
    ScopeWorkflow ConfigScope = "workflow"
    ScopeJob      ConfigScope = "job"
)

// ConfigValue represents a configurable value with inheritance
type ConfigValue struct {
    Key         string      `json:"key"`
    Value       any         `json:"value"`
    Scope       ConfigScope `json:"scope"`
    ScopeID     string      `json:"scope_id"` // org_id, team_id, workflow_id, etc.
    InheritedFrom *ConfigValue `json:"inherited_from,omitempty"`
    OverriddenAt  *ConfigScope `json:"overridden_at,omitempty"`
    Constraints   *Constraints `json:"constraints,omitempty"`
}

// Constraints define what overrides are allowed
type Constraints struct {
    Min         any      `json:"min,omitempty"`
    Max         any      `json:"max,omitempty"`
    AllowedValues []any  `json:"allowed_values,omitempty"`
    Immutable   bool     `json:"immutable"` // Can't be overridden
}

// EffectiveConfig is the resolved config for a specific context
type EffectiveConfig struct {
    // Identity
    OrgID      string `json:"org_id"`
    TeamID     string `json:"team_id"`
    ProjectID  string `json:"project_id"`  // Added from new plan
    WorkflowID string `json:"workflow_id,omitempty"`
    JobID      string `json:"job_id,omitempty"`
    PrincipalID string `json:"principal_id"` // Added from new plan
    
    // Resolved values (after inheritance)
    Safety     SafetyConfig     `json:"safety"`
    Budget     BudgetConfig     `json:"budget"`
    RateLimits RateLimitConfig  `json:"rate_limits"`
    Retry      RetryConfig      `json:"retry"`
    Resources  ResourceConfig   `json:"resources"`
    Models     ModelsConfig     `json:"models"`
    Context    ContextConfig    `json:"context"` // Added from new plan
    Experiment ExperimentConfig `json:"experiment"` // Added from new plan
    Integrations IntegrationConfig `json:"integrations"` // Added from new plan
    SLO        SLOConfig        `json:"slo"`       // Added from new plan
    
    // Audit trail - shows where each value came from
    Sources    map[string]ConfigSource `json:"sources"`
}

type ConfigSource struct {
    Scope   ConfigScope `json:"scope"`
    ScopeID string      `json:"scope_id"`
    SetBy   string      `json:"set_by"`   // User ID
    SetAt   time.Time   `json:"set_at"`
}

// ConfigVersion defines metadata for a versioned configuration.
type ConfigVersion struct {
    ID        string      `json:"id"`
    Scope     ConfigScope `json:"scope"`
    ScopeID   string      `json:"scope_id"`
    Version   int         `json:"version"`
    Status    string      `json:"status"`    // "draft", "active", "deprecated"
    CreatedAt time.Time   `json:"created_at"`
    CreatedBy string      `json:"created_by"`
    Config    EffectiveConfig `json:"config"` // The actual configuration content
}

