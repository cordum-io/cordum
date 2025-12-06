Got it. Let’s stop micro-tweaking and lay down a **single, end-to-end delivery plan** you can hand to Codex and just start executing.

Below is a **self-contained `MVP_DELIVERY_PLAN.md`**:

* Human readable ✅
* AI-friendly ✅ (clear phases, tasks, file paths, and interfaces)
* Modular from day 0 so V2/V3/V4 don’t require a rewrite ✅

You can drop this into `docs/MVP_DELIVERY_PLAN.md` and feed chunks to Codex CLI.

---

# CortexOS — MVP Delivery Plan (AI Control Plane)

**Goal:**
Deliver a **working MVP** of CortexOS — an AI Control Plane — that can:

* Accept jobs
* Run a Safety check (stub)
* Schedule jobs via NATS
* Execute them on workers
* Return results
  With **clean modular boundaries** so V2+ can extend without refactor hell.

We assume:

* Language: **Go** for control plane
* Bus: **NATS** (no JetStream complexity yet, just core NATS)
* Protocol: **Protobuf**
* Dev target: **local Docker + NATS**, not k8s yet (k8s comes after MVP)

---

## 0. MVP Definition (What “Good Enough” Means)

MVP is **DONE** when all of this is true:

1. **Job pipeline works end-to-end:**

   * A CLI / script publishes `JobRequest` → `sys.job.submit`.
   * Scheduler:

     * Receives
     * Safety-checks
     * Chooses a subject
     * Dispatches to a worker (`job.echo` / `job.code.python`).
   * Worker:

     * Processes
     * Publishes `JobResult` to `sys.job.result`.
   * Scheduler logs result.

2. **Heartbeats & basic scheduling exist:**

   * Workers send `Heartbeat` on `sys.heartbeat.*`.
   * Scheduler stores them and uses them (even in trivial way).

3. **Everything is Protobuf over NATS**:

   * No JSON on the bus.
   * `BusPacket` envelope with `oneof` for `JobRequest`, `JobResult`, `Heartbeat`.

4. **Modularity is in place**:

   * Scheduler depends on interfaces:

     * `Bus`
     * `SafetyChecker`
     * `WorkerRegistry`
     * `SchedulingStrategy`
   * Safety is pluggable (stub now, real service later).
   * Protocol is versioned (`cortex.v1`).

5. **Dev UX is clean**:

   * One command (`make dev-up` or simple instructions) starts NATS, Scheduler, Worker.
   * One script sends test jobs.

---

## 1. Repository Layout

Codex: create this structure.

```bash
cortex-core/
  api/
    proto/
      v1/                 # Public wire contracts (versioned)
  cmd/
    cortex-scheduler/     # Scheduler binary
    cortex-worker-echo/   # Example worker (MVP)
  internal/
    infrastructure/
      bus/                # NATS wrapper
      config/             # Simple config loader (YAML/env)
    scheduler/            # Engine, strategies, safety stub
    # safety/            # (Future: real Safety Kernel service/client)
  pkg/
    pb/
      v1/                 # Generated protobuf Go code
  config/
    local.yaml            # Dev config (NATS URL, routing)
  tools/
    scripts/              # Job sender, dev helpers
  docs/
    MVP_DELIVERY_PLAN.md
    ARCHITECTURE.md       # High-level design (later)
```

---

## 2. Phase Breakdown

### Phase 1 — Core Contracts & Build System

**Objective:**
Have a compiling repo with Protobuf contracts, generated Go code, and a working build.

#### Tasks

1. **Initialize Go module**

   * `go mod init github.com/cortex-os/core`

2. **Create directories** (if not exist)

   * `api/proto/v1`
   * `cmd/cortex-scheduler`
   * `cmd/cortex-worker-echo`
   * `internal/infrastructure/bus`
   * `internal/infrastructure/config`
   * `internal/scheduler`
   * `pkg/pb/v1`
   * `config`
   * `tools/scripts`
   * `docs`

