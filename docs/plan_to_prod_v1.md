Got you. Let’s zoom out and lay down a **clean, production-grade plan for CortexOS**, exactly in the architecture we defined earlier (AI Control Plane, Safety Kernel, Memory Fabric, Scheduler, Bus, Workers, Observability).

I’ll keep it **end‑to‑end and concrete**, not theory, and I’ll show **where pub/sub lives** in that story.

---

# CortexOS – Production System Plan (Not MVP)

You are **here now**:

* NATS bus
* Protobuf contracts
* Scheduler skeleton (stub safety + naive strategy)
* Echo worker
* Local dev flow + tests

We want a **production control plane** that:

* Accepts real prompts via HTTP/gRPC
* Persists context/results via Redis (context_ptr/result_ptr)
* Enforces policies via a Safety Kernel
* Schedules across worker pools based on load/capabilities
* Uses NATS as a **proper pub/sub bus** (with clear subjects, queue groups; JetStream optional at first)
* Has metrics, logging, and is deployable to K8s

Below is the plan in **7 stages**. Each stage builds on what you already have.

---

## STAGE 1 – Lock In the Core Bus + Subjects (Your Pub/Sub System)

**Goal:** Make the NATS layer explicit, clean, and production‑ready. This is your pub/sub system.

Right now you have subjects:

* `sys.job.submit`
* `sys.job.result`
* `sys.heartbeat.>`
* `job.echo`

### 1.1 Define the Bus Contract (Subjects + Semantics)

Freeze this as v1:

* **Submission:** `sys.job.submit`
  – API Gateway publishes new jobs here.

* **Results:** `sys.job.result`
  – Workers publish job results here.

* **Heartbeats:** `sys.heartbeat.<worker-id>`
  – Workers publish health here (no persistence needed).

* **Worker Pools:** `job.<pool>.<kind>` e.g.

  * `job.echo`
  * `job.code.python`
  * `job.chat.llm`
  * `job.k8s.ops`

### 1.2 NATS semantics

* **Queue groups for workers**

  * All echo workers join queue `workers-echo` on `job.echo`.
  * All code workers join queue `workers-code` on `job.code.python`.

* **Scheduler**

  * Subscribes to:

    * `sys.job.submit` (queue `cortex-scheduler`)
    * `sys.job.result` (queue `cortex-scheduler`)
    * `sys.heartbeat.>` (plain sub)

> For now: **plain NATS** with queue groups is enough.
> Later you can add JetStream streams for persistence.

### 1.3 Hardening

* Add constants for subjects in a central package, no string literals sprinkled everywhere.
* Harden NATS connection (you already started: reconnect, logging).
* Tests: integration test that:

  * Sender → `sys.job.submit` → Scheduler → Worker → `sys.job.result`.

**Deliverable:** `docs/BUS_PROTOCOL.md` and a `bus` package with all subject names + semantics.

---

## STAGE 2 – Memory Fabric (Redis) For Prompts & Results

**Goal:** Make `context_ptr` / `result_ptr` real and standard. This is where **user prompts live** in production.

### 2.1 Redis Store

Create `internal/infrastructure/memory/redis_store.go`:

* Interface:

```go
type Store interface {
    PutContext(ctx context.Context, key string, data []byte) error
    GetContext(ctx context.Context, key string) ([]byte, error)
    PutResult(ctx context.Context, key string, data []byte) error
    GetResult(ctx context.Context, key string) ([]byte, error)
}
```

* Implementation: Redis using `REDIS_URL` from config (host:port, DB).

### 2.2 Wire Echo Path Through Redis

* **Sender / API (for now still `send_echo_job.go`):**

  * Build JSON payload: `{"prompt":"hello","metadata":{...}}`.
  * `ctxKey := "ctx:" + jobID`.
  * `memory.PutContext(ctx, ctxKey, payloadBytes)`.
  * Set `context_ptr = "redis://" + ctxKey` in `JobRequest`.

* **Echo Worker:**

  * Parse `context_ptr`.
  * Fetch context from Redis.
  * Log it and/or transform.
  * `resKey := "res:" + jobID`.
  * `memory.PutResult(ctx, resKey, resultBytes)`.
  * Set `result_ptr = "redis://" + resKey` in `JobResult`.

