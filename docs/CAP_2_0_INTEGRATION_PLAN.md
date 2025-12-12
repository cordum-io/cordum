# CAP 2.0.0 Integration Plan for CoreTex

## Overview

This document outlines the steps to fully align CoreTex with CAP 2.0.0. The goal is to make CoreTex the canonical reference implementation of CAP.

## Current State Gap Analysis

| Feature | CAP 2.0.0 | CoreTex Current | Action |
|---------|-----------|-----------------|--------|
| Package | `coretex.agent.v1` | `coretex.v1` | Migrate |
| JobStatus | 10 states (incl PENDING, SCHEDULED, DISPATCHED, RUNNING, SUCCEEDED) | 6 states (COMPLETED instead of SUCCEEDED) | Align |
| ContextHints | Message with `max_input_tokens`, `allow_summarization`, `allow_retrieval`, `tags` | Flat fields + `ContextMode` enum | Replace |
| Budget | Message with token limits + `deadline_ms` | Flat `max_input/output_tokens` | Add |
| tenant_id | First-class field (13) | In `env_vars` map | Promote |
| principal_id | First-class field (14) | Missing | Add |
| labels | Map field (15) | Missing | Add |
| signature | bytes field (14) in BusPacket | Missing | Add |
| Field numbers | CAP standard | Different (parent_job_id=10 vs 7) | **Breaking change** |

---

## Phase 1: Proto Alignment (Day 1-2)

### Step 1.1: Import CAP module (no copying)

```go
// go.mod
require github.com/coretexos/cap/v2 v2.0.0
```

Then import:
```go
import agentv1 "github.com/coretexos/cap/v2/go/cortex/agent/v1"
```

### Step 1.2: Create Compatibility Layer

For gradual migration, create adapters between old and new types:

```go
// core/protocol/compat/convert.go
package compat

import (
    oldpb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
    newpb "github.com/coretexos/cap/v2/go/cortex/agent/v1"
)

func JobRequestToCAP(old *oldpb.JobRequest) *newpb.JobRequest {
    return &newpb.JobRequest{
        JobId:       old.JobId,
        Topic:       old.Topic,
        Priority:    convertPriority(old.Priority),
        ContextPtr:  old.ContextPtr,
        AdapterId:   old.AdapterId,
        Env:         old.EnvVars,
        ParentJobId: old.ParentJobId,
        WorkflowId:  old.WorkflowId,
        StepIndex:   old.StepIndex,
        MemoryId:    old.MemoryId,
        TenantId:    old.EnvVars["tenant_id"],
        ContextHints: &newpb.ContextHints{
            MaxInputTokens:     old.MaxInputTokens,
            AllowSummarization: old.ContextMode == oldpb.ContextMode_CONTEXT_MODE_RAG,
            AllowRetrieval:     old.ContextMode == oldpb.ContextMode_CONTEXT_MODE_RAG,
        },
        Budget: &newpb.Budget{
            MaxInputTokens:  int64(old.MaxInputTokens),
            MaxOutputTokens: int64(old.MaxOutputTokens),
        },
    }
}

func JobRequestFromCAP(new *newpb.JobRequest) *oldpb.JobRequest {
    // Reverse conversion for backward compat
}
```

### Step 1.3: Verify upstream protos

No local proto files should be modified. Ensure all components consume the upstream CAP v2.0.0 definitions via the Go module import; reference field layouts from the CAP repo when needed.

---

## Phase 2: Scheduler Updates (Day 2-3)

### Step 2.1: Update Engine to Use New Fields

```go
// core/controlplane/scheduler/engine.go

func (e *Engine) processJob(req *pb.JobRequest, traceID string) {
    // Use first-class tenant_id instead of env
    tenant := req.GetTenantId()
    if tenant == "" {
        tenant = req.GetEnv()["tenant_id"] // Fallback for compat
    }
    
    // Use principal_id for audit
    principal := req.GetPrincipalId()
    
    // Use labels for routing decisions
    labels := req.GetLabels()
    
    // Use budget.deadline_ms for per-job timeout
    if budget := req.GetBudget(); budget != nil && budget.DeadlineMs > 0 {
        e.jobStore.SetDeadline(ctx, req.JobId, time.Now().Add(
            time.Duration(budget.DeadlineMs)*time.Millisecond,
        ))
    }
    
    // ... rest of processing
}
```