3. **Define Protobuf contracts (v1)**

   `api/proto/v1/job.proto`:

   ```proto
   syntax = "proto3";

   package cortex.v1;
   option go_package = "github.com/cortex-os/core/pkg/pb/v1";

   enum JobPriority {
     JOB_PRIORITY_UNSPECIFIED = 0;
     JOB_PRIORITY_BATCH       = 1;
     JOB_PRIORITY_INTERACTIVE = 2;
     JOB_PRIORITY_CRITICAL    = 3;
   }

   enum JobStatus {
     JOB_STATUS_UNSPECIFIED = 0;
     JOB_STATUS_COMPLETED   = 1;
     JOB_STATUS_FAILED      = 2;
   }

   message JobRequest {
     string job_id = 1;
     string topic = 2;                // logical topic / NATS subject
     JobPriority priority = 3;
     string context_ptr = 4;          // "redis://jobs/{id}/context" (future)
     string adapter_id = 5;
     map<string, string> env_vars = 6;
     reserved 7, 8, 9;
   }

   message JobResult {
     string job_id = 1;
     JobStatus status = 2;
     string result_ptr = 3;           // "redis://jobs/{id}/result" (future)
     string worker_id = 4;
     int64 execution_ms = 5;
     reserved 6, 7, 8;
   }
   ```

   `api/proto/v1/heartbeat.proto`:

   ```proto
   syntax = "proto3";

   package cortex.v1;
   option go_package = "github.com/cortex-os/core/pkg/pb/v1";

   message Heartbeat {
     string worker_id = 1;
     string region    = 2;
     string type      = 3;              // "gpu", "cpu-tools", etc.

     float cpu_load        = 4;         // 0–100
     float gpu_utilization = 5;         // 0–100
     int32 active_jobs     = 6;

     repeated string capabilities = 7;   // ["echo", "python", "k8s"]
     reserved 8, 9, 10;
   }
   ```

   `api/proto/v1/packet.proto`:

   ```proto
   syntax = "proto3";

   package cortex.v1;
   option go_package = "github.com/cortex-os/core/pkg/pb/v1";

   import "google/protobuf/timestamp.proto";
   import "api/proto/v1/job.proto";
   import "api/proto/v1/heartbeat.proto";

   message BusPacket {
     string trace_id  = 1;
     string sender_id = 2;

     google.protobuf.Timestamp created_at = 3;

     uint32 protocol_version = 4;

     oneof payload {
       JobRequest job_request = 10;
       JobResult  job_result  = 11;
       Heartbeat  heartbeat   = 12;
       SystemAlert alert      = 13;
     }
   }

   message SystemAlert {
     string level     = 1; // "INFO", "WARN", "CRITICAL"
     string message   = 2;
     string component = 3;
   }
   ```

   > Codex: adjust `import` paths if protoc complains; worst case, change to `import "job.proto";` and include the folder in `-I`.

4. **Makefile for proto + builds**

   `Makefile`:

   ```make
   PROTO_SRC = api/proto/v1
   PB_OUT    = pkg/pb/v1

   proto:
   	protoc \
   		-I $(PROTO_SRC) \
   		--go_out=$(PB_OUT) --go_opt=paths=source_relative \
   		$(PROTO_SRC)/job.proto \
   		$(PROTO_SRC)/heartbeat.proto \
   		$(PROTO_SRC)/packet.proto

   build-scheduler: proto
   	go build -o bin/cortex-scheduler ./cmd/cortex-scheduler

   build-worker-echo: proto
   	go build -o bin/cortex-worker-echo ./cmd/cortex-worker-echo

   build: build-scheduler build-worker-echo

   .PHONY: proto build build-scheduler build-worker-echo
   ```

5. **Run:**

   * `make proto`
   * `go build ./...`

**Exit criteria Phase 1:**

* `.pb.go` files exist in `pkg/pb/v1`.
* `go build ./...` passes.

---

