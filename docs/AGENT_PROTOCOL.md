# CortexOS Agent Protocol (Agent ⇄ Agent over the Control Plane)

This document defines how CortexOS components (workers, scheduler, API gateway, external orchestrators) communicate over the NATS bus using Protobuf envelopes and Redis pointers.
This is the required spec for any new agent implementation.

## Actors (Agents)
- **Agents** = any active component that produces or consumes jobs:
  - Leaf workers (LLM, math, k8s, etc.)
  - Orchestrator workers (workflows)
  - System tools (e.g., repo orchestration)

## Communication model
- Agents never talk directly.
- All communication flows through the control plane using:
  - NATS subjects (queue groups for load-balancing)
  - Protobuf messages (`BusPacket`, `JobRequest`, `JobResult`, `Heartbeat`, `SystemAlert`)
  - Redis pointers (`context_ptr`, `result_ptr`)
  - Job state in `JobStore`

## 1. Core Concepts
- **Jobs**: unit of work submitted to the control plane with a `context_ptr` and destination `topic`.
- **Pools**: logical grouping of workers behind a subject (e.g., `job.chat.simple` → pool `chat-simple`).
- **Heartbeats**: agent status updates (load, pool, capacity) that inform scheduling.
- **Pointers**: Redis-backed references to context/results, never pass large payloads over NATS.
- **Safety**: gRPC-based policy gate checked by the scheduler before dispatch.

### 1.1 Jobs
- A job is the smallest schedulable unit of work.
- Identified by `job_id` (UUID string).
- Payload includes:
  - `topic` (NATS subject for its worker pool, e.g., `job.chat.simple`)
  - `priority` (`INTERACTIVE|BATCH|CRITICAL`)
  - `context_ptr` (`redis://ctx:<job_id>`, JSON blob)
  - `adapter_id` (worker-specific skill / LoRA / profile)
- Jobs are immutable contracts once created; agents operate on context/results referenced by pointers.
- Managed by the **JobStore** with states: `PENDING`, `RUNNING`, `COMPLETED`, `FAILED`, `DENIED`.
- Flow:
  1) Client writes context JSON to Redis (`ctx:<job_id>`).
  2) Publish `BusPacket{JobRequest}` to `sys.job.submit`.
  3) Scheduler checks safety; if allowed, selects pool/worker and republishes to `topic`.
  4) Worker executes, stores result at `res:<job_id>`, publishes `BusPacket{JobResult}` to `sys.job.result`.
  5) Scheduler updates `JobStore` state and `result_ptr`.

### 1.2 Agents
- An agent is a process/container that:
  - **Produces** jobs (publishes `JobRequest` envelopes to `sys.job.submit`).
  - **Consumes** jobs (subscribes to `job.*` subjects in a queue group, executes work, publishes `JobResult` to `sys.job.result`).
  - **Reports** health (publishes `Heartbeat` on `sys.heartbeat.*`).
  - Operates only on pointers to context/results; it never modifies job contracts directly.
  - Connects to NATS (`NatsBus`).
  - Optionally connects to Redis (`RedisStore`) and JobStore.
  - Subscribes to one or more job subjects (e.g., `job.chat.simple`).
  - Periodically sends heartbeats on `sys.heartbeat.<pool>`.
  - Types:
    - Leaf workers (LLM, math, k8s, etc.)
    - Orchestrator workers (workflows)
    - System tools (e.g., repo orchestration)

1. **Leaf Workers**
   - Implement actual work (LLM, math solver, k8s ops, etc.).
   - Consume `JobRequest`, produce `JobResult`.

2. **Orchestrator Workers**
   - Also implemented as workers (subscribe to `job.workflow.*` or similar).
   - Consume a “parent” `JobRequest`.
   - Produce child jobs by publishing new `JobRequest`s to the system.
   - Aggregate child `JobResult`s and write a final `JobResult` for the parent.

3. **Clients (non-NATS)**
   - Use the API Gateway (`SubmitJob`, `GetJobStatus`) and never see NATS directly.
   - Not considered agents in this spec.

### 1.3 Agent-to-Agent Communication Model
- No direct RPC between agents.
- There is no direct point-to-point protocol like “agent A calls agent B”.
- All communication is mediated by the control plane:
  1. Producer agent writes input to Redis → gets a `context_ptr`.
  2. Producer agent creates a `JobRequest` and publishes it to `sys.job.submit`.
  3. Scheduler:
     - Calls Safety Kernel.
     - Chooses the right pool / subject (`job.*`).
     - Re-publishes the `JobRequest` to the chosen job subject.
  4. Consumer agent (worker) subscribed to that subject:
     - Fetches context from Redis.
     - Processes.
     - Writes result to Redis (`result_ptr`).
     - Publishes `JobResult` to `sys.job.result`.
  5. JobStore is updated on every transition:
     - `PENDING` → `RUNNING` → `COMPLETED` / `FAILED` / `DENIED`.
  - `Heartbeat` → `sys.heartbeat.*` → scheduler registry for load/capacity.
  - Orchestrators publish child `JobRequest`s the same way; parents/children are related by `job_id`/context, not by direct calls.
  - Agents interact only via jobs + Redis pointers.

**Agent-to-agent** is therefore always:
- Agent A publishes `JobRequest` → scheduler routes via pool → Agent B consumes.
- Agent B publishes `JobResult` → scheduler updates state → Agent A (or any consumer) can read the `result_ptr` via JobStore/Redis.
> Agent → Job → Scheduler → Other Agent, mediated by the control plane.

