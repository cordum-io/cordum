# coretexOS Technical Specification

## 1. System Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                                    CORETEXOS ARCHITECTURE                                │
├─────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                          │
│                                      ┌─────────────┐                                    │
│                                      │   Web UI    │                                    │
│                                      │  (React)    │                                    │
│                                      └──────┬──────┘                                    │
│                                             │                                            │
│                                      ┌──────▼──────┐                                    │
│                                      │ API Gateway │                                    │
│                                      │   (Go/gRPC) │                                    │
│                                      └──────┬──────┘                                    │
│                                             │                                            │
│         ┌───────────────────────────────────┼───────────────────────────────────┐       │
│         │                                   │                                    │       │
│         │                          CONTROL PLANE                                 │       │
│         │                                   │                                    │       │
│         │    ┌──────────────┐    ┌─────────▼────────┐    ┌──────────────┐       │       │
│         │    │   Workflow   │    │    Scheduler     │    │    Safety    │       │       │
│         │    │   Engine     │◄──►│                  │◄──►│    Kernel    │       │       │
│         │    └──────┬───────┘    └────────┬─────────┘    └──────────────┘       │       │
│         │           │                     │                                      │       │
│         │    ┌──────▼───────┐    ┌────────▼─────────┐                           │       │
│         │    │   Config     │    │     Worker       │                           │       │
│         │    │   Service    │    │    Registry      │                           │       │
│         │    └──────────────┘    └──────────────────┘                           │       │
│         │                                                                        │       │
│         └────────────────────────────────────────────────────────────────────────┘       │
│                                             │                                            │
│                                      ┌──────▼──────┐                                    │
│                                      │    NATS     │                                    │
│                                      │  (Message   │                                    │
│                                      │    Bus)     │                                    │
│                                      └──────┬──────┘                                    │
│                                             │                                            │
│         ┌───────────────────────────────────┼───────────────────────────────────┐       │
│         │                                   │                                    │       │
│         │                           DATA PLANE                                   │       │
│         │                                   │                                    │       │
│         │    ┌──────────┐  ┌──────────┐  ┌─▼────────┐  ┌──────────┐            │       │
│         │    │  Worker  │  │  Worker  │  │  Worker  │  │  Worker  │            │       │
│         │    │ Manager  │  │ Manager  │  │ Manager  │  │ Manager  │            │       │
│         │    │ (Docker) │  │  (HTTP)  │  │ (Lambda) │  │ (Script) │            │       │
│         │    └────┬─────┘  └────┬─────┘  └────┬─────┘  └────┬─────┘            │       │
│         │         │             │             │             │                   │       │
│         │    ┌────▼────┐   ┌────▼────┐   ┌────▼────┐   ┌────▼────┐             │       │
│         │    │Container│   │  HTTP   │   │ Lambda  │   │Sandboxed│             │       │
│         │    │  Pool   │   │ Clients │   │ Invoker │   │ Runtime │             │       │
│         │    └─────────┘   └─────────┘   └─────────┘   └─────────┘             │       │
│         │                                                                        │       │
│         └────────────────────────────────────────────────────────────────────────┘       │
│                                                                                          │
│         ┌────────────────────────────────────────────────────────────────────────┐       │
│         │                          STORAGE LAYER                                  │       │
│         │                                                                         │       │
│         │    ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐             │       │
│         │    │  Redis   │  │ Postgres │  │   S3     │  │  Vault   │             │       │
│         │    │ (State)  │  │ (Config) │  │(Artifacts│  │(Secrets) │             │       │
│         │    └──────────┘  └──────────┘  └──────────┘  └──────────┘             │       │
│         │                                                                         │       │
│         └────────────────────────────────────────────────────────────────────────┘       │
│                                                                                          │
└─────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 0. Reality Check: Current State vs. This Spec

| Area                | Today (repo)                                                     | Spec Target                                                    | Gap/Action |
|---------------------|------------------------------------------------------------------|----------------------------------------------------------------|------------|
| Job pipeline        | NATS (core) + Redis job store; scheduler dispatch + workers      | JetStream-capable bus; DLQ ops; richer routing                 | Keep NATS core for now; add DLQ APIs + routing hints; decide on JetStream adoption |
| Workflow engine     | Redis-backed workflows/runs + basic dispatch via workflow engine | DAG runner with steps/step runs, pause/resume                  | Add expression eval, retries/timeouts/approvals; richer routing |
| Persistence         | Redis for job state + workflows/config/DLQ                       | Redis + vector store for state/context; Postgres optional      | Keep Redis as primary; add vector DB for embeddings; defer Postgres |
| Config              | Redis-backed config service (effective merge)                    | Hierarchical config service (system→org→team→workflow→step)    | Use effective config in scheduler/workflow dispatch             |
| Expression eval     | None                                                             | Expression parser + built-ins for conditions/for_each/input    | Add evaluator lib and use in workflow engine                 |
| Workers             | Go binaries (chat, code, repo)                                   | Worker managers (Docker/HTTP/Script), GPU isolation            | Keep current; add manager abstractions incrementally         |
| Safety              | Safety client; decisions stored per job                          | Safety per step/run + approvals handler                        | Extend models + engine; add approval topics/APIs             |
| APIs                | Job submit/list/get/cancel; traces via Redis                     | Workflow CRUD, run tracking, config APIs, approvals, DLQ ops   | Add REST/gRPC endpoints accordingly                          |
| Storage/Secrets     | None beyond Redis                                                | S3 for artifacts; Vault for secrets                            | Defer until engine needs artifacts/secrets                   |

Principle: evolve one system. Add Postgres-backed workflow/config layers and the engine; keep Redis + NATS for job state/dispatch; avoid parallel “half systems”.

## 2. Core Components

### 2.1 Component Responsibilities

| Component | Responsibility | Tech Stack |
|-----------|---------------|------------|
| **API Gateway** | HTTP/gRPC routing, auth, rate limiting | Go, grpc-gateway |
| **Workflow Engine** | DAG execution, step orchestration, state machine | Go |
| **Scheduler** | Job queuing, worker assignment, load balancing | Go |
| **Safety Kernel** | Policy enforcement, PII detection, rate limiting | Go |
| **Config Service** | Hierarchical config, inheritance resolution | Go |
| **Worker Registry** | Worker definitions, health tracking, scaling | Go |
| **Worker Managers** | Execute jobs on specific worker types | Go |
| **NATS** | Message routing, pub/sub, request/reply | NATS JetStream |
| **Redis** | Job state, workflow state, caching | Redis 7+ |
| **Postgres** | Config, workflows, workers, audit logs | Postgres 15+ |
| **S3** | Artifacts, large inputs/outputs | S3-compatible |
| **Vault** | Secrets management | HashiCorp Vault |

---

## 3. Data Models

### 3.1 Core Entities

> Implementation note: Redis remains the primary state store; vector DB for embeddings/semantic context. Postgres is optional and can be added later if we need relational queries.

```go
// core/models/workflow.go

// Workflow is the definition (template)
type Workflow struct {
    ID          string            `json:"id" db:"id"`
    OrgID       string            `json:"org_id" db:"org_id"`
    TeamID      string            `json:"team_id" db:"team_id"`
    Name        string            `json:"name" db:"name"`
    Description string            `json:"description" db:"description"`
    Version     string            `json:"version" db:"version"`
    
    // The workflow definition
    Timeout     time.Duration     `json:"timeout" db:"timeout"`
    Steps       map[string]*Step  `json:"steps" db:"steps"`           // JSON column
    Config      *WorkflowConfig   `json:"config" db:"config"`         // JSON column
    InputSchema *JSONSchema       `json:"input_schema" db:"input_schema"`
    
    // Metadata
    CreatedBy   string            `json:"created_by" db:"created_by"`
    CreatedAt   time.Time         `json:"created_at" db:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at" db:"updated_at"`
    
    // Template info (if saved as template)
    IsTemplate  bool              `json:"is_template" db:"is_template"`
    Visibility  Visibility        `json:"visibility" db:"visibility"`
    Parameters  []Parameter       `json:"parameters" db:"parameters"` // JSON column
}

// Step is a single step in a workflow
type Step struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Type        StepType          `json:"type"`
    
    // Execution
    WorkerID    string            `json:"worker_id,omitempty"`    // For worker type
    Topic       string            `json:"topic,omitempty"`        // For built-in types
    
    // Dependencies & flow
    DependsOn   []string          `json:"depends_on,omitempty"`
    Condition   string            `json:"condition,omitempty"`    // Expression
    
    // Looping
    ForEach     string            `json:"for_each,omitempty"`     // Expression
    MaxParallel int               `json:"max_parallel,omitempty"`
    
    // Input/Output
    Input       map[string]any    `json:"input,omitempty"`        // Can contain expressions
    OutputPath  string            `json:"output_path,omitempty"`  // Where to store output in context
    
    // Error handling
    OnError     string            `json:"on_error,omitempty"`     // Step ID to jump to
    Retry       *RetryConfig      `json:"retry,omitempty"`
    Timeout     time.Duration     `json:"timeout,omitempty"`
    
    // Type-specific config
    LLMConfig      *LLMStepConfig      `json:"llm,omitempty"`
    HTTPConfig     *HTTPStepConfig     `json:"http,omitempty"`
    ApprovalConfig *ApprovalStepConfig `json:"approval,omitempty"`
    ConditionConfig *ConditionStepConfig `json:"condition_config,omitempty"`
    NotifyConfig   *NotifyStepConfig   `json:"notify,omitempty"`
    TransformConfig *TransformStepConfig `json:"transform,omitempty"`
}

type StepType string

const (
    StepTypeLLM       StepType = "llm"
    StepTypeWorker    StepType = "worker"
    StepTypeHTTP      StepType = "http"
    StepTypeContainer StepType = "container"
    StepTypeScript    StepType = "script"
    StepTypeApproval  StepType = "approval"
    StepTypeInput     StepType = "input"
    StepTypeCondition StepType = "condition"
    StepTypeSwitch    StepType = "switch"
    StepTypeParallel  StepType = "parallel"
    StepTypeLoop      StepType = "loop"
    StepTypeDelay     StepType = "delay"
    StepTypeNotify    StepType = "notify"
    StepTypeTransform StepType = "transform"
    StepTypeStorage   StepType = "storage"
    StepTypeSubWorkflow StepType = "subworkflow"
)
```

```go
// core/models/execution.go

// WorkflowRun is a single execution of a workflow
type WorkflowRun struct {
    ID          string            `json:"id" db:"id"`
    WorkflowID  string            `json:"workflow_id" db:"workflow_id"`
    OrgID       string            `json:"org_id" db:"org_id"`
    
    // Input & context
    Input       map[string]any    `json:"input" db:"input"`       // JSON
    Context     map[string]any    `json:"context" db:"context"`   // JSON - shared data between steps
    
    // State
    Status      RunStatus         `json:"status" db:"status"`
    StartedAt   *time.Time        `json:"started_at" db:"started_at"`
    CompletedAt *time.Time        `json:"completed_at" db:"completed_at"`
    
    // Output
    Output      map[string]any    `json:"output" db:"output"`     // JSON
    Error       *RunError         `json:"error" db:"error"`       // JSON
    
    // Steps state
    Steps       map[string]*StepRun `json:"steps" db:"steps"`     // JSON
    
    // Metrics
    TotalCostUSD float64          `json:"total_cost_usd" db:"total_cost_usd"`
    
    // Audit
    TriggeredBy string            `json:"triggered_by" db:"triggered_by"`
    CreatedAt   time.Time         `json:"created_at" db:"created_at"`
}