### Phase 2 — Bus Abstraction & Config

**Objective:**
Abstract NATS behind a simple interface and load basic config.

#### Tasks

1. **Bus interface (for modularity)**

   `internal/scheduler/types.go`:

   ```go
   package scheduler

   import pb "github.com/cortex-os/core/pkg/pb/v1"

   type Bus interface {
       Publish(subject string, packet *pb.BusPacket) error
       Subscribe(subject, queue string, handler func(*pb.BusPacket)) error
   }
   ```

2. **NATS implementation**

   `internal/infrastructure/bus/nats.go`:

   ```go
   package bus

   import (
       "log"

       "github.com/nats-io/nats.go"
       pb "github.com/cortex-os/core/pkg/pb/v1"
       "google.golang.org/protobuf/proto"
   )

   type NatsBus struct {
       nc *nats.Conn
   }

   func NewNatsBus(url string) (*NatsBus, error) {
       nc, err := nats.Connect(url)
       if err != nil {
           return nil, err
       }
       return &NatsBus{nc: nc}, nil
   }

   func (b *NatsBus) Close() {
       b.nc.Close()
   }

   func (b *NatsBus) Publish(subject string, packet *pb.BusPacket) error {
       data, err := proto.Marshal(packet)
       if err != nil {
           return err
       }
       return b.nc.Publish(subject, data)
   }

   func (b *NatsBus) Subscribe(subject, queue string, handler func(*pb.BusPacket)) error {
       _, err := b.nc.QueueSubscribe(subject, queue, func(m *nats.Msg) {
           var packet pb.BusPacket
           if err := proto.Unmarshal(m.Data, &packet); err != nil {
               log.Printf("Bus: failed to unmarshal: %v", err)
               return
           }
           handler(&packet)
       })
       return err
   }
   ```

3. **Config loader (minimal)**

   `config/local.yaml`:

   ```yaml
   nats_url: "nats://localhost:4222"
   ```

   `internal/infrastructure/config/config.go`:

   ```go
   package config

   import (
       "os"
   )

   type Config struct {
       NatsURL string
   }

   func Load() *Config {
       url := os.Getenv("NATS_URL")
       if url == "" {
           url = "nats://localhost:4222"
       }
       return &Config{
           NatsURL: url,
       }
   }
   ```

**Exit criteria Phase 2:**

* You can connect to NATS and compile the scheduler/worker with the Bus interface wired.

---

### Phase 3 — Scheduler Engine & Inline Safety Stub

**Objective:**
Implement the Scheduler with a **pluggable core** and a simple Safety check.

#### Tasks

1. **Scheduler interfaces (modular primitives)**

   Extend `internal/scheduler/types.go`:

   ```go
   package scheduler

   import pb "github.com/cortex-os/core/pkg/pb/v1"

   type SafetyDecision int

   const (
       SafetyAllow SafetyDecision = iota
       SafetyDeny
   )

   type SafetyChecker interface {
       Check(req *pb.JobRequest) (SafetyDecision, string)
   }

   type WorkerRegistry interface {
       UpdateHeartbeat(hb *pb.Heartbeat)
       Snapshot() map[string]*pb.Heartbeat
   }

   type SchedulingStrategy interface {
       PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error)
   }
   ```