### Step 2.2: Update Strategy for Labels

```go
// core/controlplane/scheduler/strategy_least_loaded.go

func (s *LeastLoadedStrategy) PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error) {
    labels := req.GetLabels()
    
    // Filter workers by label requirements
    var eligible []*pb.Heartbeat
    for _, hb := range workers {
        if hb.GetPool() != pool {
            continue
        }
        // Check label constraints
        if !matchesLabels(hb, labels) {
            continue
        }
        eligible = append(eligible, hb)
    }
    
    // ... pick least loaded from eligible
}

func matchesLabels(hb *pb.Heartbeat, required map[string]string) bool {
    workerLabels := hb.GetLabels() // Need to add to Heartbeat proto
    for k, v := range required {
        if workerLabels[k] != v {
            return false
        }
    }
    return true
}
```

### Step 2.3: Update Safety Client

```go
// core/controlplane/scheduler/safety_client.go

func (c *SafetyClient) Check(req *pb.JobRequest) (SafetyDecision, string) {
    // Use first-class fields
    resp, err := c.client.Check(ctx, &pb.PolicyCheckRequest{
        JobId:       req.GetJobId(),
        Topic:       req.GetTopic(),
        Tenant:      req.GetTenantId(),      // First-class
        PrincipalId: req.GetPrincipalId(),   // New field
        Priority:    req.GetPriority(),
        Labels:      req.GetLabels(),        // For policy decisions
    })
    // ...
}
```

---

## Phase 3: Worker Runtime Updates (Day 3-4)

### Step 3.1: Handle ContextHints

```go
// core/agent/runtime/worker.go

func (w *Worker) wrapHandler(handler HandlerFunc) func(*pb.BusPacket) {
    return func(packet *pb.BusPacket) {
        req := packet.GetJobRequest()
        if req == nil {
            return
        }
        
        // Build context based on hints
        ctx := w.buildContext(req)
        
        // ... execute handler
    }
}

func (w *Worker) buildContext(req *pb.JobRequest) context.Context {
    ctx := context.Background()
    
    hints := req.GetContextHints()
    budget := req.GetBudget()
    
    // Apply deadline from budget
    if budget != nil && budget.DeadlineMs > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, 
            time.Duration(budget.DeadlineMs)*time.Millisecond)
        defer cancel()
    }
    
    // Store hints in context for handler use
    ctx = context.WithValue(ctx, contextHintsKey, hints)
    ctx = context.WithValue(ctx, budgetKey, budget)
    ctx = context.WithValue(ctx, memoryIDKey, req.GetMemoryId())
    
    return ctx
}
```

### Step 3.2: Context-Aware Loading

```go
// core/context/engine/service.go

type ContextLoader struct {
    store     memory.Store
    vectorDB  VectorStore  // For RAG
    summarizer Summarizer  // For compression
}

func (l *ContextLoader) Load(ctx context.Context, req *pb.JobRequest) ([]byte, error) {
    hints := req.GetContextHints()
    memoryID := req.GetMemoryId()
    contextPtr := req.GetContextPtr()
    
    // Decision tree based on hints
    if hints.GetAllowRetrieval() && memoryID != "" {
        // Use RAG: retrieve from vector store
        return l.retrieveFromMemory(ctx, memoryID, hints.GetTags(), hints.GetMaxInputTokens())
    }
    
    // Load raw context
    raw, err := l.store.GetContext(ctx, contextPtr)
    if err != nil {
        return nil, err
    }
    
    // Summarize if allowed and needed
    if hints.GetAllowSummarization() && len(raw) > hints.GetMaxInputTokens()*4 {
        return l.summarizer.Summarize(ctx, raw, hints.GetMaxInputTokens())
    }
    
    return raw, nil
}

func (l *ContextLoader) retrieveFromMemory(ctx context.Context, memoryID string, tags []string, maxTokens int32) ([]byte, error) {
    // Query vector store with memory_id scope
    query := l.extractQuery(ctx) // From current job context
    
    chunks, err := l.vectorDB.Search(ctx, memoryID, query, SearchOpts{
        Tags:      tags,
        MaxTokens: int(maxTokens),
    })
    if err != nil {
        return nil, err
    }
    
    return l.assembleContext(chunks), nil
}
```

