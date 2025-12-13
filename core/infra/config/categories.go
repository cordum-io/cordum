package config

import "time"

// SafetyConfig - Controls what the AI can/cannot do
type SafetyConfig struct {
    // PII Detection
    PIIDetectionEnabled    bool     `json:"pii_detection_enabled"`
    PIIAction              string   `json:"pii_action"` // "block", "redact", "warn", "log"
    PIITypesToDetect       []string `json:"pii_types"` // "email", "phone", "ssn", etc.
    AllowedEmailDomains    []string `json:"allowed_email_domains"`
    
    // Injection Protection
    InjectionDetection     bool     `json:"injection_detection"`
    InjectionAction        string   `json:"injection_action"`
    InjectionSensitivity   string   `json:"injection_sensitivity"` // "low", "medium", "high"
    
    // Content Filtering
    ContentFilterEnabled   bool     `json:"content_filter_enabled"`
    BlockedCategories      []string `json:"blocked_categories"`
    
    // Anomaly Detection
    AnomalyDetection       bool     `json:"anomaly_detection"`
    AnomalyThresholds      map[string]float64 `json:"anomaly_thresholds"`
    
    // Topic Restrictions
    AllowedTopics          []string `json:"allowed_topics"`
    DeniedTopics           []string `json:"denied_topics"`
}

// BudgetConfig - Controls spending
type BudgetConfig struct {
    // Limits
    DailyLimitUSD          float64  `json:"daily_limit_usd"`
    MonthlyLimitUSD        float64  `json:"monthly_limit_usd"`
    PerJobMaxUSD           float64  `json:"per_job_max_usd"`
    PerWorkflowMaxUSD      float64  `json:"per_workflow_max_usd"`
    
    // Alerts
    AlertAtPercent         []int    `json:"alert_at_percent"` // e.g., [50, 75, 90, 100]
    AlertChannels          []string `json:"alert_channels"` // "email", "slack", "webhook"
    
    // Actions
    ActionAtLimit          string   `json:"action_at_limit"` // "block", "alert_only", "throttle"
    
    // Tracking
    CostAttributionEnabled bool     `json:"cost_attribution_enabled"`
    CostCenters            []string `json:"cost_centers"` // For chargeback
}

// RateLimitConfig - Controls throughput
type RateLimitConfig struct {
    RequestsPerMinute      int      `json:"requests_per_minute"`
    RequestsPerHour        int      `json:"requests_per_hour"`
    BurstSize              int      `json:"burst_size"`
    ConcurrentJobs         int      `json:"concurrent_jobs"`
    ConcurrentWorkflows    int      `json:"concurrent_workflows"`
    QueueSize              int      `json:"queue_size"`
    
    // Per-priority limits
    CriticalJobsPerMinute  int      `json:"critical_jobs_per_minute"`
    InteractiveJobsPerMin  int      `json:"interactive_jobs_per_minute"`
    BatchJobsPerMinute     int      `json:"batch_jobs_per_minute"`
}

// RetryConfig - Controls failure handling
type RetryConfig struct {
    MaxRetries             int           `json:"max_retries"`
    InitialBackoff         time.Duration `json:"initial_backoff"`
    MaxBackoff             time.Duration `json:"max_backoff"`
    BackoffMultiplier      float64       `json:"backoff_multiplier"`
    RetryableErrors        []string      `json:"retryable_errors"`
    NonRetryableErrors     []string      `json:"non_retryable_errors"`
}

// ResourceConfig - Controls resource allocation
type ResourceConfig struct {
    DefaultPriority        string   `json:"default_priority"` // "batch", "interactive", "critical"
    MaxTimeoutSeconds      int      `json:"max_timeout_seconds"`
    DefaultTimeoutSeconds  int      `json:"default_timeout_seconds"`
    MaxParallelSteps       int      `json:"max_parallel_steps"`
    PreemptionEnabled      bool     `json:"preemption_enabled"`
    PreemptionGracePeriod  int      `json:"preemption_grace_period_seconds"`
}

// ModelsConfig - Controls which AI models can be used
type ModelsConfig struct {
    AllowedModels          []string `json:"allowed_models"`
    DefaultModel           string   `json:"default_model"`
    FallbackModels         []string `json:"fallback_models"`
    ModelPreferences       map[string]string `json:"model_preferences"` // topic -> model
    
    // Cost controls per model
    ModelCostLimits        map[string]float64 `json:"model_cost_limits"`
}

// ContextConfig / DataAccessConfig - Controls memory and data access
type ContextConfig struct {
    AllowedMemoryIDs     []string          `json:"allowed_memory_ids"` // "repo:org/*", "kb:support", etc.
    DeniedMemoryIDs      []string          `json:"denied_memory_ids"`
    MaxContextTokens     int               `json:"max_context_tokens"` // per job
    MaxRetrievedChunks   int               `json:"max_retrieved_chunks"`
    CrossTenantAccess    bool              `json:"cross_tenant_access"` // should usually be false
    AllowedConnectors    []string          `json:"allowed_connectors"`  // "github", "slack", "jira"
    RedactionPolicies    map[string]string `json:"redaction_policies"`  // memory_id -> "full|pii_only|none"
}

// ExperimentConfig - Controls A/B testing for ML persona
type ExperimentConfig struct {
    Enabled      bool                `json:"enabled"`
    Name         string              `json:"name"`
    Buckets      []ExperimentBucket  `json:"buckets"`  // A/B/C
}

type ExperimentBucket struct {
    Name        string             `json:"name"`       // "control", "variant-llama3"
    Weight      int                `json:"weight"`     // 0–100
    Model       string             `json:"model"`      // overrides DefaultModel
    ExtraConfig map[string]any     `json:"extra_config"`
}

// IntegrationConfig - Defines policies for external integrations
type IntegrationConfig struct {
    GitHub    IntegrationStatus `json:"github"`
    GitLab    IntegrationStatus `json:"gitlab"`
    Slack     IntegrationStatus `json:"slack"`
    Jira      IntegrationStatus `json:"jira"`
    // ...
}

type IntegrationStatus struct {
    Enabled       bool   `json:"enabled"`
    ConnectionID  string `json:"connection_id"` // references stored secrets in Vault/KMS
    AllowedTeams  []string `json:"allowed_teams"`
    AllowedScopes []string `json:"allowed_scopes"` // "repos:read", "issues:write"
    SecretValue   string `json:"-"` // Resolved secret value (for demonstration only)
}

// SLOConfig - Service Level Objectives per workflow
type SLOConfig struct {
    TargetP95LatencyMs int     `json:"target_p95_latency_ms"`
    ErrorRateBudget    float64 `json:"error_rate_budget"` // e.g., 0.01 for 1%
    TimeoutSeconds     int     `json:"timeout_seconds"`   // hard cap for workflow
    Critical           bool    `json:"critical"`          // page on breach
}