2. **V1 implementations (simple)**

   `safety_stub.go`:

   ```go
   package scheduler

   import pb "github.com/cortex-os/core/pkg/pb/v1"

   type SafetyStub struct{}

   func NewSafetyStub() *SafetyStub {
       return &SafetyStub{}
   }

   func (s *SafetyStub) Check(req *pb.JobRequest) (SafetyDecision, string) {
       if req.Topic == "sys.destroy" {
           return SafetyDeny, "forbidden topic"
       }
       return SafetyAllow, ""
   }
   ```

   `registry_memory.go`:

   ```go
   package scheduler

   import (
       "sync"

       pb "github.com/cortex-os/core/pkg/pb/v1"
   )

   type MemoryRegistry struct {
       mu      sync.RWMutex
       workers map[string]*pb.Heartbeat
   }

   func NewMemoryRegistry() *MemoryRegistry {
       return &MemoryRegistry{
           workers: make(map[string]*pb.Heartbeat),
       }
   }

   func (r *MemoryRegistry) UpdateHeartbeat(hb *pb.Heartbeat) {
       r.mu.Lock()
       defer r.mu.Unlock()
       r.workers[hb.WorkerId] = hb
   }

   func (r *MemoryRegistry) Snapshot() map[string]*pb.Heartbeat {
       r.mu.RLock()
       defer r.mu.RUnlock()
       copy := make(map[string]*pb.Heartbeat, len(r.workers))
       for k, v := range r.workers {
           copy[k] = v
       }
       return copy
   }
   ```

   `strategy_naive.go`:

   ```go
   package scheduler

   import (
       "fmt"

       pb "github.com/cortex-os/core/pkg/pb/v1"
   )

   type NaiveStrategy struct{}

   func NewNaiveStrategy() *NaiveStrategy {
       return &NaiveStrategy{}
   }

   func (s *NaiveStrategy) PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error) {
       // V1: subject = topic (we assume workers subscribe directly to topic).
       if req.Topic == "" {
           return "", fmt.Errorf("empty topic")
       }
       return req.Topic, nil
   }
   ```

3. **Engine**

   `engine.go`:

   ```go
   package scheduler

   import (
       "log"

       pb "github.com/cortex-os/core/pkg/pb/v1"
       "google.golang.org/protobuf/types/known/timestamppb"
   )

   type Engine struct {
       bus      Bus
       safety   SafetyChecker
       registry WorkerRegistry
       strategy SchedulingStrategy
   }

   func NewEngine(bus Bus, safety SafetyChecker, reg WorkerRegistry, strat SchedulingStrategy) *Engine {
       return &Engine{
           bus:      bus,
           safety:   safety,
           registry: reg,
           strategy: strat,
       }
   }

   func (e *Engine) Start() error {
       if err := e.bus.Subscribe("sys.heartbeat.>", "cortex-scheduler", e.HandlePacket); err != nil {
           return err
       }
       if err := e.bus.Subscribe("sys.job.submit", "cortex-scheduler", e.HandlePacket); err != nil {
           return err
       }
       if err := e.bus.Subscribe("sys.job.result", "cortex-scheduler", e.HandlePacket); err != nil {
           return err
       }
       return nil
   }

   func (e *Engine) HandlePacket(p *pb.BusPacket) {
       switch payload := p.Payload.(type) {
       case *pb.BusPacket_Heartbeat:
           hb := payload.Heartbeat
           log.Printf("[SCHEDULER] 💓 worker_id=%s type=%s cpu=%.1f gpu=%.1f",
               hb.WorkerId, hb.Type, hb.CpuLoad, hb.GpuUtilization)
           e.registry.UpdateHeartbeat(hb)

       case *pb.BusPacket_JobRequest:
           req := payload.JobRequest
           log.Printf("[SCHEDULER] 📥 job_id=%s topic=%s", req.JobId, req.Topic)
           e.processJob(req, p.TraceId)

       case *pb.BusPacket_JobResult:
           res := payload.JobResult
           log.Printf("[SCHEDULER] ✅ job_id=%s status=%s worker_id=%s",
               res.JobId, res.Status.String(), res.WorkerId)
       }
   }

   func (e *Engine) processJob(req *pb.JobRequest, traceId string) {
       decision, reason := e.safety.Check(req)
       if decision == SafetyDeny {
           log.Printf("[SAFETY] ⛔ BLOCKED job_id=%s reason=%s", req.JobId, reason)
           return
       }

       workers := e.registry.Snapshot()
       subject, err := e.strategy.PickSubject(req, workers)
       if err != nil {
           log.Printf("[SCHEDULER] ERROR selecting subject job_id=%s: %v", req.JobId, err)
           return
       }

       log.Printf("[SCHEDULER] 🚀 dispatch job_id=%s subject=%s", req.JobId, subject)

       packet := &pb.BusPacket{
           TraceId:         traceId,
           SenderId:        "cortex-scheduler-0",
           CreatedAt:       timestamppb.Now(),
           ProtocolVersion: 1,
           Payload: &pb.BusPacket_JobRequest{
               JobRequest: req,
           },
       }

       if err := e.bus.Publish(subject, packet); err != nil {
           log.Printf("[SCHEDULER] ERROR publishing job_id=%s: %v", req.JobId, err)
       }
   }
   ```