type RunStatus string

const (
    RunStatusPending   RunStatus = "pending"
    RunStatusRunning   RunStatus = "running"
    RunStatusWaiting   RunStatus = "waiting"   // Waiting for approval/input
    RunStatusSucceeded RunStatus = "succeeded"
    RunStatusFailed    RunStatus = "failed"
    RunStatusCancelled RunStatus = "cancelled"
    RunStatusTimedOut  RunStatus = "timed_out"
)

// StepRun is the execution state of a single step
type StepRun struct {
    StepID      string            `json:"step_id"`
    Status      StepStatus        `json:"status"`
    
    // Timing
    StartedAt   *time.Time        `json:"started_at,omitempty"`
    CompletedAt *time.Time        `json:"completed_at,omitempty"`
    
    // For loops - track iterations
    Iterations  []IterationRun    `json:"iterations,omitempty"`
    
    // Input/Output
    Input       map[string]any    `json:"input,omitempty"`        // Resolved input
    Output      any               `json:"output,omitempty"`
    Error       *StepError        `json:"error,omitempty"`
    
    // Retries
    Attempts    int               `json:"attempts"`
    
    // Job reference (if dispatched to worker)
    JobID       string            `json:"job_id,omitempty"`
    
    // Metrics
    DurationMS  int64             `json:"duration_ms"`
    CostUSD     float64           `json:"cost_usd"`
}

type StepStatus string

const (
    StepStatusPending   StepStatus = "pending"
    StepStatusReady     StepStatus = "ready"      // Dependencies met, waiting to run
    StepStatusRunning   StepStatus = "running"
    StepStatusWaiting   StepStatus = "waiting"    // Human approval/input
    StepStatusSucceeded StepStatus = "succeeded"
    StepStatusFailed    StepStatus = "failed"
    StepStatusSkipped   StepStatus = "skipped"    // Condition was false
    StepStatusCancelled StepStatus = "cancelled"
)

// IterationRun tracks each iteration of a for_each loop
type IterationRun struct {
    Index       int               `json:"index"`
    Key         string            `json:"key,omitempty"`
    Item        any               `json:"item"`
    Status      StepStatus        `json:"status"`
    Output      any               `json:"output,omitempty"`
    Error       *StepError        `json:"error,omitempty"`
    JobID       string            `json:"job_id,omitempty"`
}
```

```go
// core/models/worker.go

// WorkerDefinition is a registered worker
type WorkerDefinition struct {
    ID          string            `json:"id" db:"id"`
    OrgID       string            `json:"org_id" db:"org_id"`
    TeamID      string            `json:"team_id" db:"team_id"`
    
    // Identity
    Name        string            `json:"name" db:"name"`
    Description string            `json:"description" db:"description"`
    Icon        string            `json:"icon" db:"icon"`
    Tags        []string          `json:"tags" db:"tags"`             // JSON array
    
    // Type
    Type        WorkerType        `json:"type" db:"type"`
    
    // Type-specific config (only one is set)
    Docker      *DockerConfig     `json:"docker,omitempty" db:"docker_config"`
    HTTP        *HTTPWorkerConfig `json:"http,omitempty" db:"http_config"`
    Lambda      *LambdaConfig     `json:"lambda,omitempty" db:"lambda_config"`
    Script      *ScriptConfig     `json:"script,omitempty" db:"script_config"`
    
    // Schema
    InputSchema  *JSONSchema      `json:"input_schema" db:"input_schema"`
    OutputSchema *JSONSchema      `json:"output_schema" db:"output_schema"`
    
    // Defaults
    DefaultTimeout time.Duration  `json:"default_timeout" db:"default_timeout"`
    DefaultRetry   *RetryConfig   `json:"default_retry" db:"default_retry"`
    
    // Resources
    Resources   ResourceReqs      `json:"resources" db:"resources"`
    
    // Scaling
    MinInstances int              `json:"min_instances" db:"min_instances"`
    MaxInstances int              `json:"max_instances" db:"max_instances"`
    
    // Visibility
    Visibility  Visibility        `json:"visibility" db:"visibility"`
    
    // Status
    Status      WorkerStatus      `json:"status" db:"status"`
    
    // Audit
    CreatedBy   string            `json:"created_by" db:"created_by"`
    CreatedAt   time.Time         `json:"created_at" db:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at" db:"updated_at"`
}

type WorkerType string

const (
    WorkerTypeDocker  WorkerType = "docker"
    WorkerTypeHTTP    WorkerType = "http"
    WorkerTypeLambda  WorkerType = "lambda"
    WorkerTypeScript  WorkerType = "script"
    WorkerTypeBuiltIn WorkerType = "builtin"
)

type DockerConfig struct {
    Image       string            `json:"image"`
    Registry    *RegistryAuth     `json:"registry,omitempty"`
    Command     []string          `json:"command,omitempty"`
    Args        []string          `json:"args,omitempty"`
    Entrypoint  []string          `json:"entrypoint,omitempty"`
    EnvVars     map[string]string `json:"env_vars,omitempty"`
    Secrets     []SecretRef       `json:"secrets,omitempty"`
    WorkDir     string            `json:"work_dir,omitempty"`
    
    // I/O mode
    InputMode   IOMode            `json:"input_mode"`   // stdin, file, env
    InputPath   string            `json:"input_path,omitempty"`
    OutputMode  IOMode            `json:"output_mode"`  // stdout, file
    OutputPath  string            `json:"output_path,omitempty"`
    
    // Network
    NetworkMode string            `json:"network_mode,omitempty"`
}

type HTTPWorkerConfig struct {
    URL          string            `json:"url"`
    Method       string            `json:"method"`
    Headers      map[string]string `json:"headers,omitempty"`
    
    // Auth
    AuthType     AuthType          `json:"auth_type"`
    AuthConfig   *AuthConfig       `json:"auth_config,omitempty"`
    
    // Request
    BodyTemplate string            `json:"body_template,omitempty"`
    ContentType  string            `json:"content_type,omitempty"`
    
    // Response
    SuccessCodes []int             `json:"success_codes,omitempty"` // Default: [200]
    OutputPath   string            `json:"output_path,omitempty"`   // JSONPath to extract
    
    // Retry
    RetryOn5xx   bool              `json:"retry_on_5xx"`
}

type LambdaConfig struct {
    ARN         string            `json:"arn"`
    Region      string            `json:"region"`
    RoleARN     string            `json:"role_arn,omitempty"`
    InvokeType  string            `json:"invoke_type"` // sync, async
}

type ScriptConfig struct {
    Runtime     Runtime           `json:"runtime"` // python3.11, node18, bash
    Code        string            `json:"code"`
    EntryPoint  string            `json:"entry_point,omitempty"` // Function name
    Dependencies []string         `json:"dependencies,omitempty"`
}

type ResourceReqs struct {
    CPUCores    float64           `json:"cpu_cores"`
    MemoryMB    int               `json:"memory_mb"`
    GPUType     string            `json:"gpu_type,omitempty"`
    GPUCount    int               `json:"gpu_count,omitempty"`
    DiskMB      int               `json:"disk_mb,omitempty"`
    EphemeralMB int               `json:"ephemeral_mb,omitempty"`
}

type SecretRef struct {
    EnvName    string             `json:"env_name"`    // VAR name to inject
    SecretPath string             `json:"secret_path"` // Path in Vault
    SecretKey  string             `json:"secret_key"`  // Key within secret
}
```

```go
// core/models/job.go

// Job is a single unit of work dispatched to a worker
type Job struct {
    ID          string            `json:"id"`
    
    // References
    WorkflowRunID string          `json:"workflow_run_id,omitempty"`
    StepID      string            `json:"step_id,omitempty"`
    IterationIdx *int             `json:"iteration_idx,omitempty"`
    
    // Routing
    OrgID       string            `json:"org_id"`
    WorkerID    string            `json:"worker_id"`  // Which worker definition
    Topic       string            `json:"topic"`      // NATS topic for routing
    
    // Payload
    Input       map[string]any    `json:"input"`
    
    // Config
    Timeout     time.Duration     `json:"timeout"`
    Priority    Priority          `json:"priority"`
    
    // State
    Status      JobStatus         `json:"status"`
    AssignedTo  string            `json:"assigned_to,omitempty"` // Worker instance ID
    
    // Timing
    CreatedAt   time.Time         `json:"created_at"`
    StartedAt   *time.Time        `json:"started_at,omitempty"`
    CompletedAt *time.Time        `json:"completed_at,omitempty"`
    
    // Result
    Output      any               `json:"output,omitempty"`
    Error       *JobError         `json:"error,omitempty"`
    
    // Metrics
    DurationMS  int64             `json:"duration_ms"`
    CostUSD     float64           `json:"cost_usd"`
    
    // Retry state
    Attempt     int               `json:"attempt"`
    MaxRetries  int               `json:"max_retries"`
}

type JobStatus string

const (
    JobStatusPending   JobStatus = "pending"
    JobStatusQueued    JobStatus = "queued"
    JobStatusRunning   JobStatus = "running"
    JobStatusSucceeded JobStatus = "succeeded"
    JobStatusFailed    JobStatus = "failed"
    JobStatusTimedOut  JobStatus = "timed_out"
    JobStatusCancelled JobStatus = "cancelled"
    JobStatusRetrying  JobStatus = "retrying"
)

type Priority int

const (
    PriorityBatch       Priority = 0
    PriorityNormal      Priority = 50
    PriorityInteractive Priority = 75
    PriorityCritical    Priority = 100
)
```

### 3.2 Configuration Models

```go
// core/models/config.go

// EffectiveConfig is the resolved config for a context
type EffectiveConfig struct {
    OrgID       string            `json:"org_id"`
    TeamID      string            `json:"team_id,omitempty"`
    WorkflowID  string            `json:"workflow_id,omitempty"`
    StepID      string            `json:"step_id,omitempty"`
    
    Safety      SafetyConfig      `json:"safety"`
    Budget      BudgetConfig      `json:"budget"`
    RateLimits  RateLimitConfig   `json:"rate_limits"`
    Retry       RetryConfig       `json:"retry"`
    Resources   ResourceConfig    `json:"resources"`
    Models      ModelsConfig      `json:"models"`
    
    // Where each value came from
    Sources     map[string]ConfigSource `json:"sources"`
}

type ConfigSource struct {
    Scope     ConfigScope       `json:"scope"`
    ScopeID   string            `json:"scope_id"`
    SetBy     string            `json:"set_by"`
    SetAt     time.Time         `json:"set_at"`
}

type ConfigScope string

const (
    ConfigScopeSystem   ConfigScope = "system"
    ConfigScopeOrg      ConfigScope = "org"
    ConfigScopeTeam     ConfigScope = "team"
    ConfigScopeWorkflow ConfigScope = "workflow"
    ConfigScopeStep     ConfigScope = "step"
)

type SafetyConfig struct {
    PIIDetectionEnabled  bool              `json:"pii_detection_enabled"`
    PIIAction            string            `json:"pii_action"`
    PIITypes             []string          `json:"pii_types"`
    AllowedEmailDomains  []string          `json:"allowed_email_domains"`
    
    InjectionDetection   bool              `json:"injection_detection"`
    InjectionAction      string            `json:"injection_action"`
    InjectionSensitivity string            `json:"injection_sensitivity"`
    
    AnomalyDetection     bool              `json:"anomaly_detection"`
    
    AllowedWorkers       []string          `json:"allowed_workers"`
    DeniedWorkers        []string          `json:"denied_workers"`
}