## 2. Protocol Primitives (Protobuf)

### 2.1 Envelope: `BusPacket`
- Fields:
  - `trace_id` (string, UUID recommended)
  - `sender_id` (string)
  - `created_at` (timestamp)
  - `protocol_version` (int32, current: `1`)
  - `payload` (oneof: `JobRequest`, `JobResult`, `Heartbeat`, `SystemAlert`, etc.)
- Transport: always sent over NATS subjects.
- All agent traffic over NATS is a `BusPacket`.

```proto
// api/proto/v1/packet.proto
syntax = "proto3";

package cortex.v1;
option go_package = "github.com/yaront1111/cortex-os/core/pkg/pb/v1";

message BusPacket {
  string trace_id         = 1;
  string sender_id        = 2;
  google.protobuf.Timestamp created_at = 3;
  int32  protocol_version = 4;

  oneof payload {
    JobRequest  job_request  = 10;
    JobResult   job_result   = 11;
    Heartbeat   heartbeat    = 12;
    SystemAlert alert        = 13;
  }
}
```

### 2.2 JobRequest

```proto
// api/proto/v1/job.proto
message JobRequest {
  string job_id      = 1;
  string topic       = 2; // logical target: job.chat.simple, job.repo.lint, etc.
  JobPriority priority = 3; // enum; INTERACTIVE / BATCH / CRITICAL
  string context_ptr = 4; // "redis://ctx:<job_id or other key>"
  string adapter_id  = 5; // worker-specific mode, LoRA, skill id
  map<string, string> env_vars = 6; // optional key/value hints
  // (Optional extensions for workflows — to be appended at the end)
  // string parent_job_id = 7;
  // string workflow_id   = 8;
  // int32  step_index    = 9;
}
```

- Contracts:
  - `job_id` must be non-empty and unique per job.
  - `topic` must be non-empty and map to a configured pool subject (`job.*`).
  - `context_ptr` must be a valid `redis://` pointer if the job uses Redis context (payload typically JSON).
  - `priority` should reflect caller SLO; current strategy is load-based, but priority is reserved for policy.
  - Do not mutate `JobRequest` after creation.
  - `adapter_id` may be empty if the worker has a default mode.
  - `env_vars` are optional; avoid secrets unless needed and scoped to worker.
  - Optional workflow fields, if added, must be appended with new field numbers (never renumber).

### 2.3 JobResult

```proto
// api/proto/v1/job.proto
message JobResult {
  string job_id   = 1;
  JobStatus status = 2;      // COMPLETED, FAILED, etc.
  string result_ptr = 3;     // "redis://res:<job_id>" or other key
  string worker_id   = 4;    // who did the work (from Heartbeat.worker_id)
  int64 execution_ms = 5;    // time spent in the worker
}
```

- Invariants:
- `job_id` must match the originating `JobRequest.job_id`.
- `status` must reflect final state for this attempt (`COMPLETED`, `FAILED`, `CANCELLED`).
- `result_ptr` must be set if `status=COMPLETED`; it should point to a valid Redis key.
- If `status=FAILED`, `result_ptr` may point to error details or be empty.
- `worker_id` should match `Heartbeat.worker_id` for that agent.
- `execution_ms` should reflect wall-clock execution duration.

### 2.4 Heartbeat

```proto
// api/proto/v1/heartbeat.proto
message Heartbeat {
  string worker_id = 1;
  string region    = 2;
  string type      = 3;              // "gpu", "cpu-tools", "cpu"

  float cpu_load        = 4;         // 0.0–100.0
  float gpu_utilization = 5;         // 0.0–100.0
  int32 active_jobs     = 6;

  repeated string capabilities = 7;   // ["chat", "math", "repo-lint"]
  reserved 8, 9, 10;
  string pool = 11;                   // worker pool name
  int32 max_parallel_jobs = 12;       // capacity hint
}
```

- Invariants:
  - `worker_id` must be unique per process instance and consistent across heartbeats and results.
  - `pool` must match a configured pool name in the scheduler and the worker’s subscribed subject group.
  - `active_jobs` should reflect current in-flight jobs.
- `cpu_load` / `gpu_utilization` should be normalized 0-100.
- Heartbeats must be sent at a regular interval (e.g., every 5s).
- Append-only for new fields; do not renumber existing ones.

### 2.5 SystemAlert

```proto
// api/proto/v1/packet.proto (placeholder; add field numbers when defined)
message SystemAlert {
  string level     = 1;  // INFO, WARN, CRITICAL
  string message   = 2;
  string component = 3;  // "worker-chat-1", "repo-orchestrator", etc.
}
```

- Reserved for control-plane events (e.g., degraded worker pool, safety violation logs).
- Extend with new fields at the end; do not reuse ids.
- Used for agent → system error reporting.
- Agents can publish alerts to `sys.alert` or `sys.alert.<component>`.

## 3. NATS Subjects and Roles
- Submission: `sys.job.submit` (queue: `cortex-scheduler`)
- Results: `sys.job.result` (queue: `cortex-scheduler`, and any observers)
- Heartbeats: `sys.heartbeat.*` (scheduler subscribes to `sys.heartbeat.>`)
- Alerts: `sys.alert` (and `sys.alert.<component>`) for `SystemAlert`
- Worker pools (current examples):
  - `job.echo` → pool `echo` → queue `workers-echo`
  - `job.chat.simple` → pool `chat-simple` → queue `workers-chat`
  - `job.chat.advanced` → pool `chat-advanced` → queue `workers-chat-advanced`
  - Future pools follow `job.<domain>.<variant>` convention.