4. **Scheduler main**

   `cmd/cortex-scheduler/main.go`:

   ```go
   package main

   import (
       "log"
       "os"
       "os/signal"
       "syscall"

       "github.com/cortex-os/core/internal/infrastructure/bus"
       "github.com/cortex-os/core/internal/infrastructure/config"
       "github.com/cortex-os/core/internal/scheduler"
   )

   func main() {
       log.Println("🧠 CortexOS Scheduler starting...")

       cfg := config.Load()

       natsBus, err := bus.NewNatsBus(cfg.NatsURL)
       if err != nil {
           log.Fatalf("failed to connect to NATS: %v", err)
       }
       defer natsBus.Close()

       engine := scheduler.NewEngine(
           natsBus,
           scheduler.NewSafetyStub(),
           scheduler.NewMemoryRegistry(),
           scheduler.NewNaiveStrategy(),
       )

       if err := engine.Start(); err != nil {
           log.Fatalf("failed to start engine: %v", err)
       }

       log.Println("✅ Scheduler running. Waiting for signals...")
       sigCh := make(chan os.Signal, 1)
       signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
       <-sigCh
       log.Println("Scheduler shutting down...")
   }
   ```

**Exit criteria Phase 3:**

* Scheduler starts, connects to NATS, subscribes to three subjects.
* Heartbeats / job requests / job results can be handled (once we have a worker & sender).

---

### Phase 4 — Worker Echo + Heartbeats

**Objective:**
Implement a simple worker to validate the flow.

#### Tasks