type BudgetConfig struct {
    DailyLimitUSD       float64           `json:"daily_limit_usd"`
    MonthlyLimitUSD     float64           `json:"monthly_limit_usd"`
    PerJobMaxUSD        float64           `json:"per_job_max_usd"`
    PerRunMaxUSD        float64           `json:"per_run_max_usd"`
    
    AlertAtPercent      []int             `json:"alert_at_percent"`
    AlertChannels       []string          `json:"alert_channels"`
    ActionAtLimit       string            `json:"action_at_limit"`
}

type RateLimitConfig struct {
    RequestsPerMinute   int               `json:"requests_per_minute"`
    RequestsPerHour     int               `json:"requests_per_hour"`
    BurstSize           int               `json:"burst_size"`
    ConcurrentJobs      int               `json:"concurrent_jobs"`
    ConcurrentRuns      int               `json:"concurrent_runs"`
}

type RetryConfig struct {
    MaxRetries          int               `json:"max_retries"`
    InitialBackoffMS    int               `json:"initial_backoff_ms"`
    MaxBackoffMS        int               `json:"max_backoff_ms"`
    BackoffMultiplier   float64           `json:"backoff_multiplier"`
    RetryableErrors     []string          `json:"retryable_errors"`
}

type ResourceConfig struct {
    DefaultPriority     Priority          `json:"default_priority"`
    DefaultTimeoutSec   int               `json:"default_timeout_sec"`
    MaxTimeoutSec       int               `json:"max_timeout_sec"`
    MaxParallelSteps    int               `json:"max_parallel_steps"`
}

type ModelsConfig struct {
    AllowedModels       []string          `json:"allowed_models"`
    DefaultModel        string            `json:"default_model"`
    FallbackModels      []string          `json:"fallback_models"`
}
```

---

## 4. Integration Plan (Phased, One System)

1) **Data foundation (Week 1-2)**  
   - Define models and Redis schemas for Workflow/WorkflowRun/Step/StepRun/Config/Audit (done).  
   - Add a `core/models` package with structs and Redis persistence helpers.  
   - Keep Redis for job state; add vector DB (e.g., Qdrant/Weaviate) for embeddings when needed.

2) **Config service (Week 2-3)**  
   - Implement hierarchical merge (system→org→team→workflow→step). (done)  
   - Expose gRPC/HTTP get/set endpoints. (done via gateway)  
   - Scheduler pulls effective config; job envs record safety/budget decisions. (done)
   - Routing/cancel: scheduler honors preferred worker/pool labels; publishes cancel packets; DLQ retries rehydrate context before re-dispatch. (done)
   - Safety kernel now accepts `effective_config` (CAP proto updated); client sends it with checks. (done)

3) **Expression evaluator (Week 3-4)**  
   - Parser + built-ins (`length`, `first`, `where`, `json`, math/logic).  
   - Use for conditions/for_each/input mapping.

4) **Workflow engine (Week 4-7)**  
   - DAG runner: persist runs/step runs in Postgres; emit step jobs to NATS; consume results to advance DAG; handle retries/timeouts/approvals.  
   - Topics: workflow events, approvals.  
   - Built-in steps: LLM, HTTP, Approval (timeout).

5) **APIs (Week 5-8)**  
   - Workflow CRUD, start run, get/list runs, step/run state, approvals, DLQ list/retry.  
   - Filters: topic/state/tenant/team/time.

6) **Worker managers (Week 7-10)**  
   - Docker/HTTP/Script abstractions; GPU hinting; reuse existing workers initially.

7) **Artifacts/Secrets (Week 10-14)**  
   - Wire S3 for artifacts; Vault for secrets as needed by steps (optional; defer if Redis/vector suffice).

Throughout: add DLQ ops, job/trace filters, and keep scheduler backward compatible while new layers land.

---

## 5. Workflow Engine

### 4.1 Core Engine

```go
// core/engine/workflow_engine.go

type WorkflowEngine struct {
    store       WorkflowStore
    jobStore    JobStore
    scheduler   Scheduler
    evaluator   ExpressionEvaluator
    configSvc   ConfigService
    notifier    Notifier
    metrics     MetricsRecorder
    
    // Active runs in memory
    activeRuns  sync.Map // runID -> *runState
}

type runState struct {
    run         *WorkflowRun
    workflow    *Workflow
    config      *EffectiveConfig
    cancel      context.CancelFunc
    mu          sync.RWMutex
}

// StartRun begins execution of a workflow
func (e *WorkflowEngine) StartRun(ctx context.Context, req *StartRunRequest) (*WorkflowRun, error) {
    // 1. Load workflow
    workflow, err := e.store.GetWorkflow(ctx, req.WorkflowID)
    if err != nil {
        return nil, fmt.Errorf("load workflow: %w", err)
    }
    
    // 2. Resolve effective config
    config, err := e.configSvc.ResolveConfig(ctx, ConfigContext{
        OrgID:      req.OrgID,
        TeamID:     workflow.TeamID,
        WorkflowID: workflow.ID,
    })
    if err != nil {
        return nil, fmt.Errorf("resolve config: %w", err)
    }
    
    // 3. Validate input against schema
    if workflow.InputSchema != nil {
        if err := e.validateInput(req.Input, workflow.InputSchema); err != nil {
            return nil, fmt.Errorf("invalid input: %w", err)
        }
    }
    
    // 4. Check rate limits
    if err := e.checkRateLimits(ctx, req.OrgID, config.RateLimits); err != nil {
        return nil, err
    }
    
    // 5. Create run
    run := &WorkflowRun{
        ID:          uuid.New().String(),
        WorkflowID:  workflow.ID,
        OrgID:       req.OrgID,
        Input:       req.Input,
        Context:     make(map[string]any),
        Status:      RunStatusPending,
        Steps:       e.initStepStates(workflow.Steps),
        TriggeredBy: req.UserID,
        CreatedAt:   time.Now(),
    }
    
    // 6. Save run
    if err := e.store.CreateRun(ctx, run); err != nil {
        return nil, fmt.Errorf("create run: %w", err)
    }
    
    // 7. Start execution in background
    runCtx, cancel := context.WithTimeout(context.Background(), workflow.Timeout)
    state := &runState{
        run:      run,
        workflow: workflow,
        config:   config,
        cancel:   cancel,
    }
    e.activeRuns.Store(run.ID, state)
    
    go e.executeRun(runCtx, state)
    
    return run, nil
}

// executeRun is the main execution loop
func (e *WorkflowEngine) executeRun(ctx context.Context, state *runState) {
    defer func() {
        e.activeRuns.Delete(state.run.ID)
        state.cancel()
    }()
    
    // Update status to running
    e.updateRunStatus(ctx, state, RunStatusRunning)
    
    for {
        select {
        case <-ctx.Done():
            e.handleRunTimeout(ctx, state)
            return
        default:
        }
        
        // Find ready steps
        readySteps := e.findReadySteps(state)
        
        if len(readySteps) == 0 {
            // Check if we're done or waiting
            if e.isRunComplete(state) {
                e.completeRun(ctx, state)
                return
            }
            if e.isRunWaiting(state) {
                e.updateRunStatus(ctx, state, RunStatusWaiting)
                // Will be resumed when approval/input comes in
                return
            }
            // Still running steps, wait
            time.Sleep(100 * time.Millisecond)
            continue
        }
        
        // Execute ready steps in parallel
        var wg sync.WaitGroup
        for _, stepID := range readySteps {
            wg.Add(1)
            go func(sid string) {
                defer wg.Done()
                e.executeStep(ctx, state, sid)
            }(stepID)
        }
        wg.Wait()
    }
}

// findReadySteps finds steps that can be executed
func (e *WorkflowEngine) findReadySteps(state *runState) []string {
    state.mu.RLock()
    defer state.mu.RUnlock()
    
    var ready []string
    
    for stepID, stepDef := range state.workflow.Steps {
        stepRun := state.run.Steps[stepID]
        
        // Skip if not pending
        if stepRun.Status != StepStatusPending {
            continue
        }
        
        // Check dependencies
        depsOK := true
        for _, depID := range stepDef.DependsOn {
            depRun := state.run.Steps[depID]
            if depRun.Status != StepStatusSucceeded && depRun.Status != StepStatusSkipped {
                depsOK = false
                break
            }
        }
        if !depsOK {
            continue
        }
        
        // Check condition
        if stepDef.Condition != "" {
            result, err := e.evaluator.EvaluateBool(stepDef.Condition, e.buildEvalContext(state))
            if err != nil {
                // Log error, skip step
                continue
            }
            if !result {
                // Condition false, skip step
                e.skipStep(state, stepID)
                continue
            }
        }
        
        ready = append(ready, stepID)
    }
    
    return ready
}

// executeStep executes a single step
func (e *WorkflowEngine) executeStep(ctx context.Context, state *runState, stepID string) {
    stepDef := state.workflow.Steps[stepID]
    
    // Mark as running
    e.updateStepStatus(state, stepID, StepStatusRunning)
    
    // Resolve input expressions
    input, err := e.resolveInput(stepDef.Input, e.buildEvalContext(state))
    if err != nil {
        e.failStep(state, stepID, fmt.Errorf("resolve input: %w", err))
        return
    }
    
    // Check if it's a loop
    if stepDef.ForEach != "" {
        e.executeLoop(ctx, state, stepID, stepDef, input)
        return
    }
    
    // Execute based on type
    var output any
    switch stepDef.Type {
    case StepTypeLLM:
        output, err = e.executeLLMStep(ctx, state, stepDef, input)
    case StepTypeWorker:
        output, err = e.executeWorkerStep(ctx, state, stepDef, input)
    case StepTypeHTTP:
        output, err = e.executeHTTPStep(ctx, state, stepDef, input)
    case StepTypeApproval:
        e.startApprovalStep(ctx, state, stepID, stepDef, input)
        return // Will be completed async
    case StepTypeCondition:
        output, err = e.executeConditionStep(ctx, state, stepDef, input)
    case StepTypeTransform:
        output, err = e.executeTransformStep(ctx, state, stepDef, input)
    case StepTypeNotify:
        output, err = e.executeNotifyStep(ctx, state, stepDef, input)
    case StepTypeDelay:
        output, err = e.executeDelayStep(ctx, state, stepDef, input)
    case StepTypeSubWorkflow:
        output, err = e.executeSubWorkflowStep(ctx, state, stepDef, input)
    default:
        err = fmt.Errorf("unknown step type: %s", stepDef.Type)
    }
    
    if err != nil {
        e.handleStepError(ctx, state, stepID, stepDef, err)
        return
    }
    
    // Store output
    e.completeStep(state, stepID, output)
}