---

## Phase 4: API Gateway Updates (Day 4-5)

### Step 4.1: Update REST Handlers

```go
// cmd/coretex-api-gateway/main.go

type SubmitJobHTTPRequest struct {
    Topic       string            `json:"topic"`
    Prompt      string            `json:"prompt"`
    Priority    string            `json:"priority"`
    AdapterId   string            `json:"adapter_id"`
    MemoryId    string            `json:"memory_id"`
    TenantId    string            `json:"tenant_id"`
    PrincipalId string            `json:"principal_id"`
    Labels      map[string]string `json:"labels"`
    
    // Context hints
    MaxInputTokens     int32    `json:"max_input_tokens"`
    AllowSummarization bool     `json:"allow_summarization"`
    AllowRetrieval     bool     `json:"allow_retrieval"`
    Tags               []string `json:"tags"`
    
    // Budget
    MaxOutputTokens int64 `json:"max_output_tokens"`
    MaxTotalTokens  int64 `json:"max_total_tokens"`
    DeadlineMs      int64 `json:"deadline_ms"`
}

func (s *server) handleSubmitJobHTTP(w http.ResponseWriter, r *http.Request) {
    var req SubmitJobHTTPRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid json", http.StatusBadRequest)
        return
    }
    
    jobID := uuid.NewString()
    traceID := uuid.NewString()
    
    // Use tenant from request or fall back to server default
    tenantID := req.TenantId
    if tenantID == "" {
        tenantID = s.tenant
    }
    
    // Build CAP-compliant JobRequest
    jobReq := &pb.JobRequest{
        JobId:       jobID,
        Topic:       req.Topic,
        Priority:    parsePriority(req.Priority),
        ContextPtr:  memory.PointerForKey(memory.MakeContextKey(jobID)),
        AdapterId:   req.AdapterId,
        MemoryId:    req.MemoryId,
        TenantId:    tenantID,
        PrincipalId: req.PrincipalId,
        Labels:      req.Labels,
        ContextHints: &pb.ContextHints{
            MaxInputTokens:     req.MaxInputTokens,
            AllowSummarization: req.AllowSummarization,
            AllowRetrieval:     req.AllowRetrieval,
            Tags:               req.Tags,
        },
        Budget: &pb.Budget{
            MaxInputTokens:  int64(req.MaxInputTokens),
            MaxOutputTokens: req.MaxOutputTokens,
            MaxTotalTokens:  req.MaxTotalTokens,
            DeadlineMs:      req.DeadlineMs,
        },
    }
    
    // ... store context, publish, etc.
}
```

### Step 4.2: Update gRPC Service

```protobuf
// api/proto/v1/api.proto

message SubmitJobRequest {
    string topic = 1;
    string prompt = 2;
    string priority = 3;
    string adapter_id = 4;
    string memory_id = 5;
    string tenant_id = 6;
    string principal_id = 7;
    map<string, string> labels = 8;
    
    // Context hints
    int32 max_input_tokens = 10;
    bool allow_summarization = 11;
    bool allow_retrieval = 12;
    repeated string tags = 13;
    
    // Budget
    int64 max_output_tokens = 20;
    int64 max_total_tokens = 21;
    int64 deadline_ms = 22;
}
```

---

## Phase 5: Job Store Updates (Day 5-6)

### Step 5.1: Store New Fields