### 3.1 System Subjects
- `sys.job.submit`
  - Producers: API Gateway, orchestrator agents, internal tools.
  - Consumer: Scheduler (`cortex-scheduler`) [queue group: `cortex-scheduler`].
  - Payload: `BusPacket{JobRequest}`; jobs are immutable after publish.
- `sys.job.result`
  - Producers: workers (leaf + orchestrators) after execution.
  - Consumers: Scheduler (`cortex-scheduler`) (state updates); later Telemetry/observers may tap.
  - Payload: `BusPacket{JobResult}`; idempotency is the producer’s responsibility if retried.
- `sys.heartbeat.*`
  - Producers: workers (all agents that execute work).
  - Consumer: Scheduler on `sys.heartbeat.>`.
  - Payload: `BusPacket{Heartbeat}`; emit at steady interval (e.g., 5s).
- `sys.alert` / `sys.alert.<component>`
  - Producers: any agent emitting `SystemAlert`.
  - Consumers: Telemetry / SRE tooling.
  - Payload: `BusPacket{SystemAlert}`.

### 3.2 Job Subjects
- Pattern: `job.<domain>[.<variant>]`
- Each worker pool has a `job.<...>` subject. Current examples:
  - `job.echo` → pool `echo` → queue `workers-echo`
  - `job.chat.simple` → pool `chat-simple` → queue `workers-chat`
  - `job.chat.advanced` → pool `chat-advanced` → queue `workers-chat-advanced`
  - (example reserved) `job.repo.ingest` → pool `repo-ingest` → queue `workers-repo-ingest`
  - (example reserved) `job.repo.lint` → pool `repo-lint` → queue `workers-repo-lint`
  - (example orchestrator) `job.repo.improve` → pool `repo-improve` → queue `workers-repo-improve`
  - (example ops) `job.k8s.ops` → pool `k8s-ops` → queue `workers-k8s-ops`
- Producers: Scheduler republishes `JobRequest` here after safety/selection; orchestrators may publish child jobs.
- Consumers: Workers in the matching pool (queue group).
- Payload: `BusPacket{JobRequest}`.
- Scheduler publishes to `JobRequest.topic` (e.g., `job.chat.simple`).
- Workers subscribe to their pool subjects with a queue group:
  - `job.echo` + queue `workers-echo`
  - `job.chat.simple` + queue `workers-chat`
  - `job.chat.advanced` + queue `workers-chat-advanced`
  - Example: `QueueSubscribe("job.chat.simple", "workers-chat", handler)`
  - NATS handles load balancing within a pool queue group; the scheduler decides which pool/subject to publish to.

## 4. Agent Responsibilities
- Follow the control-plane model (no direct agent→agent calls).
- Use `BusPacket` envelopes over NATS; do not send raw proto messages.
- Store large payloads in Redis and pass pointers (`context_ptr`, `result_ptr`).
- Emit heartbeats regularly; keep `active_jobs` accurate.
- Respect job immutability; never mutate `JobRequest` after publish.
- Publish `JobResult` for every job (completed or failed); set `status` accordingly and `result_ptr` when applicable.
- Use configured pool subject/queue group; ensure `pool` in heartbeats matches.

### 4.1 Leaf Worker Agent
- Subscribes to its pool subject with a queue group (e.g., `job.chat.simple`, queue `workers-chat`).
- Example: `cortex-worker-chat`, pool `chat-simple`.
- On `JobRequest`:
  1) Increment `active_jobs`.
  2) Fetch context via `context_ptr` (Redis).
  3) Execute work.
  4) Store result at `res:<job_id>` → `result_ptr`.
  5) Publish `JobResult` to `sys.job.result` with `status` and `result_ptr`.
  6) Decrement `active_jobs`.
- Emit heartbeats on `sys.heartbeat.<pool>` with accurate `active_jobs`, `cpu_load`, `max_parallel_jobs`.
 - Startup:
   - Read config (NATS URL, Redis URL, pool name, worker_id).
   - Connect to NATS via `NatsBus`.
   - Connect to Redis (`RedisStore`) for `context_ptr` / `result_ptr`.
    - Subscribe to job subject + queue group.
    - Start:
      - Heartbeat loop on `sys.heartbeat.<pool>`.
      - Job handler for `JobRequest` on the subscribed subject.
    - Example subscription: `Subscribe("job.chat.simple", "workers-chat", handleJob)`
    - Heartbeat loop example: publish to `sys.heartbeat.chat-simple` every N seconds.

Job handler pattern:
1) Increment `active_jobs`.
2) Parse `BusPacket` → `JobRequest`.
3) Validate `job_id`, `context_ptr`.
4) Fetch context from Redis; handle missing/invalid pointers gracefully.
5) Execute business logic.
6) Write result to Redis → `result_ptr`.
7) Publish `JobResult` to `sys.job.result`.
8) Decrement `active_jobs`.