// executeLoop handles for_each steps
func (e *WorkflowEngine) executeLoop(ctx context.Context, state *runState, stepID string, stepDef *Step, input map[string]any) {
    // Evaluate for_each expression to get items
    items, err := e.evaluator.Evaluate(stepDef.ForEach, e.buildEvalContext(state))
    if err != nil {
        e.failStep(state, stepID, fmt.Errorf("evaluate for_each: %w", err))
        return
    }
    
    // Convert to slice
    itemSlice, ok := toSlice(items)
    if !ok {
        e.failStep(state, stepID, fmt.Errorf("for_each must evaluate to array, got %T", items))
        return
    }
    
    // Initialize iterations
    state.mu.Lock()
    stepRun := state.run.Steps[stepID]
    stepRun.Iterations = make([]IterationRun, len(itemSlice))
    for i, item := range itemSlice {
        stepRun.Iterations[i] = IterationRun{
            Index:  i,
            Item:   item,
            Status: StepStatusPending,
        }
    }
    state.mu.Unlock()
    
    // Execute iterations with concurrency limit
    maxParallel := stepDef.MaxParallel
    if maxParallel <= 0 {
        maxParallel = 10 // Default
    }
    sem := make(chan struct{}, maxParallel)
    
    var wg sync.WaitGroup
    var mu sync.Mutex
    var outputs []any
    var firstErr error
    
    for i, item := range itemSlice {
        wg.Add(1)
        sem <- struct{}{} // Acquire
        
        go func(idx int, itm any) {
            defer wg.Done()
            defer func() { <-sem }() // Release
            
            // Build iteration context
            iterCtx := e.buildEvalContext(state)
            iterCtx["_item"] = itm
            iterCtx["_index"] = idx
            
            // Resolve input with iteration context
            iterInput, err := e.resolveInput(stepDef.Input, iterCtx)
            if err != nil {
                mu.Lock()
                if firstErr == nil {
                    firstErr = err
                }
                mu.Unlock()
                e.updateIterationStatus(state, stepID, idx, StepStatusFailed)
                return
            }
            
            // Execute
            output, err := e.executeStepOnce(ctx, state, stepDef, iterInput)
            
            mu.Lock()
            if err != nil {
                if firstErr == nil {
                    firstErr = err
                }
                e.updateIterationStatus(state, stepID, idx, StepStatusFailed)
            } else {
                outputs = append(outputs, output)
                e.updateIterationOutput(state, stepID, idx, output)
                e.updateIterationStatus(state, stepID, idx, StepStatusSucceeded)
            }
            mu.Unlock()
        }(i, item)
    }
    
    wg.Wait()
    
    if firstErr != nil {
        e.failStep(state, stepID, firstErr)
        return
    }
    
    // Complete with all outputs
    e.completeStep(state, stepID, outputs)
}

// executeWorkerStep dispatches work to a registered worker
func (e *WorkflowEngine) executeWorkerStep(ctx context.Context, state *runState, stepDef *Step, input map[string]any) (any, error) {
    // Get worker definition
    worker, err := e.store.GetWorker(ctx, stepDef.WorkerID)
    if err != nil {
        return nil, fmt.Errorf("get worker: %w", err)
    }
    
    // Check if worker is allowed
    if !e.isWorkerAllowed(worker.ID, state.config.Safety) {
        return nil, fmt.Errorf("worker %s not allowed by safety policy", worker.ID)
    }
    
    // Create job
    job := &Job{
        ID:            uuid.New().String(),
        WorkflowRunID: state.run.ID,
        StepID:        stepDef.ID,
        OrgID:         state.run.OrgID,
        WorkerID:      worker.ID,
        Topic:         e.workerTopic(worker),
        Input:         input,
        Timeout:       e.resolveTimeout(stepDef, worker, state.config),
        Priority:      e.resolvePriority(stepDef, state.config),
        Status:        JobStatusPending,
        CreatedAt:     time.Now(),
        MaxRetries:    state.config.Retry.MaxRetries,
    }
    
    // Safety check
    if err := e.safetyCheck(ctx, state, job); err != nil {
        return nil, fmt.Errorf("safety check: %w", err)
    }
    
    // Dispatch job
    result, err := e.scheduler.DispatchAndWait(ctx, job)
    if err != nil {
        return nil, err
    }
    
    return result.Output, nil
}
```

### 4.2 Expression Evaluator

```go
// core/engine/evaluator.go

type ExpressionEvaluator struct {
    parser *ExpressionParser
    funcs  map[string]ExprFunc
}

type EvalContext map[string]any

type ExprFunc func(args ...any) (any, error)

func NewExpressionEvaluator() *ExpressionEvaluator {
    e := &ExpressionEvaluator{
        parser: NewExpressionParser(),
        funcs:  make(map[string]ExprFunc),
    }
    
    // Register built-in functions
    e.registerBuiltins()
    
    return e
}

func (e *ExpressionEvaluator) registerBuiltins() {
    // Array functions
    e.funcs["length"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("length requires 1 argument")
        }
        return getLength(args[0])
    }
    
    e.funcs["first"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("first requires 1 argument")
        }
        return getFirst(args[0])
    }
    
    e.funcs["last"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("last requires 1 argument")
        }
        return getLast(args[0])
    }
    
    e.funcs["slice"] = func(args ...any) (any, error) {
        if len(args) < 2 || len(args) > 3 {
            return nil, errors.New("slice requires 2-3 arguments")
        }
        return sliceArray(args...)
    }
    
    e.funcs["where"] = func(args ...any) (any, error) {
        if len(args) != 3 {
            return nil, errors.New("where requires 3 arguments: array, field, value")
        }
        return filterWhere(args[0], args[1].(string), args[2])
    }
    
    e.funcs["sort_by"] = func(args ...any) (any, error) {
        if len(args) != 2 {
            return nil, errors.New("sort_by requires 2 arguments")
        }
        return sortBy(args[0], args[1].(string))
    }
    
    // String functions
    e.funcs["upper"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("upper requires 1 argument")
        }
        return strings.ToUpper(fmt.Sprint(args[0])), nil
    }
    
    e.funcs["lower"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("lower requires 1 argument")
        }
        return strings.ToLower(fmt.Sprint(args[0])), nil
    }
    
    e.funcs["trim"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("trim requires 1 argument")
        }
        return strings.TrimSpace(fmt.Sprint(args[0])), nil
    }
    
    e.funcs["truncate"] = func(args ...any) (any, error) {
        if len(args) != 2 {
            return nil, errors.New("truncate requires 2 arguments")
        }
        s := fmt.Sprint(args[0])
        n := toInt(args[1])
        if len(s) <= n {
            return s, nil
        }
        return s[:n], nil
    }
    
    e.funcs["split"] = func(args ...any) (any, error) {
        if len(args) != 2 {
            return nil, errors.New("split requires 2 arguments")
        }
        return strings.Split(fmt.Sprint(args[0]), fmt.Sprint(args[1])), nil
    }
    
    e.funcs["join"] = func(args ...any) (any, error) {
        if len(args) != 2 {
            return nil, errors.New("join requires 2 arguments")
        }
        arr, _ := toStringSlice(args[0])
        return strings.Join(arr, fmt.Sprint(args[1])), nil
    }
    
    // JSON functions
    e.funcs["json"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("json requires 1 argument")
        }
        b, err := json.Marshal(args[0])
        return string(b), err
    }
    
    e.funcs["parse_json"] = func(args ...any) (any, error) {
        if len(args) != 1 {
            return nil, errors.New("parse_json requires 1 argument")
        }
        var v any
        err := json.Unmarshal([]byte(fmt.Sprint(args[0])), &v)
        return v, err
    }
    
    // Date functions
    e.funcs["now"] = func(args ...any) (any, error) {
        return time.Now().UTC(), nil
    }
    
    e.funcs["format_date"] = func(args ...any) (any, error) {
        if len(args) != 2 {
            return nil, errors.New("format_date requires 2 arguments")
        }
        t, ok := args[0].(time.Time)
        if !ok {
            return nil, errors.New("first argument must be time")
        }
        return t.Format(fmt.Sprint(args[1])), nil
    }
    
    // Utility functions
    e.funcs["uuid"] = func(args ...any) (any, error) {
        return uuid.New().String(), nil
    }
    
    e.funcs["default"] = func(args ...any) (any, error) {
        if len(args) != 2 {
            return nil, errors.New("default requires 2 arguments")
        }
        if args[0] == nil || args[0] == "" {
            return args[1], nil
        }
        return args[0], nil
    }
    
    e.funcs["coalesce"] = func(args ...any) (any, error) {
        for _, arg := range args {
            if arg != nil && arg != "" {
                return arg, nil
            }
        }
        return nil, nil
    }
}

// Evaluate evaluates an expression and returns the result
func (e *ExpressionEvaluator) Evaluate(expr string, ctx EvalContext) (any, error) {
    // Check if it's a template expression {{ ... }}
    if !strings.Contains(expr, "{{") {
        // Plain value
        return expr, nil
    }
    
    // Parse and evaluate
    ast, err := e.parser.Parse(expr)
    if err != nil {
        return nil, fmt.Errorf("parse expression: %w", err)
    }
    
    return e.eval(ast, ctx)
}

// EvaluateBool evaluates an expression expected to return boolean
func (e *ExpressionEvaluator) EvaluateBool(expr string, ctx EvalContext) (bool, error) {
    result, err := e.Evaluate(expr, ctx)
    if err != nil {
        return false, err
    }
    
    return toBool(result), nil
}