Now, **user prompt = stored in Redis**, accessed via `context_ptr`. That’s the real-world behavior.

**Deliverable:** `internal/infrastructure/memory` package + tests, echo path using Redis end-to-end.

---

## STAGE 3 – API Gateway (Real-World Entry Point)

**Goal:** Replace “sender script as entrypoint” with a real API where UI / ChatGPT / Ollama can send prompts.

### 3.1 API Proto

Add `api/proto/v1/api.proto`:

* `SubmitJob` → async job submission
* `GetJobStatus` → read status/result pointer

Example:

```proto
service CortexApi {
  rpc SubmitJob(SubmitJobRequest) returns (SubmitJobResponse);
  rpc GetJobStatus(GetJobStatusRequest) returns (GetJobStatusResponse);
}

message SubmitJobRequest {
  string prompt     = 1;
  string topic      = 2; // e.g. "job.chat.llm" or "job.echo"
  string adapter_id = 3;
  string priority   = 4; // "interactive", "batch"
}

message SubmitJobResponse {
  string job_id   = 1;
  string trace_id = 2;
}

message GetJobStatusRequest {
  string job_id = 1;
}

message GetJobStatusResponse {
  string job_id     = 1;
  string status     = 2; // map from JobStatus enum
  string result_ptr = 3; // may be empty if not ready
}
```

### 3.2 API Gateway Binary

`cmd/cortex-api-gateway/main.go`:

* On `SubmitJob`:

  * Generate `job_id`, `trace_id`.
  * Serialize `{prompt, adapter_id, priority}` to JSON.
  * Store in Redis as `ctx:job_id`.
  * Build `JobRequest` with `context_ptr = "redis://ctx:job_id"`.
  * Publish `BusPacket{JobRequest}` to `sys.job.submit`.
  * Return `job_id`, `trace_id`.

* On `GetJobStatus`:

  * Read job state from a JobStore (Stage 5).
  * If `COMPLETED`: return `status` + `result_ptr`.

**Deliverable:** Real HTTP/gRPC front door that any UI can hit.

---

## STAGE 4 – Safety Kernel as a Service

**Goal:** Move from inline safety stub to a **Safety Kernel service** that the scheduler *must* consult before dispatch.

### 4.1 Safety Proto

`api/proto/v1/safety.proto`:

```proto
service SafetyKernel {
  rpc Check(PolicyCheckRequest) returns (PolicyCheckResponse);
}

enum DecisionType {
  DECISION_TYPE_UNSPECIFIED = 0;
  DECISION_TYPE_ALLOW       = 1;
  DECISION_TYPE_DENY        = 2;
}

message PolicyCheckRequest {
  string job_id = 1;
  string topic  = 2;
  string tenant = 3; // optional, for later
}

message PolicyCheckResponse {
  DecisionType decision = 1;
  string reason         = 2;
}
```

### 4.2 Safety Kernel Binary

`cmd/cortex-safety-kernel/main.go`:

* gRPC server implementing `Check`:

  * For now:

    * Deny `topic == "sys.destroy"`.
    * Allow everything else.
* Log every decision.
* Later you plug OPA/Rego in here.

### 4.3 Scheduler Integration

In scheduler:

* Replace `SafetyStub` with `SafetyClient`:

  * `Check(req)` → gRPC call.
  * On `DENY`: log and drop.
  * On timeout: treat as `DENY` (fail-safe).

**Deliverable:** Network‑enforced policy gate between Scheduler and Bus. That’s the **Firewall** we designed earlier.

---

## STAGE 5 – Scheduler V2 (Pools, Load, State)

**Goal:** Turn the scheduler into a **real brain**, not a router.

You were right: current scheduler is not whole. Here’s how to finish it.

### 5.1 Pool & Capability from Heartbeats

Extend `Heartbeat` proto (append fields only):

```proto
string pool             = 8; // "echo", "code-python", "chat-llm"
int32 max_parallel_jobs = 9;
```

Workers send:

* `pool = "echo"`
* `max_parallel_jobs = 4` (for example)

### 5.2 WorkerRegistry Upgrades

Registry must support:

* `UpdateHeartbeat(hb)`
* `Snapshot() map[workerId]*Heartbeat`
* Optional: `WorkersForPool(pool) []*pb.Heartbeat`