```go
// core/infra/memory/job_store.go

const (
    metaFieldTenantId    = "tenant_id"
    metaFieldPrincipalId = "principal_id"
    metaFieldMemoryId    = "memory_id"
    metaFieldDeadline    = "deadline"
    metaFieldLabels      = "labels"
)

func (s *RedisJobStore) SetJobMeta(ctx context.Context, req *pb.JobRequest) error {
    meta := jobMetaKey(req.JobId)
    
    fields := map[string]any{
        metaFieldTopic:       req.Topic,
        metaFieldTenantId:    req.TenantId,
        metaFieldPrincipalId: req.PrincipalId,
        metaFieldMemoryId:    req.MemoryId,
    }
    
    if len(req.Labels) > 0 {
        labelsJSON, _ := json.Marshal(req.Labels)
        fields[metaFieldLabels] = string(labelsJSON)
    }
    
    if req.Budget != nil && req.Budget.DeadlineMs > 0 {
        deadline := time.Now().Add(time.Duration(req.Budget.DeadlineMs) * time.Millisecond)
        fields[metaFieldDeadline] = deadline.Unix()
    }
    
    return s.client.HSet(ctx, meta, fields).Err()
}

// Index by tenant for multi-tenant queries
func (s *RedisJobStore) ListJobsByTenant(ctx context.Context, tenantID string, limit int64) ([]JobRecord, error) {
    key := "job:tenant:" + tenantID
    // ... implement with sorted set
}

// Index by principal for user-specific queries  
func (s *RedisJobStore) ListJobsByPrincipal(ctx context.Context, principalID string, limit int64) ([]JobRecord, error) {
    key := "job:principal:" + principalID
    // ... implement with sorted set
}
```

### Step 5.2: Update Reconciler for Per-Job Deadlines

```go
// core/controlplane/scheduler/reconciler.go

func (r *Reconciler) tick(ctx context.Context) {
    now := time.Now()
    
    // Existing timeout handling
    r.handleTimeouts(ctx, JobStateDispatched, now.Add(-r.dispatchTimeout))
    r.handleTimeouts(ctx, JobStateRunning, now.Add(-r.runningTimeout))
    
    // NEW: Handle per-job deadline expirations
    r.handleDeadlineExpirations(ctx, now)
}

func (r *Reconciler) handleDeadlineExpirations(ctx context.Context, now time.Time) {
    // Query jobs with expired deadlines that are still running
    expired, err := r.store.ListExpiredDeadlines(ctx, now.Unix(), 200)
    if err != nil {
        logging.Error("reconciler", "list expired deadlines", "error", err)
        return
    }
    
    for _, rec := range expired {
        if err := r.store.SetState(ctx, rec.ID, JobStateTimeout); err != nil {
            logging.Error("reconciler", "mark deadline timeout", "job_id", rec.ID, "error", err)
        } else {
            logging.Info("reconciler", "job deadline expired", "job_id", rec.ID)
        }
    }
}
```

---

## Phase 6: Safety Kernel Updates (Day 6)

### Step 6.1: Enhanced Policy Checks

```go
// cmd/coretex-safety-kernel/main.go

func (s *server) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
    decision := pb.DecisionType_DECISION_TYPE_ALLOW
    reason := ""
    
    tenant := req.GetTenant()
    principal := req.GetPrincipalId()
    topic := req.GetTopic()
    labels := req.GetLabels()
    
    // 1. Tenant validation
    if tenant == "" {
        return deny("missing tenant")
    }
    
    // 2. Topic allowlist per tenant
    if !s.policy.IsTopicAllowed(tenant, topic) {
        return deny("topic not allowed for tenant")
    }
    
    // 3. Principal-level restrictions (optional)
    if s.policy.IsPrincipalBlocked(tenant, principal) {
        return deny("principal blocked")
    }
    
    // 4. Label-based policies (e.g., compliance requirements)
    if labels["compliance"] == "hipaa" && !s.policy.IsHIPAACompliant(tenant) {
        return deny("tenant not HIPAA compliant")
    }
    
    // 5. Rate limiting per tenant/principal
    if s.rateLimiter.IsExceeded(tenant, principal) {
        return &pb.PolicyCheckResponse{
            Decision: pb.DecisionType_DECISION_TYPE_THROTTLE,
            Reason:   "rate limit exceeded",
        }, nil
    }
    
    return allow()
}
```

### Step 6.2: Update Safety Proto

```protobuf
// proto/coretex/agent/v1/safety.proto (align with CAP)

message PolicyCheckRequest {
    string job_id = 1;
    string topic = 2;
    string tenant = 3;
    JobPriority priority = 4;
    int64 estimated_cost = 5;
    string principal_id = 6;       // NEW
    map<string, string> labels = 7; // NEW
}
```

---