// EvaluateTemplate evaluates a string template with embedded expressions
func (e *ExpressionEvaluator) EvaluateTemplate(template string, ctx EvalContext) (string, error) {
    // Find all {{ ... }} and replace them
    re := regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`)
    
    var evalErr error
    result := re.ReplaceAllStringFunc(template, func(match string) string {
        // Extract expression
        expr := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(match), "}}"), "{{")
        expr = strings.TrimSpace(expr)
        
        // Evaluate
        val, err := e.Evaluate("{{ "+expr+" }}", ctx)
        if err != nil {
            evalErr = err
            return match
        }
        
        return fmt.Sprint(val)
    })
    
    return result, evalErr
}

// eval recursively evaluates an AST node
func (e *ExpressionEvaluator) eval(node ASTNode, ctx EvalContext) (any, error) {
    switch n := node.(type) {
    case *LiteralNode:
        return n.Value, nil
        
    case *IdentifierNode:
        return e.resolveIdentifier(n.Name, ctx)
        
    case *MemberAccessNode:
        obj, err := e.eval(n.Object, ctx)
        if err != nil {
            return nil, err
        }
        return e.accessMember(obj, n.Property)
        
    case *IndexAccessNode:
        obj, err := e.eval(n.Object, ctx)
        if err != nil {
            return nil, err
        }
        idx, err := e.eval(n.Index, ctx)
        if err != nil {
            return nil, err
        }
        return e.accessIndex(obj, idx)
        
    case *FunctionCallNode:
        fn, ok := e.funcs[n.Name]
        if !ok {
            return nil, fmt.Errorf("unknown function: %s", n.Name)
        }
        args := make([]any, len(n.Args))
        for i, arg := range n.Args {
            val, err := e.eval(arg, ctx)
            if err != nil {
                return nil, err
            }
            args[i] = val
        }
        return fn(args...)
        
    case *PipeNode:
        // value | function
        val, err := e.eval(n.Left, ctx)
        if err != nil {
            return nil, err
        }
        fn, ok := e.funcs[n.FuncName]
        if !ok {
            return nil, fmt.Errorf("unknown function: %s", n.FuncName)
        }
        args := []any{val}
        for _, arg := range n.Args {
            v, err := e.eval(arg, ctx)
            if err != nil {
                return nil, err
            }
            args = append(args, v)
        }
        return fn(args...)
        
    case *BinaryOpNode:
        left, err := e.eval(n.Left, ctx)
        if err != nil {
            return nil, err
        }
        right, err := e.eval(n.Right, ctx)
        if err != nil {
            return nil, err
        }
        return e.evalBinaryOp(n.Op, left, right)
        
    case *UnaryOpNode:
        val, err := e.eval(n.Operand, ctx)
        if err != nil {
            return nil, err
        }
        return e.evalUnaryOp(n.Op, val)
        
    case *TernaryNode:
        cond, err := e.eval(n.Condition, ctx)
        if err != nil {
            return nil, err
        }
        if toBool(cond) {
            return e.eval(n.TrueExpr, ctx)
        }
        return e.eval(n.FalseExpr, ctx)
        
    default:
        return nil, fmt.Errorf("unknown node type: %T", node)
    }
}

func (e *ExpressionEvaluator) resolveIdentifier(name string, ctx EvalContext) (any, error) {
    // Check context first
    if val, ok := ctx[name]; ok {
        return val, nil
    }
    
    // Check for dotted path like "steps.planner.output"
    parts := strings.Split(name, ".")
    if len(parts) > 1 {
        current := ctx[parts[0]]
        for _, part := range parts[1:] {
            if current == nil {
                return nil, nil
            }
            var err error
            current, err = e.accessMember(current, part)
            if err != nil {
                return nil, err
            }
        }
        return current, nil
    }
    
    return nil, nil // Unknown identifier returns nil
}

func (e *ExpressionEvaluator) evalBinaryOp(op string, left, right any) (any, error) {
    switch op {
    case "==":
        return reflect.DeepEqual(left, right), nil
    case "!=":
        return !reflect.DeepEqual(left, right), nil
    case ">":
        return toFloat(left) > toFloat(right), nil
    case ">=":
        return toFloat(left) >= toFloat(right), nil
    case "<":
        return toFloat(left) < toFloat(right), nil
    case "<=":
        return toFloat(left) <= toFloat(right), nil
    case "and", "&&":
        return toBool(left) && toBool(right), nil
    case "or", "||":
        return toBool(left) || toBool(right), nil
    case "+":
        if isString(left) || isString(right) {
            return fmt.Sprint(left) + fmt.Sprint(right), nil
        }
        return toFloat(left) + toFloat(right), nil
    case "-":
        return toFloat(left) - toFloat(right), nil
    case "*":
        return toFloat(left) * toFloat(right), nil
    case "/":
        return toFloat(left) / toFloat(right), nil
    case "contains":
        return contains(left, right), nil
    case "matches":
        re, err := regexp.Compile(fmt.Sprint(right))
        if err != nil {
            return false, err
        }
        return re.MatchString(fmt.Sprint(left)), nil
    default:
        return nil, fmt.Errorf("unknown operator: %s", op)
    }
}
```

---

## 5. Worker Execution System

### 5.1 Worker Manager Interface

```go
// core/workers/manager.go

// WorkerManager executes jobs for a specific worker type
type WorkerManager interface {
    // Type returns the worker type this manager handles
    Type() WorkerType
    
    // CanHandle returns true if this manager can handle the worker
    CanHandle(worker *WorkerDefinition) bool
    
    // Execute runs a job and returns the result
    Execute(ctx context.Context, job *Job, worker *WorkerDefinition) (*JobResult, error)
    
    // Validate checks if the worker config is valid
    Validate(worker *WorkerDefinition) error
    
    // Test runs a test execution
    Test(ctx context.Context, worker *WorkerDefinition, input map[string]any) (*TestResult, error)
}

type JobResult struct {
    Output    any           `json:"output"`
    Logs      []LogEntry    `json:"logs,omitempty"`
    Metrics   JobMetrics    `json:"metrics"`
}

type JobMetrics struct {
    DurationMS   int64   `json:"duration_ms"`
    CPUTimeMS    int64   `json:"cpu_time_ms,omitempty"`
    MemoryMB     int     `json:"memory_mb,omitempty"`
    NetworkInMB  float64 `json:"network_in_mb,omitempty"`
    NetworkOutMB float64 `json:"network_out_mb,omitempty"`
    CostUSD      float64 `json:"cost_usd"`
}

type LogEntry struct {
    Timestamp time.Time `json:"timestamp"`
    Level     string    `json:"level"`
    Message   string    `json:"message"`
}
```

### 5.2 Docker Worker Manager

```go
// core/workers/docker_manager.go

type DockerManager struct {
    client      *docker.Client
    registry    *RegistryClient
    secretStore SecretStore
    metrics     MetricsRecorder
    
    // Container pool for reuse
    pool        *ContainerPool
}

func (m *DockerManager) Type() WorkerType {
    return WorkerTypeDocker
}

func (m *DockerManager) Execute(ctx context.Context, job *Job, worker *WorkerDefinition) (*JobResult, error) {
    cfg := worker.Docker
    
    startTime := time.Now()
    
    // 1. Pull image if needed
    if err := m.ensureImage(ctx, cfg.Image, cfg.Registry); err != nil {
        return nil, fmt.Errorf("pull image: %w", err)
    }
    
    // 2. Prepare environment
    env := m.buildEnv(cfg, job)
    
    // 3. Inject secrets
    secretEnv, err := m.resolveSecrets(ctx, cfg.Secrets, job.OrgID)
    if err != nil {
        return nil, fmt.Errorf("resolve secrets: %w", err)
    }
    for k, v := range secretEnv {
        env = append(env, fmt.Sprintf("%s=%s", k, v))
    }
    
    // 4. Prepare input
    inputData, err := json.Marshal(job.Input)
    if err != nil {
        return nil, fmt.Errorf("marshal input: %w", err)
    }
    
    // 5. Create container
    containerCfg := &container.Config{
        Image:      cfg.Image,
        Cmd:        cfg.Command,
        Env:        env,
        WorkingDir: cfg.WorkDir,
        Labels: map[string]string{
            "coretex.job_id":    job.ID,
            "coretex.worker_id": worker.ID,
            "coretex.org_id":    job.OrgID,
        },
    }
    
    hostCfg := &container.HostConfig{
        Resources: container.Resources{
            Memory:   int64(worker.Resources.MemoryMB) * 1024 * 1024,
            NanoCPUs: int64(worker.Resources.CPUCores * 1e9),
        },
        NetworkMode: container.NetworkMode(cfg.NetworkMode),
        AutoRemove:  true,
    }
    
    // Add GPU if needed
    if worker.Resources.GPUCount > 0 {
        hostCfg.DeviceRequests = []container.DeviceRequest{
            {
                Count:        worker.Resources.GPUCount,
                Capabilities: [][]string{{"gpu"}},
            },
        }
    }
    
    resp, err := m.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, "")
    if err != nil {
        return nil, fmt.Errorf("create container: %w", err)
    }
    containerID := resp.ID
    
    defer func() {
        // Cleanup
        m.client.ContainerRemove(context.Background(), containerID, container.RemoveOptions{Force: true})
    }()
    
    // 6. Copy input to container
    if err := m.copyInput(ctx, containerID, cfg, inputData); err != nil {
        return nil, fmt.Errorf("copy input: %w", err)
    }
    
    // 7. Start container
    if err := m.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
        return nil, fmt.Errorf("start container: %w", err)
    }
    
    // 8. Wait for completion
    statusCh, errCh := m.client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
    
    var exitCode int64
    select {
    case err := <-errCh:
        return nil, fmt.Errorf("wait container: %w", err)
    case status := <-statusCh:
        exitCode = status.StatusCode
    case <-ctx.Done():
        // Timeout - kill container
        m.client.ContainerKill(context.Background(), containerID, "SIGKILL")
        return nil, fmt.Errorf("container timeout")
    }
    
    // 9. Collect logs
    logs, err := m.collectLogs(ctx, containerID)
    if err != nil {
        // Log but don't fail
    }
    
    // 10. Collect output
    output, err := m.collectOutput(ctx, containerID, cfg)
    if err != nil {
        return nil, fmt.Errorf("collect output: %w", err)
    }
    
    // 11. Check exit code
    if exitCode != 0 {
        return nil, fmt.Errorf("container exited with code %d: %s", exitCode, string(output))
    }
    
    // 12. Parse output
    var parsedOutput any
    if err := json.Unmarshal(output, &parsedOutput); err != nil {
        // Not JSON, return as string
        parsedOutput = string(output)
    }
    
    duration := time.Since(startTime)
    
    return &JobResult{
        Output: parsedOutput,
        Logs:   logs,
        Metrics: JobMetrics{
            DurationMS: duration.Milliseconds(),
            CostUSD:    m.calculateCost(worker.Resources, duration),
        },
    }, nil
}

func (m *DockerManager) copyInput(ctx context.Context, containerID string, cfg *DockerConfig, input []byte) error {
    switch cfg.InputMode {
    case IOModeStdin:
        // Will be provided via attach
        return nil
    case IOModeFile:
        // Create tar with input file
        var buf bytes.Buffer
        tw := tar.NewWriter(&buf)
        tw.WriteHeader(&tar.Header{
            Name: cfg.InputPath,
            Mode: 0644,
            Size: int64(len(input)),
        })
        tw.Write(input)
        tw.Close()
        return m.client.CopyToContainer(ctx, containerID, "/", &buf, container.CopyToContainerOptions{})
    case IOModeEnv:
        // Already in env
        return nil
    default:
        return fmt.Errorf("unknown input mode: %s", cfg.InputMode)
    }
}

func (m *DockerManager) collectOutput(ctx context.Context, containerID string, cfg *DockerConfig) ([]byte, error) {
    switch cfg.OutputMode {
    case IOModeStdout:
        logs, err := m.client.ContainerLogs(ctx, containerID, container.LogsOptions{
            ShowStdout: true,
        })
        if err != nil {
            return nil, err
        }
        defer logs.Close()
        return io.ReadAll(logs)
        
    case IOModeFile:
        reader, _, err := m.client.CopyFromContainer(ctx, containerID, cfg.OutputPath)
        if err != nil {
            return nil, err
        }
        defer reader.Close()
        
        // Extract from tar
        tr := tar.NewReader(reader)
        if _, err := tr.Next(); err != nil {
            return nil, err
        }
        return io.ReadAll(tr)
        
    default:
        return nil, fmt.Errorf("unknown output mode: %s", cfg.OutputMode)
    }
}
```

### 5.3 HTTP Worker Manager

```go
// core/workers/http_manager.go

type HTTPManager struct {
    client      *http.Client
    secretStore SecretStore
    evaluator   *ExpressionEvaluator
}

func (m *HTTPManager) Type() WorkerType {
    return WorkerTypeHTTP
}

func (m *HTTPManager) Execute(ctx context.Context, job *Job, worker *WorkerDefinition) (*JobResult, error) {
    cfg := worker.HTTP
    startTime := time.Now()
    
    // 1. Build request URL
    url, err := m.evaluator.EvaluateTemplate(cfg.URL, EvalContext{"input": job.Input})
    if err != nil {
        return nil, fmt.Errorf("evaluate URL: %w", err)
    }
    
    // 2. Build request body
    var body io.Reader
    if cfg.BodyTemplate != "" {
        bodyStr, err := m.evaluator.EvaluateTemplate(cfg.BodyTemplate, EvalContext{"input": job.Input})
        if err != nil {
            return nil, fmt.Errorf("evaluate body: %w", err)
        }
        body = strings.NewReader(bodyStr)
    } else if cfg.Method == "POST" || cfg.Method == "PUT" || cfg.Method == "PATCH" {
        bodyBytes, _ := json.Marshal(job.Input)
        body = bytes.NewReader(bodyBytes)
    }
    
    // 3. Create request
    req, err := http.NewRequestWithContext(ctx, cfg.Method, url, body)
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }
    
    // 4. Set headers
    for k, v := range cfg.Headers {
        val, _ := m.evaluator.EvaluateTemplate(v, EvalContext{"input": job.Input})
        req.Header.Set(k, val)
    }
    if cfg.ContentType != "" {
        req.Header.Set("Content-Type", cfg.ContentType)
    } else if body != nil {
        req.Header.Set("Content-Type", "application/json")
    }
    
    // 5. Apply auth
    if err := m.applyAuth(ctx, req, cfg, job.OrgID); err != nil {
        return nil, fmt.Errorf("apply auth: %w", err)
    }
    
    // 6. Execute request
    resp, err := m.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("execute request: %w", err)
    }
    defer resp.Body.Close()
    
    // 7. Read response
    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("read response: %w", err)
    }
    
    // 8. Check status code
    successCodes := cfg.SuccessCodes
    if len(successCodes) == 0 {
        successCodes = []int{200, 201, 202, 204}
    }
    if !containsInt(successCodes, resp.StatusCode) {
        return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
    }
    
    // 9. Parse response
    var output any
    if err := json.Unmarshal(respBody, &output); err != nil {
        output = string(respBody)
    }
    
    // 10. Extract output if path specified
    if cfg.OutputPath != "" {
        output, _ = jsonpath.Get(cfg.OutputPath, output)
    }
    
    duration := time.Since(startTime)
    
    return &JobResult{
        Output: output,
        Metrics: JobMetrics{
            DurationMS: duration.Milliseconds(),
            CostUSD:    0, // HTTP calls are free (user's endpoint)
        },
    }, nil
}

func (m *HTTPManager) applyAuth(ctx context.Context, req *http.Request, cfg *HTTPWorkerConfig, orgID string) error {
    switch cfg.AuthType {
    case AuthTypeNone:
        return nil
        
    case AuthTypeBearer:
        token, err := m.secretStore.Get(ctx, orgID, cfg.AuthConfig.SecretPath)
        if err != nil {
            return err
        }
        req.Header.Set("Authorization", "Bearer "+token)
        
    case AuthTypeBasic:
        username, _ := m.secretStore.Get(ctx, orgID, cfg.AuthConfig.UsernamePath)
        password, _ := m.secretStore.Get(ctx, orgID, cfg.AuthConfig.PasswordPath)
        req.SetBasicAuth(username, password)
        
    case AuthTypeAPIKey:
        key, err := m.secretStore.Get(ctx, orgID, cfg.AuthConfig.SecretPath)
        if err != nil {
            return err
        }
        if cfg.AuthConfig.HeaderName != "" {
            req.Header.Set(cfg.AuthConfig.HeaderName, key)
        } else {
            req.Header.Set("X-API-Key", key)
        }
        
    default:
        return fmt.Errorf("unknown auth type: %s", cfg.AuthType)
    }
    
    return nil
}
```

### 5.4 Script Worker Manager

```go
// core/workers/script_manager.go

type ScriptManager struct {
    runtimes map[Runtime]*RuntimeConfig
}

type RuntimeConfig struct {
    Image          string
    Command        []string
    InstallCmd     []string // Command to install dependencies
    EntrypointTmpl string   // Template for wrapping user code
}

func NewScriptManager() *ScriptManager {
    return &ScriptManager{
        runtimes: map[Runtime]*RuntimeConfig{
            RuntimePython311: {
                Image:      "python:3.11-slim",
                Command:    []string{"python", "-c"},
                InstallCmd: []string{"pip", "install", "-q"},
                EntrypointTmpl: `
import json
import sys

# User code
{{CODE}}

# Execute
if __name__ == "__main__":
    input_data = json.loads(sys.stdin.read())
    result = {{ENTRYPOINT}}(input_data)
    print(json.dumps(result))
`,
            },
            RuntimeNode18: {
                Image:      "node:18-slim",
                Command:    []string{"node", "-e"},
                InstallCmd: []string{"npm", "install", "-q"},
                EntrypointTmpl: `
const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));