Pseudo-code:
```go
func handleJob(packet *pb.BusPacket) {
    req := packet.GetJobRequest()
    if req == nil {
        // log and ignore
        return
    }
    // ... increment active_jobs, fetch context, execute, write result_ptr, publish JobResult, decrement ...
}
// 1. Fetch context from Redis
ctxKey, err := memory.KeyFromPointer(req.GetContextPtr())
// handle error: publish FAILED result if needed

ctxBytes, err := memStore.GetContext(ctx, ctxKey)
// decode ctxBytes into a struct with prompt, repo info, etc.

// 2. Do work (call model, run tool, etc.)
start := time.Now()
resultPayload := doWork(ctxBytes)

// 3. Store result in Redis
resKey := memory.MakeResultKey(req.JobId)
resultPayloadBytes, _ := json.Marshal(resultPayload)
if err := memStore.PutResult(ctx, resKey, resultPayloadBytes); err != nil {
    // Optionally publish FAILED JobResult with error info
}
resultPtr := memory.PointerForKey(resKey)

// 4. Publish JobResult to sys.job.result
res := &pb.JobResult{
    JobId:       req.JobId,
    Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
    ResultPtr:   resultPtr,
    WorkerId:    workerID,
    ExecutionMs: time.Since(start).Milliseconds(),
}

packet := &pb.BusPacket{
    TraceId:         packet.TraceId, // preserve trace
    SenderId:        workerID,
    CreatedAt:       timestamppb.Now(),
    ProtocolVersion: 1,
    Payload: &pb.BusPacket_JobResult{JobResult: res},
}

bus.Publish("sys.job.result", packet)

}
```

Heartbeat loop pattern:
1) Every N seconds (e.g., 5s):
   - Build `Heartbeat` with:
     - `worker_id`
     - `pool`
     - `type`
     - `active_jobs`
     - `cpu_load` / `gpu_utilization`
     - `capabilities`
     - `max_parallel_jobs`
   - Wrap in `BusPacket` and publish to `sys.heartbeat.<pool>`.

```go
hb := &pb.Heartbeat{
    WorkerId:        workerID,
    Region:          cfg.Region,
    Type:            "cpu",
    CpuLoad:         measuredCPULoad(),
    GpuUtilization:  0,
    ActiveJobs:      currentActiveJobs,
    Capabilities:    []string{"chat"},
    Pool:            "chat-simple",
    MaxParallelJobs: cfg.MaxParallel,
}

packet := &pb.BusPacket{
    TraceId:         "", // optional
    SenderId:        workerID,
    CreatedAt:       timestamppb.Now(),
    ProtocolVersion: 1,
    Payload: &pb.BusPacket_Heartbeat{Heartbeat: hb},
}

bus.Publish("sys.heartbeat.chat-simple", packet)
```

### 4.2 Orchestrator Agent
- Orchestrator agents are workers that produce other jobs.
- Behaves like a worker (subscribes to `job.workflow.*` or similar) but can spawn child jobs.
- Example: `cortex-worker-repo-orchestrator` on `job.repo.improve`.
- On parent `JobRequest`:
  1) Increment `active_jobs`.
  2) Fetch/parse context.
  3) Plan child tasks; for each child:
     - Write child context to Redis (`ctx:<child_job_id>`).
     - Publish `JobRequest` to `sys.job.submit` (child job_id, topic for desired pool).
  4) Await child `JobResult`s (subscribe or poll `JobStore`/Redis).
  5) Aggregate results; write parent result to Redis → `result_ptr`.
  6) Publish parent `JobResult` to `sys.job.result`.
  7) Decrement `active_jobs`.
- Heartbeats: same as leaf workers, with `capabilities` indicating orchestration domain.
- Must not directly call other agents; always use job submissions.
Key rules:
- Parent/child relations are implicit via `job_id` and context; no direct RPC.
- Child jobs are submitted through `sys.job.submit` with their own `job_id` and topics.
- Orchestrator must emit a parent `JobResult` even if children fail (set `status=FAILED` and include error info).
  - Orchestrator must not bypass the control plane:
    - Never call worker RPCs directly.
    - Never write to another worker’s Redis keys directly (use child jobs + pointers).
    - Child `JobRequest`s are published to `sys.job.submit`.
    - Wait for child completion via `JobStore` (status/result_ptr), not by manually subscribing to `sys.job.result`.

Parent job handling pattern:
1) Receive parent `JobRequest`.
2) Plan child jobs (derive child contexts).
3) For each child:
   - Write `ctx:<child_job_id>` to Redis.
   - Publish `JobRequest` (child) to `sys.job.submit`.
4) Poll `JobStore`/Redis for child `result_ptr`/states until all done or timeout.
5) Aggregate child results; write parent result to Redis (`res:<parent_job_id>`).
6) Publish parent `JobResult` to `sys.job.result` with final status.