### 5.3 Topic → Pool Mapping

Config file: `config/routing.yaml`:

```yaml
routing:
  pools:
    echo:
      topics: ["job.echo"]
    code-python:
      topics: ["job.code.python"]
    chat-llm:
      topics: ["job.chat.llm"]
```

Scheduler uses this to map `JobRequest.Topic` to pool.

### 5.4 Least-Loaded Strategy

Replace `NaiveStrategy` with `LeastLoadedStrategy`:

* For given `JobRequest`:

  * Find pool for topic.
  * Get workers in that pool.
  * Compute score per worker (active_jobs, cpu_load, gpu_util).
  * Select best worker.

You still **publish to `job.<pool>...`** as NATS subject; NATS queue groups handle which worker instance gets it. The strategy decides **which pool**, NATS handles **which exact pod**.

### 5.5 Job State Tracking

Add a `JobStore` (can be Redis-backed) in the scheduler:

* `SetState(jobID, state)` where state ∈ {PENDING, RUNNING, COMPLETED, FAILED, DENIED}
* On:

  * submission → PENDING
  * dispatch → RUNNING
  * result → COMPLETED/FAILED
  * safety deny → DENIED

API Gateway’s `/GetJobStatus` now reads from JobStore.

**Deliverables:**

* Pool-aware, least-loaded scheduler
* Job state persisted (in Redis)
* Realistic routing, not just `topic = subject`

---

## STAGE 6 – Observability & Metrics

**Goal:** Make this debuggable and monitorable in production.

### 6.1 Metrics

Introduce a `Metrics` interface:

```go
type Metrics interface {
    IncJobsReceived(topic string)
    IncJobsDispatched(topic string)
    IncJobsCompleted(topic string, status string)
    IncSafetyDenied(topic string)
}
```

Implement Prometheus-based metrics in `internal/infrastructure/metrics`, expose `/metrics` in scheduler and workers.

### 6.2 Logging

* Add a tiny logging wrapper that always logs:

  * `trace_id`
  * `job_id`
  * component (`scheduler`, `worker-echo`, `safety-kernel`)

Format:

`[SCHEDULER] job_received trace_id=... job_id=... topic=...`

### 6.3 Minimal Dashboard (later)

On k8s you wire:

* Prometheus → Grafana
* Dashboards:

  * Jobs per topic
  * Scheduler errors
  * Worker pool utilization
  * Safety denies

---

## STAGE 7 – Packaging & K8s

**Goal:** Run this as a real system, not 3 local processes.

### 7.1 Dockerfiles

For each binary:

* `cortex-api-gateway`
* `cortex-scheduler`
* `cortex-safety-kernel`
* Workers (echo, later LLM, k8s ops)

### 7.2 K8s Manifests

Namespaces:

* `cortex-system` → NATS, Redis, scheduler, safety, api-gateway
* `cortex-workers` → workers

Manifests:

* Deployments for each binary
* Service for:

  * api-gateway (ClusterIP + Ingress)
  * safety-kernel (ClusterIP)
  * scheduler (ClusterIP; internal only)
* ConfigMaps / Secrets for NATS URL, REDIS_URL, SAFETY_KERNEL_ADDR

### 7.3 Scaling

* HPA on:

  * worker Deployments (based on CPU or custom metrics)
  * scheduler (if needed)
* Node selectors for GPU workers later.

---

## TL;DR – Production Plan in One List

From where you are **today**, in this order:

1. **Bus Protocol Finalization (You mostly have this)**

   * Document subjects, queue usage, and NATS config.

2. **Memory Fabric (Redis) – real `context_ptr` / `result_ptr`**

3. **API Gateway – `/SubmitJob`, `/GetJobStatus`**

4. **Safety Kernel Service – gRPC Check + scheduler client**

5. **Scheduler V2 – pools, least-loaded strategy, job states in Redis**

6. **Observability – Prometheus metrics + structured logs**

7. **Packaging – Docker + K8s manifests (dev/prod clusters)**

That gets you from “nice MVP skeleton” to a **real, deployable AI control plane**.

If you want, next I can turn this into a `PRODUCTION_PLAN.md` with bullet‑level tasks per stage that you can drop into the repo and feed directly to Codex to implement step-by-step.
