Perfect — we’ll start exactly the right way:
**with a fully structured, production-grade `README.md` / architecture specification document** that your Codex CLI can use as the root of the repo.

Below is **Version 1.0 of the CortexOS Technical Specification Document** — extremely detailed, structured, and ready to drop into GitHub as `docs/ARCHITECTURE.md` or `README.md`.

This document gives:

* Overview
* Architecture
* Components
* NATS subjects
* Worker lifecycle
* Safety Kernel spec
* Scheduler spec
* Memory protocol
* Deployment layout
* Future roadmap

It reads like a real internal engineering doc for a serious infra system.

---

# **CortexOS — AI Control Plane**

### **Version 1.0 — Technical Architecture Document**

---

## **1. Introduction**

CortexOS is an **AI Control Plane**:
A distributed operating system that **schedules, routes, constrains, and observes** probabilistic workloads (LLMs, solvers, tools) with **deterministic guarantees**.

CortexOS is built to solve the core enterprise problems in AI adoption:

* Non-deterministic output
* Poor reliability of agentic systems
* Lack of observability
* Lack of governance, RBAC, and policy enforcement
* Inefficient GPU utilization
* Fragile JSON-based workflows
* No way to treat LLMs as “compute resources”

CortexOS introduces:

* **Predictive Scheduling**
* **Safety Kernel (AI Firewall)**
* **Grammar-Constrained Inference**
* **NATS-Based Intelligence Bus**
* **Pointer-Based Memory Fabric**
* **Mixed-Compute Worker Model (LLM + Tools + Solvers)**
* **Auditable Execution Graphs**

This document defines the **V1 architecture** for CortexOS.

---

## **2. High-Level System Overview**

CortexOS is composed of 4 planes:

1. **Control Plane (CPU)** – scheduler, safety kernel, API gateway
2. **Compute Plane (GPU & CPU Workers)** – LLMs, solvers, tools
3. **Memory Fabric** – Redis + Vector DB
4. **Observability Plane** – audit logs + metrics + dashboards

### **Operational snapshot (current codebase)**
- Scheduler is pool/load-aware and now enforces a canonical job state machine: `PENDING → SCHEDULED → DISPATCHED → RUNNING → SUCCEEDED | FAILED | CANCELLED | TIMEOUT (+DENIED)`.
- State transitions are persisted in Redis with per-state indices and an event log; non-monotonic transitions are rejected.
- A reconciler loop in the scheduler marks DISPATCHED/RUNNING jobs as TIMEOUT after a window and keeps indices tidy.
- Pool routing is externalized in `config/pools.yaml` (override with `POOL_CONFIG_PATH`); compose mounts it into the scheduler.
- Workers (echo, chat, chat-advanced, code-llm, orchestrator) and Ollama service run via `docker compose up`; workflow demo exercises code-llm → chat-simple orchestration end-to-end.

---

## **3. Cluster Topology**

```
+------------------------------------------------------------+
|                        Kubernetes Cluster                  |
|                                                            |
|   +-----------------------------+   +--------------------+ |
|   | cpu-system Node Group       |   | gpu-llm Node Group | |
|   |-----------------------------|   |--------------------| |
|   | cortex-api-gateway          |   | worker-code-llm    | |
|   | cortex-scheduler            |   | worker-vision      | |
|   | cortex-safety-kernel        |   +--------------------+ |
|   | nats-jetstream (Stateful)   |                        |
|   | redis (Stateful)            |   +--------------------+ |
|   | weaviate / qdrant (Stateful)|   | cpu-tools Group    | |
|   | cortex-dashboard            |   |--------------------| |
|   | telemetry-collector         |   | worker-math        | |
|   +-----------------------------+   | worker-k8s-ops     | |
|                                    +--------------------+ |
+------------------------------------------------------------+
```

---

## **4. Core Components**

### **4.1 API Gateway**

* Exposes CortexOS externally via:

  * gRPC
  * HTTP/JSON for non-tech integrations
* Validates requests
* Transforms input into `JobRequest` packet
* Forwards to Scheduler

---

### **4.2 Scheduler**

Primary responsibilities:

1. **Worker Selection**
2. **Latency Prediction**
3. **Queue depth & GPU utilization analysis**
4. **Autoscaling triggers**
5. **Budget estimation**
6. **PolicyCheck initiation**

Scheduler flow:

```
API → Scheduler → Safety Kernel → Scheduler → NATS → Worker
```

Key logic:

```go
type Scheduler interface {
    Schedule(task TaskContext) (subject string, err error)
    Autoscale(queueMetrics QueueDepth) Action
}
```

Decision factors:

| Factor                  | Source         |
| ----------------------- | -------------- |
| Worker load             | Heartbeats     |
| GPU utilization         | Worker metrics |
| Queue backlog           | JetStream      |
| Estimated cost (tokens) | Model metadata |
| SLA target              | Job metadata   |

---

### **4.3 Safety Kernel (AI Firewall)**

**Nothing touches the Bus without Safety approval.**

Responsibilities:

* RBAC (roles/principals)
* Data security rules
* Budget enforcement
* Environment restrictions
* Action restrictions (kubectl delete, database writes, etc.)
* Compliance requirements
* Full audit logging to `sys.audit.event`

Kernel receives:

* JobMeta
* CostEstimate
* BudgetState

Kernel returns:

* `ALLOW`
* `ALLOW_MOD` (patched topic or restricted parameters)
* `DENY`
* `REQUIRE_HUMAN`
* `THROTTLE`

Security enforcement example:

```rego
deny[msg] {
    input.job_meta.environment == "prod"
    not input.principal.roles[_] == "SRE_LEAD"
    msg = "Non-SRE attempted prod action"
}
```

---

### **4.4 NATS JetStream (The Bus)**

Job routing is done using predictable subject patterns:

```
job.code.*
job.vision.*
job.math.*
job.k8s.*
sys.job.submit
sys.job.result
sys.audit.event
sys.registry.heartbeat
```

Properties:

* Extremely low overhead
* Built-in queue groups (load balanced workers)
* Persistent streams (JetStream)
* No HTTP complexity

CortexOS uses **Protobuf**, not JSON.

Workers subscribe to specific subject namespaces.

---

### **4.5 Memory Fabric**

CortexOS never passes large payloads over the bus.

All job and result data uses **pointers**:

```
context_ptr = "redis://ctx/<job_id>"
result_ptr  = "redis://res/<job_id>"
embeddings_ptr = "vector://collection/<id>"
```

### Redis

* Hot memory
* Job metadata
* Context blobs
* Results
* State transitions

### Vector DB (Weaviate / Qdrant)

* Semantic retrieval
* Long-term memory
* Knowledge stores

---

### **4.6 Compute Plane (Workers)**

Workers are stateless agents subscribed to NATS topics.

#### **4.6.1 LLM Workers**

* llama.cpp or vLLM backend
* Dynamic LoRA injection per task
* Grammar-constrained decoding ensures deterministic structure

#### **4.6.2 Vision Workers**

* LLaVA, CLIP, OpenCLIP

#### **4.6.3 Solver Workers (C++)**

* Fast deterministic math engines
* Pathfinding
* Routing
* Optimization

These replace LLM reasoning for tasks that require precision.

#### **4.6.4 Tool Workers (Go/Python)**

* Kubernetes operator (kubectl, helm, api calls)
* AWS/GCP APIs
* Internal devops tasks

These are strictly RBAC filtered.

---

### **4.7 Telemetry & Dashboard**

The observability plane aggregates:

* Job lifecycle
* Worker performance
* GPU utilization
* Safety Kernel decisions
* Audit logs
* Scheduling traces

Dashboard shows:

```
Job → Safety → Worker → Result
```

---

## **5. Job Lifecycle**

1. API receives caller request
2. Scheduler builds `JobMeta`
3. Scheduler calls Safety Kernel
4. Safety approves or denies
5. Scheduler chooses worker subject
6. Publish to NATS
7. Worker receives job
8. Worker fetches context from Redis
9. Worker executes
10. Worker writes result back
11. Worker publishes `JobResult`
12. Scheduler or caller receives result
13. Telemetry stores trace
14. Audit logs persistent events

---

## **6. Heartbeat Protocol**

Workers emit:

```
worker_id
worker_type
gpu_util
in_flight_jobs
adapter_loaded
supported_topics
latency_histogram
```

Scheduler maintains a real-time map of cluster capacity.

---

## **7. NATS Subject Architecture**

Example:

```
job.code.python
job.code.go
job.code.rust

job.vision.classify
job.vision.describe

job.math.optimize
job.math.route

job.k8s.ops
job.k8s.deploy
job.k8s.patch

sys.audit.event
sys.policy.check
sys.registry.heartbeat
sys.job.result
```

---

## **8. Kubernetes Deployment Layout**

### Namespaces

```
cortex-system
cortex-workers
cortex-observability
```

### Core Deployments

```
Deployment: cortex-api-gateway
Deployment: cortex-scheduler
Deployment: cortex-safety-kernel
StatefulSet: nats-jetstream
StatefulSet: redis
StatefulSet: weaviate
Deployment: cortex-dashboard
Deployment: telemetry-collector
```

### Worker Deployments

```
worker-code-llm (GPU)
worker-vision (GPU)
worker-math-solver (CPU)
worker-k8s-ops (CPU)
```

Each with autoscale options.

---

## **9. Security Architecture**

* Zero-trust routing
* Safety Kernel as mandatory gatekeeper
* All worker pods run in restricted namespaces
* Workers can only communicate with:

  * NATS
  * Redis
  * Vector DB
* No direct outside access
* All destructive actions require:

  * Role-based policies
  * Budget checks
  * Audit enforcement

---

## **10. Roadmap (V1 → V2)**

### **V1 (MVP)**

* Scheduler
* Safety Kernel
* LLM worker
* Tool worker
* Redis & NATS integration
* Dashboard (basic graph)

### **V2**

* Predictive autoscaler
* Multi-model routing
* Cross-cluster deployment
* Human approval workflow
* Policy Packs marketplace

### **V3**

* Self-healing jobs
* Model marketplace
* Distributed memory fabric
* Automatic chain optimization

---

# **End of Version 1.0**

## Developer quick actions

- Run tests locally with caches inside the repo: `GOMODCACHE=$(pwd)/.gomodcache GOCACHE=$(pwd)/.gocache /usr/local/go/bin/go test ./...`
- Bring up the full stack in containers: see `docs/DOCKER.md` (`docker compose up` with NATS, Redis, scheduler, safety, API, and workers).
- See a validated end-to-end snapshot (compose + sample jobs + result pointers) in `docs/LOCAL_E2E.md`.