```go
func handleRepoImprove(packet *pb.BusPacket) {
    parentReq := packet.GetJobRequest()
    parentJobID := parentReq.GetJobId()
    // ... spawn child jobs, poll JobStore, aggregate, publish parent JobResult ...
}

// 1. Read parent context
ctxKey, _ := memory.KeyFromPointer(parentReq.GetContextPtr())
ctxBytes, _ := memStore.GetContext(ctx, ctxKey)
parentCtx := decodeRepoImproveContext(ctxBytes)

// 2. Define steps (V1: hard-coded pipeline)
steps := []Step{
    {Topic: "job.repo.ingest", Adapter: "default"},
    {Topic: "job.repo.lint",   Adapter: "default"},
    {Topic: "job.repo.tests",  Adapter: "default"},
}

var childJobIDs []string

for i, step := range steps {
    childJobID := uuid.NewString()
    // 2a. Write step-specific context to Redis
    childCtxKey := memory.MakeContextKey(childJobID)
    childCtxBytes := encodeChildContext(parentCtx, step)
    memStore.PutContext(ctx, childCtxKey, childCtxBytes)
    childPtr := memory.PointerForKey(childCtxKey)

    // 2b. Create JobRequest
    childReq := &pb.JobRequest{
        JobId:      childJobID,
        Topic:      step.Topic,
        Priority:   pb.JobPriority_JOB_PRIORITY_BATCH,
        ContextPtr: childPtr,
        AdapterId:  step.Adapter,
        // parent/workflow fields when defined in proto:
        // ParentJobId: parentJobID,
        // WorkflowId:  parentJobID,
        // StepIndex:   int32(i),
    }

    // 2c. Publish to sys.job.submit
    childPacket := &pb.BusPacket{
        TraceId:         packet.TraceId, // keep same trace
        SenderId:        "repo-orchestrator-1",
        CreatedAt:       timestamppb.Now(),
        ProtocolVersion: 1,
        Payload: &pb.BusPacket_JobRequest{JobRequest: childReq},
    }
    bus.Publish("sys.job.submit", childPacket)
    jobStore.SetState(ctx, childJobID, JobPending)
    jobStore.AddChild(ctx, parentJobID, childJobID)

    childJobIDs = append(childJobIDs, childJobID)
    // (Optional) sequential mode: wait for this child before next step
}

// 3. Wait for all children to complete (poll JobStore)
results := waitForChildren(jobStore, childJobIDs)

// 4. Merge results into final repo report
finalReportBytes := mergeRepoResults(memStore, results)

// 5. Write parent result
resKey := memory.MakeResultKey(parentJobID)
memStore.PutResult(ctx, resKey, finalReportBytes)
resPtr := memory.PointerForKey(resKey)
jobStore.SetResultPtr(ctx, parentJobID, resPtr)
jobStore.SetState(ctx, parentJobID, JobCompleted)

// 6. Publish parent JobResult
parentRes := &pb.JobResult{
    JobId:       parentJobID,
    Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
    ResultPtr:   resPtr,
    WorkerId:    "repo-orchestrator-1",
    ExecutionMs: time.Since(start).Milliseconds(),
}

parentPacket := &pb.BusPacket{
    TraceId:         packet.TraceId,
    SenderId:        "repo-orchestrator-1",
    CreatedAt:       timestamppb.Now(),
    ProtocolVersion: 1,
    Payload: &pb.BusPacket_JobResult{JobResult: parentRes},
}

bus.Publish("sys.job.result", parentPacket)
}
```
## Transport primitives
- **Bus**: NATS (queue groups for load-balancing).
- **Envelope**: `BusPacket` (`pkg/pb/v1`), fields:
  - `trace_id` (UUID recommended)
  - `sender_id`
  - `created_at` (timestamp)
  - `protocol_version` (current: `1`)
  - `payload` (oneof: `JobRequest`, `JobResult`, `Heartbeat`, `PolicyCheckRequest`/`Response`, etc.)
- **Memory fabric**: Redis backing `context_ptr` and `result_ptr`.
  - Context key: `ctx:<job_id>` → pointer `redis://ctx:<job_id>`
  - Result key: `res:<job_id>` → pointer `redis://res:<job_id>`

## Subjects and queue groups (current)
- Submission: `sys.job.submit` (queue: `cortex-scheduler`)
- Results: `sys.job.result` (queue: `cortex-scheduler`; consumers may tap)
- Heartbeats: `sys.heartbeat.*` (scheduler subscribes to `sys.heartbeat.>`)
- Worker pools (examples):
  - `job.echo` (pool `echo`, queue `workers-echo`)
  - `job.chat.simple` (pool `chat-simple`, queue `workers-chat`)
  - `job.chat.advanced` (pool `chat-advanced`, queue `workers-chat-advanced`)

## Message payloads (proto)

### JobRequest
```proto
message JobRequest {
  string job_id      = 1;
  string topic       = 2; // NATS subject to dispatch to (pool subject)
  JobPriority priority = 3; // INTERACTIVE|BATCH|CRITICAL
  string context_ptr = 4; // redis://ctx:<job_id>
  string adapter_id  = 5; // model/tool selector hint
}
```

### JobResult
```proto
message JobResult {
  string job_id      = 1;
  JobStatus status   = 2; // COMPLETED|FAILED|CANCELLED
  string result_ptr  = 3; // redis://res:<job_id>
  string worker_id   = 4;
  int64 execution_ms = 5;
}
```

### Heartbeat
```proto
message Heartbeat {
  string worker_id = 1;
  string region    = 2;
  string type      = 3; // e.g., "cpu", "gpu", "cpu-tools"

  float cpu_load        = 4; // 0-100
  float gpu_utilization = 5; // 0-100
  int32 active_jobs     = 6;

  repeated string capabilities = 7; // ["chat", "echo", ...]
  // reserved 8,9,10 in proto
  string pool             = 11; // pool name
  int32 max_parallel_jobs = 12; // capacity hint
}
```

### Safety (gRPC)
- `PolicyCheckRequest` / `PolicyCheckResponse` over gRPC (not bus) gates scheduling.
- Current safety kernel denies `topic=sys.destroy`, allows others.

## Lifecycle flows

### Submit → Schedule → Execute → Result
1) **Submit**: API gateway or client stores context JSON at `ctx:<job_id>` in Redis, sets `context_ptr`, publishes `BusPacket{JobRequest}` to `sys.job.submit`.
2) **Safety**: Scheduler receives on `sys.job.submit`, calls safety kernel (gRPC). If denied, marks job state `DENIED` and stops.
3) **Select pool/worker**:
   - Map topic → pool (`job.chat.simple`→`chat-simple`, `job.chat.advanced`→`chat-advanced`, `job.echo`→`echo`).
   - Strategy `LeastLoadedStrategy`: load score = `active_jobs + cpu_load/100 + gpu_utilization/100`. Pick lowest-score heartbeat in that pool.
   - Publish the original `JobRequest` to the pool subject (`req.Topic`) for the worker queue group.