// User code
{{CODE}}

// Execute
const result = {{ENTRYPOINT}}(input);
console.log(JSON.stringify(result));
`,
            },
            RuntimeBash: {
                Image:   "alpine:3.18",
                Command: []string{"/bin/sh", "-c"},
                EntrypointTmpl: `
{{CODE}}
`,
            },
        },
    }
}

func (m *ScriptManager) Execute(ctx context.Context, job *Job, worker *WorkerDefinition) (*JobResult, error) {
    cfg := worker.Script
    runtime := m.runtimes[cfg.Runtime]
    if runtime == nil {
        return nil, fmt.Errorf("unsupported runtime: %s", cfg.Runtime)
    }
    
    // Build the wrapper script
    entrypoint := cfg.EntryPoint
    if entrypoint == "" {
        entrypoint = "main"
    }
    
    script := strings.ReplaceAll(runtime.EntrypointTmpl, "{{CODE}}", cfg.Code)
    script = strings.ReplaceAll(script, "{{ENTRYPOINT}}", entrypoint)
    
    // Create a temporary Docker worker
    dockerCfg := &DockerConfig{
        Image:      runtime.Image,
        Command:    append(runtime.Command, script),
        InputMode:  IOModeStdin,
        OutputMode: IOModeStdout,
    }
    
    // Install dependencies if any
    if len(cfg.Dependencies) > 0 {
        installCmd := append(runtime.InstallCmd, cfg.Dependencies...)
        dockerCfg.Command = []string{"/bin/sh", "-c", 
            strings.Join(installCmd, " ") + " && " + strings.Join(append(runtime.Command, script), " ")}
    }
    
    tempWorker := &WorkerDefinition{
        Type:      WorkerTypeDocker,
        Docker:    dockerCfg,
        Resources: worker.Resources,
    }
    
    // Execute via Docker manager
    dockerMgr := &DockerManager{} // Would be injected in real impl
    return dockerMgr.Execute(ctx, job, tempWorker)
}
```

---

## 6. Built-in Step Handlers

### 6.1 LLM Step Handler

```go
// core/steps/llm_handler.go

type LLMHandler struct {
    providers map[string]LLMProvider
    configSvc ConfigService
}

type LLMProvider interface {
    Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
    EstimateCost(model string, inputTokens, outputTokens int) float64
}

type LLMRequest struct {
    Model       string
    Messages    []Message
    Temperature float64
    MaxTokens   int
    Tools       []Tool
}

type LLMResponse struct {
    Content     string
    ToolCalls   []ToolCall
    Usage       TokenUsage
}

func (h *LLMHandler) Execute(ctx context.Context, config *LLMStepConfig, input map[string]any, evalCtx EvalContext) (any, error) {
    // 1. Get effective config
    modelsConfig, _ := h.configSvc.GetModelsConfig(ctx, evalCtx["org_id"].(string))
    
    // 2. Determine model
    model := config.Model
    if model == "" {
        model = modelsConfig.DefaultModel
    }
    
    // Check if allowed
    if len(modelsConfig.AllowedModels) > 0 && !contains(modelsConfig.AllowedModels, model) {
        return nil, fmt.Errorf("model %s not allowed", model)
    }
    
    // 3. Build messages
    messages := []Message{}
    
    if config.SystemPrompt != "" {
        prompt, _ := evaluateTemplate(config.SystemPrompt, evalCtx)
        messages = append(messages, Message{Role: "system", Content: prompt})
    }
    
    if config.Prompt != "" {
        prompt, _ := evaluateTemplate(config.Prompt, evalCtx)
        messages = append(messages, Message{Role: "user", Content: prompt})
    }
    
    if config.Messages != nil {
        for _, m := range config.Messages {
            content, _ := evaluateTemplate(m.Content, evalCtx)
            messages = append(messages, Message{Role: m.Role, Content: content})
        }
    }
    
    // 4. Get provider
    provider := h.getProvider(model)
    if provider == nil {
        return nil, fmt.Errorf("no provider for model %s", model)
    }
    
    // 5. Execute
    resp, err := provider.Complete(ctx, &LLMRequest{
        Model:       model,
        Messages:    messages,
        Temperature: config.Temperature,
        MaxTokens:   config.MaxTokens,
        Tools:       config.Tools,
    })
    if err != nil {
        // Try fallback models
        for _, fallback := range modelsConfig.FallbackModels {
            provider = h.getProvider(fallback)
            if provider != nil {
                resp, err = provider.Complete(ctx, &LLMRequest{
                    Model:       fallback,
                    Messages:    messages,
                    Temperature: config.Temperature,
                    MaxTokens:   config.MaxTokens,
                })
                if err == nil {
                    break
                }
            }
        }
        if err != nil {
            return nil, err
        }
    }
    
    // 6. Build output
    output := map[string]any{
        "content":   resp.Content,
        "model":     model,
        "usage":     resp.Usage,
        "cost_usd":  provider.EstimateCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens),
    }
    
    if len(resp.ToolCalls) > 0 {
        output["tool_calls"] = resp.ToolCalls
    }
    
    return output, nil
}
```

### 6.2 Approval Step Handler

```go
// core/steps/approval_handler.go

type ApprovalHandler struct {
    store       ApprovalStore
    notifier    Notifier
    evaluator   *ExpressionEvaluator
}

type ApprovalRequest struct {
    ID          string
    WorkflowRunID string
    StepID      string
    OrgID       string
    
    Title       string
    Message     string
    Data        map[string]any // Data to show reviewer
    
    Approvers   []Approver
    Timeout     time.Duration
    TimeoutAction string // "approve", "reject", "skip"
    
    Status      ApprovalStatus
    Decision    *ApprovalDecision
    
    CreatedAt   time.Time
    ExpiresAt   time.Time
}

type Approver struct {
    Type  string // "user", "team", "role"
    Value string // user email, team ID, role name
}

type ApprovalDecision struct {
    Decision  string    // "approved", "rejected", "request_changes"
    Comment   string
    DecidedBy string
    DecidedAt time.Time
}

func (h *ApprovalHandler) Start(ctx context.Context, config *ApprovalStepConfig, input map[string]any, evalCtx EvalContext) (*ApprovalRequest, error) {
    // 1. Evaluate message template
    message, _ := h.evaluator.EvaluateTemplate(config.Message, evalCtx)
    
    // 2. Create approval request
    req := &ApprovalRequest{
        ID:            uuid.New().String(),
        WorkflowRunID: evalCtx["workflow_run_id"].(string),
        StepID:        evalCtx["step_id"].(string),
        OrgID:         evalCtx["org_id"].(string),
        Title:         config.Title,
        Message:       message,
        Data:          input,
        Approvers:     config.Approvers,
        Timeout:       config.Timeout,
        TimeoutAction: config.TimeoutAction,
        Status:        ApprovalStatusPending,
        CreatedAt:     time.Now(),
        ExpiresAt:     time.Now().Add(config.Timeout),
    }
    
    // 3. Save
    if err := h.store.Create(ctx, req); err != nil {
        return nil, err
    }
    
    // 4. Notify approvers
    for _, approver := range config.Approvers {
        h.notifier.NotifyApprovalRequired(ctx, approver, req)
    }
    
    // 5. Schedule timeout
    go h.scheduleTimeout(req)
    
    return req, nil
}

func (h *ApprovalHandler) Decide(ctx context.Context, requestID string, decision *ApprovalDecision) error {
    // 1. Get request
    req, err := h.store.Get(ctx, requestID)
    if err != nil {
        return err
    }
    
    // 2. Check if already decided
    if req.Status != ApprovalStatusPending {
        return fmt.Errorf("approval already %s", req.Status)
    }
    
    // 3. Verify decider is allowed
    if !h.canDecide(decision.DecidedBy, req.Approvers) {
        return fmt.Errorf("user not authorized to decide")
    }
    
    // 4. Update
    req.Status = ApprovalStatus(decision.Decision)
    req.Decision = decision
    
    if err := h.store.Update(ctx, req); err != nil {
        return err
    }
    
    // 5. Resume workflow
    h.resumeWorkflow(ctx, req)
    
    return nil
}

func (h *ApprovalHandler) scheduleTimeout(req *ApprovalRequest) {
    time.Sleep(time.Until(req.ExpiresAt))
    
    // Check if still pending
    current, err := h.store.Get(context.Background(), req.ID)
    if err != nil || current.Status != ApprovalStatusPending {
        return
    }
    
    // Apply timeout action
    decision := &ApprovalDecision{
        Decision:  req.TimeoutAction,
        Comment:   "Auto-decided due to timeout",
        DecidedBy: "system",
        DecidedAt: time.Now(),
    }
    
    h.Decide(context.Background(), req.ID, decision)
}
```

