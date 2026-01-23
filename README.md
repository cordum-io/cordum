# Cordum - Deterministic Control Plane for Autonomous Workflows

[![License: BUSL-1.1](https://img.shields.io/badge/license-BUSL--1.1-blue)](LICENSE)
[![Release](https://img.shields.io/github/v/release/cordum-io/cordum?sort=semver)](https://github.com/cordum-io/cordum/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/cordum-io/cordum)](go.mod)
[![Docker Compose](https://img.shields.io/badge/compose-ready-0f766e)](docker-compose.yml)
[![Docs](https://img.shields.io/badge/docs-cordum--docs-0ea5e9)](docs/README.md)
![Docker Pulls](https://img.shields.io/docker/pulls/cordum/control-plane)
![CI](https://github.com/cordum-io/cordum/workflows/CI/badge.svg)
![CodeQL](https://github.com/cordum-io/cordum/workflows/CodeQL/badge.svg)
![Coverage Target](https://img.shields.io/badge/coverage-target%2080%25-22c55e)
[![Website](https://img.shields.io/badge/website-cordum.io-blue)](https://cordum.io)
[![WebsiteDocs](https://img.shields.io/badge/docs-cordum.io%2Fdocs-0ea5e9)](https://cordum.io/docs)
[![Discord](https://img.shields.io/badge/discord-join-5865F2?logo=discord&logoColor=white)](https://discord.gg/26yw9VQV)

Cordum (cordum.io) is a governance-first control plane for autonomous workflows: the API gateway accepts
jobs and workflow runs, the scheduler routes work and gates it through the Safety Kernel, and the workflow
engine coordinates run state and timelines. NATS provides the durable bus, Redis stores state and context/result
pointers, and CAP v2 wire contracts (from the CAP repo) define job envelopes, safety checks, and heartbeats so
external workers stay decoupled; packs add workflows, schemas, and policy/config overlays.


See the full product docs at [Cordum](https://cordum.io) (or the local `docs/README.md`).

## 2-minute guardrails demo

Run the approval + remediation demo (worker + policy gate + approval): `./tools/scripts/demo_guardrails.sh`

Walkthrough + GIF recording steps: `docs/demo-guardrails.md`

## Getting started (1 minute)

![Getting started](docs/assets/getting-started.gif)

Install (one-liner):

```bash
curl -fsSL https://get.cordum.io | sh
# or run locally from a clone:
./tools/scripts/install.sh
```

`get.cordum.io` should serve `tools/scripts/install.sh` from this repo.

1. `go run ./cmd/cordumctl up` (requires Go), or `docker compose build && docker compose up -d`.
2. Open `http://localhost:8082` (dashboard).
3. Run `./tools/scripts/platform_smoke.sh`.

## Feature highlights

- Workflow engine with retries/backoff, approvals, timeouts, delays, and crash-safe state.
- Least-loaded scheduling with capability-aware pool routing.
- Policy-before-dispatch (ALLOW/DENY/REQUIRE_APPROVAL/CONSTRAINTS).
- Pack overlays for workflows, schemas, and policy/config fragments.
- Durable job bus on NATS JetStream with Redis-backed pointers and auditability.
- API + CLI for workflows, runs, policy bundles, schemas, packs, locks, and artifacts.

## Architecture (current code)

Core services:
- API gateway: HTTP/WS + gRPC for jobs, workflows/runs, approvals, config, policy, DLQ, schemas, locks, artifacts, traces, packs.
- Scheduler: safety gate, routing, job state, reconciler timeouts.
- Safety kernel: policy check/evaluate/explain/simulate; file policy + config-service fragments.
- Workflow engine: Redis-backed workflows/runs with fan-out, approvals, retries/backoff, delay/notify/condition steps, reruns, timeline.
- Context engine (optional): gRPC helper for context windows and memory in Redis.
- Dashboard (optional): React UI served via Nginx; connects to `/api/v1` and `/api/v1/stream`.

Control plane flow (simplified):

```
Clients/UI
   |
   v
API Gateway  --->  Redis (runs, jobs, pointers, config, policy, DLQ)
   |
   v
Scheduler  --->  Safety Kernel (policy decision)
   |
   v
NATS (JetStream bus)  --->  External workers (your code)
```

Protocol:
- CAP v2 (repo: `github.com/cordum-io/cap`) defines the bus envelope, job/safety schemas, and compatibility rules; this repo aliases the types in `core/protocol/pb/v1`.
- API/Context protos live in `core/protocol/proto/v1`; generated Go types live in `core/protocol/pb/v1` and `sdk/gen/go/cordum/v1`.

SDK:
- Public Go SDK lives under `sdk/` (module `github.com/cordum/cordum/sdk`), including generated protos,
  a minimal gateway client, and a CAP worker runtime (`sdk/runtime`).

## Why Cordum?

Cordum is an open-source **control plane and safety layer** designed specifically for autonomous AI agents.

Think of it not as another AI framework (like LangChain or CrewAI) but as the **infrastructure** or
"firewall" that sits between your LLM's intent and your production systems. Its primary goal is to solve
the "Trust Gap" -- the hesitation enterprises have in letting non-deterministic AI agents execute actions
(writes) in production environments.

Here is a deep analysis of its architecture, capabilities, and strategic value.

### 1. The core philosophy: "Policy-Before-Dispatch"

Most agentic frameworks function by having the LLM directly call tools. Cordum inverts this.

* Traditional: LLM -> Tool Execution -> Result.
* Cordum: LLM -> Intent (Job) -> Safety Kernel (Policy Check) -> Dispatch to Worker -> Result.

This ensures that no matter how "jailbroken" or confused an LLM becomes, it cannot execute a dangerous
command (e.g., `DROP DATABASE`) if the hard-coded policy forbids it. The constraints are enforced at the
infrastructure level, not the prompt level.

### 2. Technical architecture

Cordum is built as a distributed system using industry-standard infrastructure components, making it
"production-ready" by design rather than a prototype toy.

#### A. The control plane (Go)

The core logic resides in a set of Go services:

* API Gateway: handles HTTP/gRPC requests for job submissions, workflow triggers, and config management.
* Safety Kernel: the brain of the operation. It intercepts every job dispatch attempt and evaluates it
  against defined policies (Allow, Deny, Require Approval, Constrain).
* Scheduler: a capability-aware dispatcher. It routes jobs to workers based on required capabilities
  (e.g., `requires: [gpu, production-access]`) and worker load, rather than simple round-robin.
* Workflow Engine: manages multi-step processes (DAGs), handling retries, backoffs, and state persistence.

#### B. The data plane (NATS + Redis)

* NATS JetStream: used as the nervous system. All communication between the control plane and workers
  happens over a durable message bus. This decouples the agent (the "brain") from the tools (the "hands"),
  allowing for asynchronous execution and better scalability.
* Redis: acts as the "heap" and state store. It holds the current state of workflows, payload pointers
  (references to data rather than large blobs on the wire), and locks.

#### C. The protocol: CAP v2

Cordum uses the **Cordum Agent Protocol (CAP)**.

* Purpose: it standardizes how agents talk to the world. Unlike MCP (Model Context Protocol), which
  focuses on providing context *to* an LLM, CAP focuses on execution.
* Wire format: defined in Protobuf, ensuring strict contracts for Jobs, Results, and Heartbeats.
* Language agnostic: because workers communicate via NATS + CAP, they can be written in any language
  (Python, Node.js, Go) and still be orchestrated by the central Cordum brain.

### 3. Key features

#### The Safety Kernel

This is the differentiator. You can define policies like:

* Human-in-the-loop: "Any job tagged `write` that targets `production` requires human approval."
* Rate limiting: "Max 50 API calls per minute for the `junior-dev-agent`."
* Input constraints: "The `recipient_email` field must match `@company.com`."

#### The Pack system

Cordum introduces "Packs" -- distributable bundles of functionality.

* A Pack can contain Workers (Docker containers), Workflows (definitions), and Policies.
* This allows teams to "install" capabilities (e.g., a "GitHub Triage Pack") that come pre-configured
  with the necessary safety guardrails, rather than writing them from scratch.

#### MCP native

Cordum supports the Model Context Protocol (MCP). It acts as a bridge, allowing standard MCP clients
(like Claude Desktop or IDEs) to trigger Cordum workflows. This means you can use your existing tools
while gaining the safety and audit benefits of Cordum's control plane.

### 4. Developer experience

* `cordumctl`: a CLI tool for managing the platform, inspecting runs, and handling packs.
* Dashboard: a web UI for visualizing workflow graphs, viewing run timelines (flight recorder), and
  managing approvals.
* SDKs: currently focused on Go, with patterns available for Python and Node workers.

### 5. Strategic analysis

| Feature | Cordum | Traditional agent frameworks |
| --- | --- | --- |
| Execution model | Deterministic, policy-gated | Probabilistic, direct execution |
| State management | Durable (Redis/NATS), resumable | Often in-memory / ephemeral |
| Security | Infrastructure-level (the "firewall") | Prompt-level (system prompts) |
| Target audience | Platform/DevOps engineers, enterprise | AI application developers |

**Pros:**

* Security-first: solves the biggest blocker to enterprise adoption.
* Auditability: every action, decision, and result is recorded.
* Reliability: uses NATS for at-least-once delivery, preventing "lost" agent actions.

**Cons / risks:**

* Complexity: requires running NATS and Redis, which is heavier than a simple Python library.
* Early stage: the project is in active development (v0.1.x), meaning APIs might evolve.
* Adoption friction: requires teams to rethink how they deploy agents (separating the "brain" from
  the "runner").

**Final verdict:**

Cordum is infrastructure for the post-prototype era of AI. If you are building a simple chatbot, it is
overkill. If you are building autonomous agents that are expected to perform real work (modifying databases,
merging code, processing payments) without constant supervision, Cordum provides the governance layer
to do so safely.

## Quickstart (Docker)

Requirements: Docker/Compose, curl, jq. Go is required if you want to use `cordumctl`.

```bash
go run ./cmd/cordumctl up
```

Or manually:

```bash
docker compose build
docker compose up -d
```

For prebuilt images:

```bash
export CORDUM_VERSION=v0.1.1
docker compose -f docker-compose.release.yml pull
docker compose -f docker-compose.release.yml up -d
```

## Kubernetes (Helm)

```bash
helm install cordum ./cordum-helm -n cordum --create-namespace
```

Published chart (when available):

```bash
helm repo add cordum https://charts.cordum.io
helm repo update
helm install cordum cordum/cordum -n cordum --create-namespace
```

Dashboard (optional): `http://localhost:8082` (uses `CORDUM_API_KEY`).

Platform smoke (create workflow + run + approve + delete):
```bash
./tools/scripts/platform_smoke.sh
```

CLI smoke (cordumctl) (requires `cordumctl` on PATH; build with `make build SERVICE=cordumctl` and add `./bin` to `PATH`):
```bash
./tools/scripts/cordumctl_smoke.sh
```

## Examples

- `examples/hello-pack` - minimal pack (workflow + schema + policy/config overlays)
- `examples/hello-worker-go` - Go worker that consumes `job.hello-pack.echo`
- `examples/python-worker` - Python worker example for `job.hello-pack.echo`
- `examples/node-worker` - Node worker example for `job.hello-pack.echo`
- `examples/demo-guardrails` - approval + remediation demo pack
- `cordum-packs/packs/mcp-bridge` - MCP stdio bridge + pack (packs monorepo)

## Docs

Start here:
- `docs/README.md` (docs index)
- `docs/system_overview.md` (architecture + data flow)
- `docs/CORE.MD` (deep technical reference)

Key guides:
- `docs/DOCKER.md` (compose + environment)
- `docs/quickstart.md` (hello world tutorial)
- `docs/AGENT_PROTOCOL.md` (bus + pointer semantics)
- `docs/pack.md` (pack format + install flow)
- `docs/LOCAL_E2E.md` (local e2e walkthrough)

Resources:
- `CONTRIBUTING.md`
- `SECURITY.md`
- `SUPPORT.md`
- `LICENSE`

## Repositories

- `cordum`: core control plane (this repo)
- `cordum-enterprise`: enterprise binaries (license check + enterprise auth provider)
- `cordum-packs`: official pack bundles + worker projects
- `cap`: protocol contracts and SDKs (`github.com/cordum-io/cap/v2`)

## Enterprise

Enterprise features are delivered by the `cordum-enterprise` repo and require a signed license.
Enterprise-only capabilities (available in enterprise binaries):
- Enterprise auth provider (multi-tenant API keys + RBAC)
- License enforcement on enterprise binaries

Enterprise offering includes (roadmap and commercial program):
- SSO/SAML integration
- SIEM and audit log export
- Dedicated support + SLA
- Custom pack development
- Managed or on-prem deployment assistance

## Development and tests

- Go toolchain: `go 1.24` (module-specified). Use local cache to avoid permission issues:
  `GOCACHE=$(pwd)/.cache/go-build go test ./...`
- Proto changes: edit `core/protocol/proto/v1`, then run `make proto`.
- Docker images: `docker compose build` rebuilds all services.
- Binaries: `make build` (all), or `make build SERVICE=cordum-scheduler` (one).
- Container image: `make docker SERVICE=cordum-scheduler` (uses the root Dockerfile).
- Smoke: `make smoke` (runs `tools/scripts/platform_smoke.sh`).
- Integration tests: `make test-integration` (opt-in, tagged).

## Observability

- Scheduler: `:9090/metrics`
- API gateway: `:9092/metrics`
- Workflow engine health: `:9093/health`

## Reset state (local)

- Wipe Redis (jobs + ctx/res + memory + workflows + config + DLQ):
  `docker compose exec redis redis-cli FLUSHALL`
- Wipe JetStream state too: `docker compose down -v` (removes `nats_data`) and then `docker compose up -d`

## License

Licensed under the Business Source License 1.1 (BUSL-1.1). Free for
self-hosted and internal use, but not for competing hosted/managed offerings.
See `LICENSE` for details and the Change Date.