4) **Execute** (worker):
   - Subscribe to its subject with queue group.
   - Increment `active_jobs`; fetch context via `context_ptr` (Redis).
   - Do work (LLM/tool/etc.).
   - Store result JSON at `res:<job_id>`; produce `result_ptr`.
   - Publish `BusPacket{JobResult}` to `sys.job.result`; decrement `active_jobs`.
5) **Result handling**:
   - Scheduler updates job state (`COMPLETED`/`FAILED`) and result_ptr.
   - Clients can poll status via API gateway or read Redis directly if authorized.

### Heartbeats
- Workers emit every ~5s on `sys.heartbeat.<pool>` (exact subject not enforced; wildcard used).
- Fields must include `worker_id`, `pool`, `active_jobs`, `cpu_load`, `max_parallel_jobs`.
- Scheduler registry tracks latest heartbeat per worker and filters by pool during selection.

## Worker responsibilities
- Use the shared Protobuf contracts; do not change field numbers.
- Respect `context_ptr`/`result_ptr` conventions and avoid large payloads on the bus.
- Keep `active_jobs` accurate (increment before work, decrement after).
- Send heartbeats periodically until shutdown.
- Publish results to `sys.job.result` even on failure (set `status=FAILED` and optionally include error info in the result payload stored in Redis).

## Scheduler responsibilities
- Subscribe to `sys.job.submit`, `sys.job.result`, `sys.heartbeat.>`.
- Fail closed on safety errors/timeouts.
- Use topic→pool mapping; no hardcoded worker IDs.
- Log selection decisions (pool, selected worker, score).
- Persist job state/result pointers via `JobStore` (Redis-backed).

## API gateway responsibilities
- Accepts external submissions; writes prompt/context to Redis (`ctx:<job_id>`), sets `context_ptr`.
- Publishes `JobRequest` to `sys.job.submit`.
- Optional: expose `GetJobStatus` that reads job state + `result_ptr` from `JobStore`.

## Operational notes
- Protocol version: `protocol_version=1` in `BusPacket`.
- Queue groups provide at-most-once per worker group; idempotency is the worker’s responsibility if needed.
- If no workers are available in the pool, scheduler logs an error and leaves job in `PENDING` (current behavior).
- Advanced chat worker uses `OLLAMA_URL`/`OLLAMA_MODEL`; if unreachable, it returns a fallback stub prefixed with `[fallback]`.

## 5. Job Lifecycle (Agent View)
1) Producer prepares context:
   - Write input JSON to Redis (`ctx:<job_id>`), derive `context_ptr=redis://ctx:<job_id>`.
   - Build `JobRequest` (job_id, topic, priority, context_ptr, adapter_id, env_vars).
2) Submit:
   - Publish `BusPacket{JobRequest}` to `sys.job.submit`.
3) Scheduler:
   - Safety check (deny → `DENIED` state; allow → continue).
   - Select pool via topic→pool map; choose least-loaded worker (score: active_jobs + cpu/gpu load).
   - Publish `JobRequest` to `JobRequest.topic` (pool subject).
4) Worker execution:
   - Subscribe to subject with queue group.
   - Increment `active_jobs`; fetch context via `context_ptr`.
   - Execute (LLM/tool/orchestration).
   - Store result at `res:<job_id>` → `result_ptr`.
   - Publish `BusPacket{JobResult}` to `sys.job.result`; decrement `active_jobs`.
5) State tracking:
   - Scheduler updates `JobStore` state (`PENDING`→`RUNNING`→`COMPLETED/FAILED/DENIED`) and `result_ptr`.
6) Heartbeats:
   - Workers emit `Heartbeat` on `sys.heartbeat.<pool>` with load/capacity for scheduling.

### 5.1 Submission (Producer / Client Agent)
- Steps:
  1) Generate `job_id` (UUID).
  2) Write context JSON to Redis at `ctx:<job_id>`; set `context_ptr=redis://ctx:<job_id>`.
  3) Create `JobRequest(job_id, topic, priority, context_ptr, adapter_id, env_vars)`.
  4) Wrap in `BusPacket`:
     - `trace_id` (UUID)
     - `sender_id` (component name)
     - `created_at` (now)
     - `protocol_version` (1)
     - `payload = JobRequest`
  5) Publish `BusPacket{JobRequest}` to `sys.job.submit`.
- Example:
  ```go
  ctxKey := "ctx:" + jobID
  memStore.PutContext(ctx, ctxKey, payloadBytes)
  ctxPtr := memory.PointerForKey(ctxKey) // redis://ctx:<job_id>
  ```
- Agents in this role: API Gateway, orchestrators when spawning child jobs, internal tools.

### 5.2 Scheduler (mediator)
- Receives `BusPacket{JobRequest}` on `sys.job.submit`.
- Calls Safety Kernel; if denied → set `DENIED` and stop.
- Maps `topic` to pool; selects least-loaded worker in that pool (score: active_jobs + cpu/gpu load).
- Publishes the same `JobRequest` to `JobRequest.topic` (pool subject).
- Updates `JobStore` state: set `PENDING` on receipt, `RUNNING` on dispatch; `COMPLETED/FAILED` on result.
- If safety returns DENY: mark `DENIED` in `JobStore`; may publish a `JobResult` with failure info if desired.
- If allowed: dispatch, set `RUNNING`, keep `trace_id` unchanged.
- Picks pool/subject → `job.*` based on `topic` mapping.
- Publishes the `JobRequest` to the selected pool subject.