---

## 7. Configuration Service

```go
// core/config/service.go

type ConfigService struct {
    store ConfigStore
    cache *cache.Cache
}

// ResolveConfig resolves the effective config for a context by merging
// system -> org -> team -> workflow -> step configs
func (s *ConfigService) ResolveConfig(ctx context.Context, c ConfigContext) (*EffectiveConfig, error) {
    // 1. Load all applicable configs
    configs := []ConfigLayer{}
    
    // System defaults
    systemCfg, _ := s.store.GetSystemConfig(ctx)
    if systemCfg != nil {
        configs = append(configs, ConfigLayer{Scope: ConfigScopeSystem, Config: systemCfg})
    }
    
    // Org config
    if c.OrgID != "" {
        orgCfg, _ := s.store.GetOrgConfig(ctx, c.OrgID)
        if orgCfg != nil {
            configs = append(configs, ConfigLayer{Scope: ConfigScopeOrg, ScopeID: c.OrgID, Config: orgCfg})
        }
    }
    
    // Team config
    if c.TeamID != "" {
        teamCfg, _ := s.store.GetTeamConfig(ctx, c.TeamID)
        if teamCfg != nil {
            configs = append(configs, ConfigLayer{Scope: ConfigScopeTeam, ScopeID: c.TeamID, Config: teamCfg})
        }
    }
    
    // Workflow config
    if c.WorkflowID != "" {
        wfCfg, _ := s.store.GetWorkflowConfig(ctx, c.WorkflowID)
        if wfCfg != nil {
            configs = append(configs, ConfigLayer{Scope: ConfigScopeWorkflow, ScopeID: c.WorkflowID, Config: wfCfg})
        }
    }
    
    // Step config
    if c.StepID != "" {
        stepCfg, _ := s.store.GetStepConfig(ctx, c.WorkflowID, c.StepID)
        if stepCfg != nil {
            configs = append(configs, ConfigLayer{Scope: ConfigScopeStep, ScopeID: c.StepID, Config: stepCfg})
        }
    }
    
    // 2. Merge configs
    return s.mergeConfigs(configs)
}

func (s *ConfigService) mergeConfigs(layers []ConfigLayer) (*EffectiveConfig, error) {
    result := &EffectiveConfig{
        Sources: make(map[string]ConfigSource),
    }
    
    // Start with defaults
    result.Safety = DefaultSafetyConfig()
    result.Budget = DefaultBudgetConfig()
    result.RateLimits = DefaultRateLimitConfig()
    result.Retry = DefaultRetryConfig()
    result.Resources = DefaultResourceConfig()
    result.Models = DefaultModelsConfig()
    
    // Apply each layer
    for _, layer := range layers {
        s.applyLayer(result, layer)
    }
    
    return result, nil
}

func (s *ConfigService) applyLayer(result *EffectiveConfig, layer ConfigLayer) {
    cfg := layer.Config
    source := ConfigSource{
        Scope:   layer.Scope,
        ScopeID: layer.ScopeID,
    }
    
    // Safety config
    if cfg.Safety != nil {
        if cfg.Safety.PIIDetectionEnabled != nil {
            result.Safety.PIIDetectionEnabled = *cfg.Safety.PIIDetectionEnabled
            result.Sources["safety.pii_detection_enabled"] = source
        }
        if cfg.Safety.PIIAction != "" {
            result.Safety.PIIAction = cfg.Safety.PIIAction
            result.Sources["safety.pii_action"] = source
        }
        // ... other safety fields
    }
    
    // Budget config - can only be MORE restrictive at lower levels
    if cfg.Budget != nil {
        if cfg.Budget.DailyLimitUSD != nil {
            if *cfg.Budget.DailyLimitUSD < result.Budget.DailyLimitUSD || result.Budget.DailyLimitUSD == 0 {
                result.Budget.DailyLimitUSD = *cfg.Budget.DailyLimitUSD
                result.Sources["budget.daily_limit_usd"] = source
            }
        }
        if cfg.Budget.MonthlyLimitUSD != nil {
            if *cfg.Budget.MonthlyLimitUSD < result.Budget.MonthlyLimitUSD || result.Budget.MonthlyLimitUSD == 0 {
                result.Budget.MonthlyLimitUSD = *cfg.Budget.MonthlyLimitUSD
                result.Sources["budget.monthly_limit_usd"] = source
            }
        }
        // ... other budget fields
    }
    
    // Rate limits - can only be MORE restrictive
    if cfg.RateLimits != nil {
        if cfg.RateLimits.RequestsPerMinute != nil {
            if *cfg.RateLimits.RequestsPerMinute < result.RateLimits.RequestsPerMinute {
                result.RateLimits.RequestsPerMinute = *cfg.RateLimits.RequestsPerMinute
                result.Sources["rate_limits.requests_per_minute"] = source
            }
        }
        // ... other rate limit fields
    }
    
    // Retry config - can be overridden freely
    if cfg.Retry != nil {
        if cfg.Retry.MaxRetries != nil {
            result.Retry.MaxRetries = *cfg.Retry.MaxRetries
            result.Sources["retry.max_retries"] = source
        }
        // ... other retry fields
    }
}
```

---

## 8. API Design

### 8.1 REST API Endpoints

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              API ENDPOINTS                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  WORKFLOWS                                                                  │
│  ─────────                                                                  │
│  POST   /api/v1/workflows                    Create workflow                │
│  GET    /api/v1/workflows                    List workflows                 │
│  GET    /api/v1/workflows/:id                Get workflow                   │
│  PUT    /api/v1/workflows/:id                Update workflow                │
│  DELETE /api/v1/workflows/:id                Delete workflow                │
│  POST   /api/v1/workflows/:id/run            Start workflow run             │
│  POST   /api/v1/workflows/:id/validate       Validate workflow              │
│                                                                              │
│  WORKFLOW RUNS                                                              │
│  ─────────────                                                              │
│  GET    /api/v1/runs                         List runs                      │
│  GET    /api/v1/runs/:id                     Get run                        │
│  GET    /api/v1/runs/:id/steps               Get run steps                  │
│  GET    /api/v1/runs/:id/logs                Get run logs                   │
│  POST   /api/v1/runs/:id/cancel              Cancel run                     │
│  POST   /api/v1/runs/:id/retry               Retry failed run               │
│                                                                              │
│  WORKERS                                                                    │
│  ───────                                                                    │
│  POST   /api/v1/workers                      Register worker                │
│  GET    /api/v1/workers                      List workers                   │
│  GET    /api/v1/workers/:id                  Get worker                     │
│  PUT    /api/v1/workers/:id                  Update worker                  │
│  DELETE /api/v1/workers/:id                  Delete worker                  │
│  POST   /api/v1/workers/:id/test             Test worker                    │
│  GET    /api/v1/workers/:id/stats            Get worker stats               │
│                                                                              │
│  JOBS                                                                       │
│  ────                                                                       │
│  GET    /api/v1/jobs                         List jobs                      │
│  GET    /api/v1/jobs/:id                     Get job                        │
│  GET    /api/v1/jobs/:id/logs                Get job logs                   │
│  POST   /api/v1/jobs/:id/cancel              Cancel job                     │
│                                                                              │
│  APPROVALS                                                                  │
│  ─────────                                                                  │
│  GET    /api/v1/approvals                    List pending approvals         │
│  GET    /api/v1/approvals/:id                Get approval                   │
│  POST   /api/v1/approvals/:id/decide         Approve/reject                 │
│                                                                              │
│  TEMPLATES                                                                  │
│  ─────────                                                                  │
│  GET    /api/v1/templates                    List templates                 │
│  GET    /api/v1/templates/:id                Get template                   │
│  POST   /api/v1/templates                    Create template                │
│  POST   /api/v1/templates/:id/use            Create workflow from template  │
│                                                                              │
│  CONFIG                                                                     │
│  ──────                                                                     │
│  GET    /api/v1/config/org/:orgId            Get org config                 │
│  PUT    /api/v1/config/org/:orgId            Update org config              │
│  GET    /api/v1/config/team/:teamId          Get team config                │
│  PUT    /api/v1/config/team/:teamId          Update team config             │
│  GET    /api/v1/config/effective             Get effective config           │
│                                                                              │
│  ANALYTICS                                                                  │
│  ─────────                                                                  │
│  GET    /api/v1/analytics/costs              Get cost analytics             │
│  GET    /api/v1/analytics/usage              Get usage analytics            │
│  GET    /api/v1/analytics/performance        Get performance metrics        │
│                                                                              │
│  AUDIT                                                                      │
│  ─────                                                                      │
│  GET    /api/v1/audit/logs                   Get audit logs                 │
│                                                                              │
│  WEBSOCKET                                                                  │
│  ─────────                                                                  │
│  WS     /api/v1/ws/runs/:id                  Stream run updates             │
│  WS     /api/v1/ws/jobs/:id                  Stream job updates             │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Key Request/Response Types

```go
// api/types.go

// Workflow endpoints
type CreateWorkflowRequest struct {
    Name        string            `json:"name" validate:"required"`
    Description string            `json:"description"`
    Timeout     string            `json:"timeout"` // "30m", "1h"
    Steps       map[string]*Step  `json:"steps" validate:"required"`
    Config      *WorkflowConfig   `json:"config,omitempty"`
    InputSchema *JSONSchema       `json:"input_schema,omitempty"`
}

type StartRunRequest struct {
    Input       map[string]any    `json:"input"`
    Priority    string            `json:"priority,omitempty"`
    CallbackURL string            `json:"callback_url,omitempty"`
}

type RunResponse struct {
    ID          string            `json:"id"`
    WorkflowID  string            `json:"workflow_id"`
    Status      string            `json:"status"`
    Input       map[string]any    `json:"input"`
    Output      map[string]any    `json:"output,omitempty"`
    Error       *RunError         `json:"error,omitempty"`
    Steps       map[string]*StepRunResponse `json:"steps"`
    StartedAt   *time.Time        `json:"started_at,omitempty"`
    CompletedAt *time.Time        `json:"completed_at,omitempty"`
    TotalCostUSD float64          `json:"total_cost_usd"`
}

// Worker endpoints
type RegisterWorkerRequest struct {
    Name        string            `json:"name" validate:"required"`
    Description string            `json:"description"`
    Type        string            `json:"type" validate:"required,oneof=docker http lambda script"`
    Docker      *DockerConfig     `json:"docker,omitempty"`
    HTTP        *HTTPWorkerConfig `json:"http,omitempty"`
    Lambda      *LambdaConfig     `json:"lambda,omitempty"`
    Script      *ScriptConfig     `json:"script,omitempty"`
    InputSchema *JSONSchema       `json:"input_schema,omitempty"`
    OutputSchema *JSONSchema      `json:"output_schema,omitempty"`
    Resources   *ResourceReqs     `json:"resources,omitempty"`
    Visibility  string            `json:"visibility,omitempty"`
}

type TestWorkerRequest struct {
    Input       map[string]any    `json:"input" validate:"required"`
}

type TestWorkerResponse struct {
    Success     bool              `json:"success"`
    Output      any               `json:"output,omitempty"`
    Error       string            `json:"error,omitempty"`
    DurationMS  int64             `json:"duration_ms"`
    Logs        []LogEntry        `json:"logs,omitempty"`
}

// Approval endpoints
type DecideApprovalRequest struct {
    Decision    string            `json:"decision" validate:"required,oneof=approved rejected request_changes"`
    Comment     string            `json:"comment,omitempty"`
}
```