1. **Worker main**

   `cmd/cortex-worker-echo/main.go` (simplified):

   ```go
   package main

   import (
       "log"
       "math/rand"
       "os"
       "os/signal"
       "syscall"
       "time"

       "github.com/cortex-os/core/internal/infrastructure/bus"
       "github.com/cortex-os/core/internal/infrastructure/config"
       pb "github.com/cortex-os/core/pkg/pb/v1"
       "google.golang.org/protobuf/types/known/timestamppb"
   )

   const workerID = "worker-echo-1"

   func main() {
       rand.Seed(time.Now().UnixNano())
       log.Println("🔧 CortexOS Worker Echo starting...")

       cfg := config.Load()
       natsBus, err := bus.NewNatsBus(cfg.NatsURL)
       if err != nil {
           log.Fatalf("failed to connect to NATS: %v", err)
       }
       defer natsBus.Close()

       // Subscribe to job.echo
       if err := natsBus.Subscribe("job.echo", "workers-echo", handleJob(natsBus)); err != nil {
           log.Fatalf("failed to subscribe: %v", err)
       }

       // Periodic heartbeat
       go func() {
           ticker := time.NewTicker(5 * time.Second)
           defer ticker.Stop()
           for range ticker.C {
               hb := &pb.Heartbeat{
                   WorkerId:     workerID,
                   Region:       "local",
                   Type:         "cpu",
                   CpuLoad:      float32(rand.Intn(50)),
                   GpuUtilization: 0,
                   ActiveJobs:   0,
                   Capabilities: []string{"echo"},
               }
               pkt := &pb.BusPacket{
                   TraceId:         "hb-" + workerID,
                   SenderId:        workerID,
                   CreatedAt:       timestamppb.Now(),
                   ProtocolVersion: 1,
                   Payload: &pb.BusPacket_Heartbeat{
                       Heartbeat: hb,
                   },
               }
               if err := natsBus.Publish("sys.heartbeat.echo", pkt); err != nil {
                   log.Printf("failed to publish heartbeat: %v", err)
               }
           }
       }()

       log.Println("✅ Worker Echo running. Waiting for jobs and signals...")
       sigCh := make(chan os.Signal, 1)
       signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
       <-sigCh
       log.Println("Worker Echo shutting down...")
   }

   func handleJob(bus *bus.NatsBus) func(*pb.BusPacket) {
       return func(p *pb.BusPacket) {
           req := p.GetJobRequest()
           if req == nil {
               return
           }
           log.Printf("[WORKER echo] 📥 job_id=%s topic=%s context_ptr=%s",
               req.JobId, req.Topic, req.ContextPtr)

           start := time.Now()
           // Simulate work
           time.Sleep(time.Duration(100+rand.Intn(400)) * time.Millisecond)

           res := &pb.JobResult{
               JobId:       req.JobId,
               Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
               ResultPtr:   "memory://echo/" + req.JobId,
               WorkerId:    workerID,
               ExecutionMs: time.Since(start).Milliseconds(),
           }

           pkt := &pb.BusPacket{
               TraceId:         p.TraceId,
               SenderId:        workerID,
               CreatedAt:       timestamppb.Now(),
               ProtocolVersion: 1,
               Payload: &pb.BusPacket_JobResult{
                   JobResult: res,
               },
           }

           if err := bus.Publish("sys.job.result", pkt); err != nil {
               log.Printf("failed to publish result: %v", err)
           }
       }
   }
   ```

2. **Job sender script**

   `tools/scripts/send_echo_job.go`:

   ```go
   package main

   import (
       "log"
       "time"

       "github.com/cortex-os/core/internal/infrastructure/bus"
       "github.com/cortex-os/core/internal/infrastructure/config"
       pb "github.com/cortex-os/core/pkg/pb/v1"
       "github.com/google/uuid"
       "google.golang.org/protobuf/types/known/timestamppb"
   )

   func main() {
       cfg := config.Load()
       natsBus, err := bus.NewNatsBus(cfg.NatsURL)
       if err != nil {
           log.Fatalf("connect: %v", err)
       }
       defer natsBus.Close()

       jobID := uuid.NewString()
       traceID := uuid.NewString()

       req := &pb.JobRequest{
           JobId:    jobID,
           Topic:    "job.echo",
           Priority: pb.JobPriority_JOB_PRIORITY_INTERACTIVE,
           ContextPtr: "memory://echo_ctx/" + jobID,
       }

       pkt := &pb.BusPacket{
           TraceId:         traceID,
           SenderId:        "job-sender",
           CreatedAt:       timestamppb.Now(),
           ProtocolVersion: 1,
           Payload: &pb.BusPacket_JobRequest{
               JobRequest: req,
           },
       }

       if err := natsBus.Publish("sys.job.submit", pkt); err != nil {
           log.Fatalf("publish: %v", err)
       }
       log.Printf("Sent job job_id=%s trace_id=%s", jobID, traceID)

       // In MVP you’ll see logs in scheduler + worker.
       time.Sleep(1 * time.Second)
   }
   ```

**Exit criteria Phase 4:**

* With NATS + Scheduler + Worker running, you run `send_echo_job` and see:

  * Scheduler log job submission.
  * Worker log job handling.
  * Scheduler log job completion.

---

### Phase 5 — Dev Environment & Documentation

**Objective:**
Make this runnable by future-you in 30 seconds.

#### Tasks

1. **docker-compose.yml (optional but nice)**

   ```yaml
   version: "3.8"
   services:
     nats:
       image: nats:latest
       ports:
         - "4222:4222"
         - "8222:8222"
   ```

   For now you can run scheduler & worker natively (no containers yet).