### 5.3 Worker (consumer)
- Subscribes to its pool subject with queue group.
- On `JobRequest`:
  - Increment `active_jobs`; fetch context via `context_ptr`.
  - Execute work; write result to Redis → `result_ptr`.
  - Publish `BusPacket{JobResult}` to `sys.job.result`; decrement `active_jobs`.
  - Heartbeats continue on `sys.heartbeat.<pool>`.
 - On `JobResult` publish:
   - Set `status` (`COMPLETED` or `FAILED`).
   - Include `result_ptr` when available (or error details pointer on failure).
   - Preserve `trace_id` from the incoming request packet.
   - Update `JobStore` state/result_ptr as needed.

### 5.2 Execution (Consumer / Worker Agent)
- Subscription: `job.<pool>` with queue group (e.g., `job.chat.simple` + `workers-chat`).
- Steps:
  1) Receive/consume `BusPacket{JobRequest}` from its job subject.
  2) Validate `job_id`, `topic`, `context_ptr`.
  3) Increment `active_jobs`; fetch context from Redis via `context_ptr`.
  4) Compute result (LLM/tool/orchestration).
  5) Store result at `res:<job_id>` in Redis → derive `result_ptr`.
  6) Publish `BusPacket{JobResult}` (`status=COMPLETED` or `FAILED`) to `sys.job.result`.
  7) Update `JobStore` (if applicable): set `result_ptr`, `COMPLETED/FAILED`.
  8) Decrement `active_jobs`.
- Heartbeats: emit periodically with accurate load/capacity.

### Agents (orchestrators, API Gateway)
- Must follow the same submission path: write context to Redis, publish `JobRequest` to `sys.job.submit`.
- Do not bypass scheduler/safety by publishing directly to `job.*`.
- Use `trace_id` end-to-end for correlation.
- Use `JobStore` and `result_ptr` to discover completion and fetch final payload from Redis.

## 6. Agent Requirements (Codex Checklist)
- [ ] Use `BusPacket` for all NATS traffic (no raw proto).
- [ ] Jobs: `job_id` non-empty/unique; `topic` maps to configured pool; `context_ptr` valid `redis://`; respect immutability.
- [ ] Results: `JobResult` carries `job_id`, `status` (COMPLETED/FAILED), `result_ptr` when available, `worker_id`, `execution_ms`.
- [ ] Heartbeats: emit regularly with `worker_id`, `pool`, `active_jobs`, `cpu_load`/`gpu_utilization`, `max_parallel_jobs`.
- [ ] Pool usage: subscribe to `job.<pool>` with queue group; set `pool` in heartbeat accordingly.
- [ ] Safety: do not bypass scheduler/safety; all jobs go through `sys.job.submit`.
- [ ] Redis pointers: write context/result to Redis; pass pointers (`context_ptr`, `result_ptr`) on the bus.
- [ ] Trace: preserve `trace_id` across JobRequest → JobResult.
- [ ] JobStore: update state/result_ptr when applicable (scheduler/workers/orchestrators).
- [ ] Alerts (optional): publish `SystemAlert` to `sys.alert`/`sys.alert.<component>` for errors.

When implementing a new agent, follow this:
1) Decide role: leaf worker, orchestrator, or API-style producer.
2) Map to a `job.<pool>` subject + queue group; set `pool` in heartbeats.
3) Wire NATS (`NatsBus`) and Redis (`RedisStore`) for `context_ptr`/`result_ptr`.
4) Implement job handler:
   - Validate request; fetch context from Redis.
   - Do work; write result to Redis; publish `JobResult`.
   - Manage `active_jobs` and heartbeats.
5) Preserve `trace_id`; keep `JobRequest` immutable; honor safety path via `sys.job.submit`.
6) For orchestrators: spawn child jobs via `sys.job.submit`, poll JobStore/Redis for completion, publish parent `JobResult`.

1. Connectors
- Use `internal/infrastructure/bus.NatsBus` to talk to NATS.
- Use `internal/infrastructure/memory.RedisStore` if you need context/result.
- Use `JobStore` if you manage job state (orchestrators, scheduler, API Gateway).

2. Subjects
- Jobs:
  - Submit to `sys.job.submit`.
  - Workers listen on `job.<pool>` with queue groups.
- Results: `sys.job.result` for `JobResult`.
- Heartbeats: `sys.heartbeat.<pool>` (scheduler uses wildcard).
- Alerts: `sys.alert` / `sys.alert.<component>`.
- Workers must subscribe only to the job subjects defined for their pool.
- Use queue groups (e.g., `workers-chat`) so multiple instances load-balance.

3. BusPacket usage
- All NATS traffic must be `BusPacket`; set:
  - `trace_id`
  - `sender_id`
  - `created_at`
  - `protocol_version=1`
  - `payload` = `JobRequest` / `JobResult` / `Heartbeat` / `SystemAlert`
- Preserve `trace_id` from request → result.
- Always send/receive `BusPacket`; do not send raw proto messages on NATS.
- For jobs: set `trace_id`, `sender_id`, `protocol_version`, `created_at`.
- Put your `JobRequest` / `JobResult` / `Heartbeat` in the `payload` oneof.