## Phase 7: Heartbeat Updates (Day 6-7)

### Step 7.1: Add Labels to Heartbeat

```protobuf
// proto/coretex/agent/v1/heartbeat.proto

message Heartbeat {
    string worker_id = 1;
    string region = 2;
    string type = 3;
    float cpu_load = 4;
    float gpu_utilization = 5;
    int32 active_jobs = 6;
    repeated string capabilities = 7;
    string pool = 8;
    int32 max_parallel_jobs = 9;
    map<string, string> labels = 13;  // NEW: worker labels for placement
}
```

### Step 7.2: Update Worker Runtime

```go
// core/agent/runtime/worker.go

type Config struct {
    WorkerID        string
    // ... existing fields
    Labels          map[string]string  // NEW
}

func (w *Worker) heartbeatLoop() {
    // ...
    hb := &pb.Heartbeat{
        WorkerId:        w.Config.WorkerID,
        Region:          w.Config.Region,
        Type:            w.Config.Type,
        CpuLoad:         cpuLoad,
        GpuUtilization:  gpuUtil,
        ActiveJobs:      atomic.LoadInt32(&w.ActiveJobs),
        Capabilities:    w.Config.Capabilities,
        Pool:            w.Config.Pool,
        MaxParallelJobs: w.Config.MaxParallelJobs,
        Labels:          w.Config.Labels,  // NEW
    }
    // ...
}
```

---

## Phase 8: Metrics & Observability (Day 7)

### Step 8.1: Add Tenant/Principal Labels to Metrics

```go
// core/infra/metrics/metrics.go

type Prom struct {
    jobsReceived   *prometheus.CounterVec
    // ...
}

func NewProm(namespace string) *Prom {
    p := &Prom{
        jobsReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
            Namespace: namespace,
            Name:      "jobs_received_total",
            Help:      "Jobs received by topic and tenant",
        }, []string{"topic", "tenant"}),  // Added tenant label
        
        jobsCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
            Namespace: namespace,
            Name:      "jobs_completed_total",
            Help:      "Jobs completed by topic, tenant, and status",
        }, []string{"topic", "tenant", "status"}),  // Added tenant label
        
        // NEW: Token usage tracking
        tokensUsed: prometheus.NewCounterVec(prometheus.CounterOpts{
            Namespace: namespace,
            Name:      "tokens_used_total",
            Help:      "Tokens consumed by tenant and type",
        }, []string{"tenant", "type"}),  // type = input/output
    }
    // ...
}
```

### Step 8.2: Budget Tracking

```go
// core/infra/metrics/metrics.go

func (p *Prom) RecordTokenUsage(tenant string, inputTokens, outputTokens int64) {
    p.tokensUsed.WithLabelValues(tenant, "input").Add(float64(inputTokens))
    p.tokensUsed.WithLabelValues(tenant, "output").Add(float64(outputTokens))
}

func (p *Prom) RecordBudgetExceeded(tenant, topic string) {
    p.budgetExceeded.WithLabelValues(tenant, topic).Inc()
}
```

---

## Phase 9: Testing & Validation (Day 8-9)

### Step 9.1: Unit Tests for New Fields

```go
// core/controlplane/scheduler/engine_test.go

func TestProcessJobWithContextHints(t *testing.T) {
    // ...
    req := &pb.JobRequest{
        JobId:    "job-1",
        Topic:    "job.chat.simple",
        TenantId: "tenant-a",
        ContextHints: &pb.ContextHints{
            MaxInputTokens:     4000,
            AllowSummarization: true,
            AllowRetrieval:     true,
            Tags:               []string{"code", "docs"},
        },
        Budget: &pb.Budget{
            MaxInputTokens:  8000,
            MaxOutputTokens: 2000,
            DeadlineMs:      30000,
        },
    }
    // ... verify hints are passed through
}

func TestStrategyRespectsLabels(t *testing.T) {
    // ...
    req := &pb.JobRequest{
        JobId:  "job-gpu",
        Topic:  "job.code.llm",
        Labels: map[string]string{"gpu": "true", "region": "us-east-1"},
    }
    // ... verify only matching workers are selected
}
```

### Step 9.2: Integration Tests