---

## 9. NATS Topic Structure

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            NATS TOPICS                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  JOB DISPATCH (by worker type)                                              │
│  ─────────────────────────────                                              │
│  jobs.docker.{worker_id}         Jobs for specific Docker worker            │
│  jobs.http.{worker_id}           Jobs for specific HTTP worker              │
│  jobs.lambda.{worker_id}         Jobs for specific Lambda worker            │
│  jobs.script.{worker_id}         Jobs for specific Script worker            │
│  jobs.builtin.llm                Jobs for built-in LLM handler              │
│  jobs.builtin.notify             Jobs for notification handler              │
│                                                                              │
│  JOB RESULTS                                                                │
│  ───────────                                                                │
│  results.{job_id}                Result for specific job                    │
│                                                                              │
│  WORKFLOW EVENTS                                                            │
│  ───────────────                                                            │
│  workflow.run.started            Workflow run started                       │
│  workflow.run.completed          Workflow run completed                     │
│  workflow.run.failed             Workflow run failed                        │
│  workflow.step.started           Step started                               │
│  workflow.step.completed         Step completed                             │
│  workflow.step.failed            Step failed                                │
│                                                                              │
│  APPROVAL EVENTS                                                            │
│  ───────────────                                                            │
│  approval.requested              Approval requested                         │
│  approval.decided                Approval decided                           │
│  approval.expired                Approval expired                           │
│                                                                              │
│  SYSTEM EVENTS                                                              │
│  ─────────────                                                              │
│  system.worker.registered        Worker registered                          │
│  system.worker.health            Worker health update                       │
│  system.config.updated           Config updated                             │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 10. Database Schema

```sql
-- migrations/001_initial.sql

-- Organizations
CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT UNIQUE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Teams
CREATE TABLE teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(org_id, slug)
);

-- Workflows
CREATE TABLE workflows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    description TEXT,
    version TEXT DEFAULT '1.0',
    timeout_seconds INT DEFAULT 3600,
    steps JSONB NOT NULL,
    config JSONB,
    input_schema JSONB,
    is_template BOOLEAN DEFAULT FALSE,
    visibility TEXT DEFAULT 'private',
    parameters JSONB,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_workflows_org ON workflows(org_id);
CREATE INDEX idx_workflows_team ON workflows(team_id);
CREATE INDEX idx_workflows_template ON workflows(is_template) WHERE is_template = TRUE;

-- Workflow Runs
CREATE TABLE workflow_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID REFERENCES workflows(id) ON DELETE SET NULL,
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    input JSONB,
    context JSONB,
    status TEXT NOT NULL DEFAULT 'pending',
    output JSONB,
    error JSONB,
    steps JSONB NOT NULL,
    total_cost_usd DECIMAL(10,6) DEFAULT 0,
    triggered_by TEXT NOT NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_runs_workflow ON workflow_runs(workflow_id);
CREATE INDEX idx_runs_org ON workflow_runs(org_id);
CREATE INDEX idx_runs_status ON workflow_runs(status);
CREATE INDEX idx_runs_created ON workflow_runs(created_at DESC);

-- Workers
CREATE TABLE workers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    description TEXT,
    icon TEXT,
    tags TEXT[],
    type TEXT NOT NULL,
    docker_config JSONB,
    http_config JSONB,
    lambda_config JSONB,
    script_config JSONB,
    input_schema JSONB,
    output_schema JSONB,
    default_timeout_seconds INT DEFAULT 300,
    default_retry JSONB,
    resources JSONB,
    min_instances INT DEFAULT 0,
    max_instances INT DEFAULT 10,
    visibility TEXT DEFAULT 'private',
    status TEXT DEFAULT 'active',
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_workers_org ON workers(org_id);
CREATE INDEX idx_workers_team ON workers(team_id);
CREATE INDEX idx_workers_type ON workers(type);

-- Jobs
CREATE TABLE jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_run_id UUID REFERENCES workflow_runs(id) ON DELETE CASCADE,
    step_id TEXT,
    iteration_idx INT,
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    worker_id UUID REFERENCES workers(id) ON DELETE SET NULL,
    topic TEXT NOT NULL,
    input JSONB,
    timeout_seconds INT,
    priority INT DEFAULT 50,
    status TEXT NOT NULL DEFAULT 'pending',
    assigned_to TEXT,
    output JSONB,
    error JSONB,
    duration_ms BIGINT,
    cost_usd DECIMAL(10,6) DEFAULT 0,
    attempt INT DEFAULT 1,
    max_retries INT DEFAULT 3,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_jobs_run ON jobs(workflow_run_id);
CREATE INDEX idx_jobs_org ON jobs(org_id);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_worker ON jobs(worker_id);
CREATE INDEX idx_jobs_created ON jobs(created_at DESC);

-- Approvals
CREATE TABLE approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_run_id UUID REFERENCES workflow_runs(id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT,
    message TEXT,
    data JSONB,
    approvers JSONB NOT NULL,
    timeout_seconds INT,
    timeout_action TEXT DEFAULT 'reject',
    status TEXT NOT NULL DEFAULT 'pending',
    decision TEXT,
    comment TEXT,
    decided_by TEXT,
    decided_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_approvals_run ON approvals(workflow_run_id);
CREATE INDEX idx_approvals_org ON approvals(org_id);
CREATE INDEX idx_approvals_status ON approvals(status);
CREATE INDEX idx_approvals_expires ON approvals(expires_at) WHERE status = 'pending';

-- Config
CREATE TABLE configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope TEXT NOT NULL, -- 'system', 'org', 'team', 'workflow', 'step'
    scope_id TEXT, -- org_id, team_id, workflow_id, etc.
    config JSONB NOT NULL,
    set_by TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(scope, scope_id)
);

CREATE INDEX idx_configs_scope ON configs(scope, scope_id);

-- Audit Log
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    actor TEXT NOT NULL, -- user ID or 'system'
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    details JSONB,
    ip_address INET,
    user_agent TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_org ON audit_logs(org_id);
CREATE INDEX idx_audit_actor ON audit_logs(actor);
CREATE INDEX idx_audit_action ON audit_logs(action);
CREATE INDEX idx_audit_created ON audit_logs(created_at DESC);

-- Usage/Metrics (for billing)
CREATE TABLE usage_records (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID REFERENCES organizations(id) ON DELETE CASCADE,
    team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    workflow_id UUID REFERENCES workflows(id) ON DELETE SET NULL,
    job_id UUID REFERENCES jobs(id) ON DELETE SET NULL,
    metric_type TEXT NOT NULL, -- 'job_count', 'llm_tokens', 'compute_seconds', 'cost_usd'
    value DECIMAL(20,6) NOT NULL,
    dimensions JSONB, -- { "model": "gpt-4", "worker_type": "docker" }
    recorded_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_usage_org ON usage_records(org_id);
CREATE INDEX idx_usage_recorded ON usage_records(recorded_at);
CREATE INDEX idx_usage_type ON usage_records(metric_type);

-- Partitioning for large tables (optional, for scale)
-- CREATE TABLE workflow_runs_y2024m01 PARTITION OF workflow_runs
--     FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');
```

---

## 11. Implementation Roadmap

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       IMPLEMENTATION PHASES                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  PHASE 1: CORE ENGINE (2-3 weeks)                                           │
│  ─────────────────────────────────                                          │
│  ☐ Data models & database schema                                           │
│  ☐ Workflow Engine (DAG execution)                                         │
│  ☐ Expression Evaluator                                                    │
│  ☐ Job Scheduler                                                           │
│  ☐ NATS integration                                                        │
│  ☐ Redis state management                                                  │
│                                                                              │
│  PHASE 2: WORKER SYSTEM (2 weeks)                                          │
│  ─────────────────────────────────                                          │
│  ☐ Worker Registry                                                         │
│  ☐ Docker Worker Manager                                                   │
│  ☐ HTTP Worker Manager                                                     │
│  ☐ Script Worker Manager                                                   │
│  ☐ Secret injection (Vault integration)                                    │
│                                                                              │
│  PHASE 3: BUILT-IN STEPS (1-2 weeks)                                       │
│  ───────────────────────────────────                                        │
│  ☐ LLM Handler (multi-provider)                                            │
│  ☐ Approval Handler                                                        │
│  ☐ Notify Handler                                                          │
│  ☐ Transform Handler                                                       │
│  ☐ Delay Handler                                                           │
│                                                                              │
│  PHASE 4: CONFIG & SAFETY (1 week)                                         │
│  ─────────────────────────────────                                          │
│  ☐ Config Service (inheritance)                                            │
│  ☐ Safety Kernel (PII, injection)                                          │
│  ☐ Rate Limiting                                                           │
│  ☐ Budget Tracking                                                         │
│                                                                              │
│  PHASE 5: API & AUTH (1 week)                                              │
│  ─────────────────────────────                                              │
│  ☐ REST API                                                                │
│  ☐ WebSocket for streaming                                                 │
│  ☐ Auth (JWT, API keys)                                                    │
│  ☐ RBAC                                                                    │
│                                                                              │
│  PHASE 6: DASHBOARD (2-3 weeks)                                            │
│  ───────────────────────────────                                            │
│  ☐ Workflow Builder UI                                                     │
│  ☐ Worker Registration UI                                                  │
│  ☐ Run Monitoring UI                                                       │
│  ☐ Config Editor UI                                                        │
│  ☐ Analytics Dashboard                                                     │
│                                                                              │
│  PHASE 7: POLISH (1-2 weeks)                                               │
│  ───────────────────────────────                                            │
│  ☐ Error handling & recovery                                               │
│  ☐ Metrics & observability                                                 │
│  ☐ Documentation                                                           │
│  ☐ Testing                                                                 │
│  ☐ Performance optimization                                                │
│                                                                              │
│  TOTAL: ~10-14 weeks                                                        │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 12. Summary: Key Technical Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Language** | Go | Performance, concurrency, single binary |
| **Message Bus** | NATS JetStream | Lightweight, persistent, request/reply |
| **State Store** | Redis | Fast, TTL support, pub/sub |
| **Database** | Postgres | JSONB for flexible schemas, reliability |
| **Container Runtime** | Docker | Universal, well-supported |
| **Secrets** | Vault | Industry standard, secure |
| **Expression Engine** | Custom | Tailored syntax, performance |
| **Config System** | Hierarchical merge | Flexibility, inheritance |
| **Worker Isolation** | Container per job | Security, resource limits |