4. Context/result pointers
- Context pointers:
  - Store context JSON in Redis at `ctx:<job_id>` (or other key).
  - Set `context_ptr = "redis://ctx:<job_id>"`.
- Result pointers:
  - Store results in Redis at `res:<job_id>` (or other key).
  - Set `result_ptr = "redis://res:<job_id>"`.
- Never send large payloads over NATS; always use pointers.
- Never send large blobs over NATS.
- Always use `context_ptr` / `result_ptr` pointing to Redis keys.
 - Use helpers:
  - `memory.MakeContextKey(jobID)`
  - `memory.MakeResultKey(jobID)`
  - `memory.PointerForKey(key)`
  - `memory.KeyFromPointer(ptr)` (for reverse lookup)

5. Idempotency & safety
- Queue groups give at-most-once delivery per worker instance; duplicate publishes may still occur.
- Workers should tolerate occasional duplicate `JobRequest` (e.g., by checking result_ptr/state if persisted).
- Producers should avoid duplicate job_ids; if retrying, consider new `job_id`.
- Safety Kernel must be called by the scheduler; agents must not bypass it.
- Do not renumber proto fields; append new fields only.
- Jobs may be replayed in edge cases; design handlers to be resilient.
- Design worker logic to tolerate duplicate `JobRequest` for the same `job_id` (e.g., check if `res:<job_id>` already exists).

6. Heartbeats
- Emit on `sys.heartbeat.<pool>` at a steady interval (e.g., every 5s).
- Include: `worker_id`, `pool`, `active_jobs`, `cpu_load`, `gpu_utilization`, `max_parallel_jobs`, `capabilities`.
- `worker_id` must be unique per process instance.
- `pool` must match configured pool name and subscribed subject.
- Use append-only changes to the proto; do not renumber fields.
- Keep `active_jobs` accurate (increment/decrement around job processing).
- Fill `pool` and `max_parallel_jobs` correctly for scheduling decisions.

7. Error handling
- On worker failure:
  - Set `JobResult.status=FAILED`; optionally store error details in Redis and point `result_ptr` to them.
  - Publish `JobResult` to `sys.job.result`; decrement `active_jobs`.
  - Keep heartbeats running until shutdown.
- On safety deny:
  - Scheduler marks `DENIED` in `JobStore`; may publish a failure `JobResult` (optional).
- Use `SystemAlert` for critical errors to `sys.alert`/`sys.alert.<component>`.

On hard failure for a job:
- Publish `JobResult{status=FAILED}`; include `result_ptr` to error payload if available.
- Do not leave jobs without a `JobResult`.
- Optionally write an error payload to Redis and set `result_ptr` to it.
For fatal worker errors:
- Emit a `SystemAlert` if the worker must exit.
- Try to publish `JobResult{status=FAILED}` for in-flight jobs before shutdown if possible.
- Emit `SystemAlert{level=CRITICAL}` for fatal worker exits.

7. Extending the Protocol
- Append new fields to proto messages; never renumber existing fields.
- Keep `protocol_version` aligned with the current BusPacket format (v1).
- If adding new message types or subjects:
  - Document the subject, producers/consumers, payload, and purpose.
  - Prefer to reuse `BusPacket` envelope; add new oneof variants with new field numbers.
- Update docs and generated code (`make proto`) when proto files change.
- When extending Protobuf:
  - Never reuse or renumber existing field tags.
  - Append fields at the end with new tags.
  - Regenerate code (`make proto`) after edits.
- NEVER change field numbers.
- Add new fields with new IDs at the end of the message.
- Preserve backward compatibility with existing workers.
- When adding new subjects:
  - Document subject name, producers, consumers, payload type.
  - Use `BusPacket` with a new oneof variant if needed.
  - Add corresponding pool/queue mapping.
  - Avoid hardcoding new subjects across the codebase; centralize mappings.
    - Reserve subjects under:
      - `sys.*` for system control
      - `job.*` for schedulable jobs/worker pools
      - `agent.*` only if introducing direct agent-level topics later (not recommended now)

8. Summary
- Agents never call each other directly; everything flows through `sys.job.submit` → `job.*` → `sys.job.result` using `BusPacket`.
- Context/results live in Redis; pass pointers (`context_ptr`, `result_ptr`) instead of payloads on NATS.
- Scheduler mediates safety and routing (topic→pool), preserves `trace_id`, and updates `JobStore`.
- Workers/orchestrators handle jobs, maintain `active_jobs`, emit heartbeats, and always publish `JobResult` (success or failure).
- Protobuf changes are append-only; never renumber fields; update docs and regenerate code on changes.
- All communication is mediated by the control plane: producer → `sys.job.submit` → scheduler → `job.*` → worker → `sys.job.result`.
- Data lives in Redis via pointers (`context_ptr`, `result_ptr`); NATS only carries references/envelopes.
- Control flows via NATS `BusPacket` envelopes carrying `JobRequest` / `JobResult` / `Heartbeat` / `SystemAlert`.
- State is tracked in `JobStore` for statuses and result pointers.
- Leaf workers: subscribe to `job.*` for their pool, consume jobs, use `context_ptr`/`result_ptr`, do work, write results to Redis, publish `JobResult`, and send heartbeats.
- Orchestrator workers: consume a parent job, subscribe to workflow/job subjects, create child jobs via `sys.job.submit`, use `JobStore` to track children, aggregate child results, and emit a final parent `JobResult`.
- This document is the normative contract between any new agent implementation and the CortexOS control plane. All new workers and orchestrators MUST follow it.