2. **README / Docs**

   In `README.md`:

   * How to start NATS:

     * `docker-compose up -d nats`
   * How to run Scheduler:

     * `make build-scheduler && ./bin/cortex-scheduler`
   * How to run Worker:

     * `make build-worker-echo && ./bin/cortex-worker-echo`
   * How to send test job:

     * `go run ./tools/scripts/send_echo_job.go`
   * What you should see in logs.

3. **Log conventions**

   Stick to prefixes:

   * `[SCHEDULER] ...`
   * `[SAFETY] ...`
   * `[WORKER echo] ...`

**Exit criteria Phase 5 (MVP COMPLETE):**

* Single machine, local NATS.
* Scheduler & worker binaries built.
* `send_echo_job` sends a job, flow is visible in logs, trace_id propagates end-to-end.
* Adding new worker is just:

  * new `cmd/cortex-worker-XYZ`
  * subscribe to `job.xyz`
  * optionally send heartbeat.

---

## 3. AI-Friendly Summary Block (for Codex / Agents)

You can feed this block to Codex to drive work step-by-step:

```json
{
  "project": "CortexOS",
  "mvp_goal": "AI Control Plane MVP: scheduler + safety stub + NATS bus + echo worker + end-to-end JobRequest->JobResult",
  "phases": [
    {
      "name": "Phase 1 - Contracts & Build",
      "focus": "Proto contracts and Makefile",
      "key_dirs": ["api/proto/v1", "pkg/pb/v1", "cmd/*", "internal/*"],
      "tasks": [
        "Create job.proto, heartbeat.proto, packet.proto under api/proto/v1 with cortex.v1 package and go_package.",
        "Set up Makefile target 'proto' using protoc to generate Go to pkg/pb/v1.",
        "Run make proto and go build ./..."
      ]
    },
    {
      "name": "Phase 2 - Bus & Config",
      "focus": "NATS wrapper and simple config loader",
      "tasks": [
        "Define Bus interface in internal/scheduler/types.go.",
        "Implement NatsBus in internal/infrastructure/bus/nats.go with Publish/Subscribe using protobuf.",
        "Implement config loader reading NATS_URL or default.",
        "Verify connection to NATS."
      ]
    },
    {
      "name": "Phase 3 - Scheduler Engine + Safety Stub",
      "focus": "Engine, SafetyChecker, WorkerRegistry, SchedulingStrategy",
      "tasks": [
        "Define SafetyChecker, WorkerRegistry, SchedulingStrategy interfaces.",
        "Implement SafetyStub, MemoryRegistry, NaiveStrategy.",
        "Implement Engine.Start and Engine.HandlePacket with subscriptions to sys.heartbeat.>, sys.job.submit, sys.job.result.",
        "Wire in processJob with safety check, strategy-based subject selection, and Publish."
      ]
    },
    {
      "name": "Phase 4 - Worker Echo + Heartbeats",
      "focus": "First worker and end-to-end flow",
      "tasks": [
        "Create cortex-worker-echo main that subscribes to job.echo and responds with JobResult on sys.job.result.",
        "Send periodic Heartbeat from worker to sys.heartbeat.echo.",
        "Create send_echo_job.go that publishes JobRequest to sys.job.submit.",
        "Run NATS + scheduler + worker + sender and verify logs."
      ]
    },
    {
      "name": "Phase 5 - Dev UX & Docs",
      "focus": "Make it easy to run",
      "tasks": [
        "Add docker-compose with NATS.",
        "Document run instructions in README.md.",
        "Ensure one command or short sequence can start NATS, scheduler, worker, and send a sample job."
      ]
    }
  ]
}
```

---

This gives you a **clear, modular path** from zero to a real MVP.

Next step for you:

* Create the repo, paste this doc into `docs/MVP_DELIVERY_PLAN.md`,
* Then start feeding **Phase 1** to Codex CLI and let it generate the `proto` files + Makefile.