```go
// tests/integration/cap_compliance_test.go

func TestCAPCompliance(t *testing.T) {
    // Start full stack: NATS, Redis, Scheduler, Safety, Gateway, Worker
    
    t.Run("ContextHintsRespected", func(t *testing.T) {
        // Submit job with allow_retrieval=true
        // Verify worker performed RAG lookup
    })
    
    t.Run("BudgetDeadlineEnforced", func(t *testing.T) {
        // Submit job with deadline_ms=100
        // Verify job times out if worker is slow
    })
    
    t.Run("TenantIsolation", func(t *testing.T) {
        // Submit jobs from different tenants
        // Verify safety policies are tenant-scoped
    })
    
    t.Run("LabelBasedRouting", func(t *testing.T) {
        // Start workers with different labels
        // Submit job with label requirements
        // Verify correct worker receives job
    })
}
```

---

## Phase 10: Documentation (Day 9-10)

### Step 10.1: Update CoreTex Docs

```markdown
# docs/CAP_COMPLIANCE.md

## CoreTex CAP 2.0.0 Compliance

CoreTex fully implements CAP 2.0.0. This document describes how CAP features map to CoreTex components.

### Protocol Alignment

| CAP Feature | CoreTex Component | Notes |
|-------------|-------------------|-------|
| BusPacket | core/protocol/pb/v1 | Direct import from CAP |
| JobRequest | Scheduler, Gateway | All fields supported |
| ContextHints | Context Engine | Drives RAG/summarization |
| Budget | Reconciler | Per-job deadlines |
| Safety Kernel | coretex-safety-kernel | gRPC service |

### Usage Examples

#### Submit Job with Context Hints

```bash
curl -X POST http://localhost:8081/api/v1/jobs \
  -H 'Content-Type: application/json' \
  -d '{
    "topic": "job.chat.simple",
    "prompt": "Explain the codebase",
    "memory_id": "repo:github.com/foo/bar",
    "allow_retrieval": true,
    "allow_summarization": true,
    "max_input_tokens": 4000,
    "tags": ["code", "architecture"]
  }'
```
```

### Step 10.2: Update API Docs

Generate OpenAPI spec with new fields, update Postman collection, etc.

---

## Migration Checklist

### Breaking Changes

- [ ] Proto field numbers changed (parent_job_id: 10→7, etc.)
- [ ] JobStatus values changed (COMPLETED → SUCCEEDED)
- [ ] Package name changed (coretex.v1 → coretex.agent.v1)
- [ ] `env_vars` renamed to `env`

### Feature Additions

- [ ] ContextHints message
- [ ] Budget message
- [ ] tenant_id first-class field
- [ ] principal_id field
- [ ] labels field
- [ ] signature field in BusPacket

### Component Updates

- [ ] Proto files regenerated
- [ ] Scheduler engine updated
- [ ] Scheduling strategy updated
- [ ] Safety client updated
- [ ] Worker runtime updated
- [ ] Context engine updated
- [ ] API gateway updated
- [ ] Job store updated
- [ ] Reconciler updated
- [ ] Heartbeat updated
- [ ] Metrics updated
- [ ] Tests updated
- [ ] Docs updated

---

## Timeline Summary

| Phase | Days | Focus |
|-------|------|-------|
| 1 | 1-2 | Proto alignment |
| 2 | 2-3 | Scheduler updates |
| 3 | 3-4 | Worker runtime |
| 4 | 4-5 | API Gateway |
| 5 | 5-6 | Job Store |
| 6 | 6 | Safety Kernel |
| 7 | 6-7 | Heartbeat |
| 8 | 7 | Metrics |
| 9 | 8-9 | Testing |
| 10 | 9-10 | Documentation |

**Total: ~10 days for full CAP 2.0.0 integration**

---

## Rollout Strategy

1. **Feature flag**: Add `CAP_COMPAT_MODE=v2` env var
2. **Dual-write**: Write both old and new field locations during transition
3. **Shadow mode**: Run new scheduler in parallel, compare decisions
4. **Canary**: Route 5% → 25% → 100% of traffic to CAP-compliant path
5. **Deprecate**: Remove old proto/fields after 30 days of clean operation
