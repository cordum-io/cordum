# Coretex Platform Core: Must‑Have Features (Pack‑Ready)

This document defines what **must live in platform core** so that future **packages** (e.g., *SRE Investigator*, *MCP adapters*, *repo tooling*, etc.) can be installed and run **without touching core code**.

**Design goal:** core provides **governance + runtime primitives**. Packages provide **domain logic** (workers, connectors, workflows, UIs).

**Protocol:** CAP v2 is the canonical wire contract for bus and safety messages.

---

## Principles

### Core is “boring”
Core knows only:
- jobs, workflows, state, pointers, config, policy, audit
- scheduling, retries, timeouts, DLQ
- approvals, budgets, and constraints

Core must **not** know:
- Kubernetes, GitHub/GitLab, Datadog/Coralogix, Sentry, LLM providers
- “incident”, “PR”, “runbook”, “patch generation” semantics
- tool-specific topics or behavior

### Packages are “installable behaviors”
A package is an **overlay** on the platform:
- adds *topics*, *workers*, *workflow templates*, and *config/policy overlays*
- uses core APIs/contracts exactly as-is
- never requires core code changes for “new product logic”

---

# P0 — Non‑Negotiable Core  
*(without these, packages will be hacks)*

## 1) Stable protocol contract (jobs + results + pointers)

**Status:** Implemented using CAP v2 (`github.com/coretexos/cap/v2`) with aliases in `core/protocol/pb/v1`.

Core must define and enforce:

- **BusPacket (CAP envelope)**
  - `trace_id, sender_id, created_at, protocol_version`
  - `payload` oneof includes `JobRequest`, `JobResult`, `Heartbeat`, `JobProgress`, `JobCancel`, `SystemAlert`
- **Pointers**
  - `ContextPointer / ResultPointer / ArtifactPointer` (store references, not big blobs)
- **Control events**
  - `JobCancel + Heartbeat + JobProgress`
- **DLQ format**
  - `error_code + error_message + last_state + attempts` (stored in DLQ entries)

**Why packages need it:** every worker and external integration becomes predictable, replayable, auditable.

**Hard rule:** version the envelope (`protocol_version`) so packages don’t break as the platform evolves.

### How packages use this (without touching core)
- A package defines new `topic`s (e.g., `job.sre.collect.k8s`, `job.sre.patch.generate`).
- Workers subscribe to those topics and **always** speak `BusPacket{JobRequest/JobResult}`.
- Workers write outputs to `ResultPointer` or `ArtifactPointer`.
- Core doesn’t need to “know” the meaning of the outputs.

---

## 2) Workflow Engine as a deterministic state machine

**Status:** Implemented in `core/workflow` and `cmd/coretex-workflow-engine`.

Core workflow engine must support **vanilla steps** that don’t require packages:

- `approval` (human gate)
- `delay` (timer)
- `condition` (evaluated expression, boolean output)
- `notify` (emits a SystemAlert on the bus)
- `worker` (dispatches a job to a topic/pool)
- `depends_on` DAG dependencies (steps run when all deps succeed; independent steps run in parallel)

Required properties:
- durable run state (crash/restart safe)
- step retries with backoff + max attempts
- timeouts per step
- cancel propagation (run cancel stops running steps)
- full run timeline (inputs/outputs pointers, status transitions)
- schema validation for workflow input and step input/output
- rerun-from-step and dry-run mode
 - dependency gating: failed/cancelled/timed-out deps block downstream steps (no implicit continue-on-error)

**Why packages need it:** packages are just workflows + workers. If the workflow engine isn’t bulletproof, the “Incident→PR” product will be unreliable.

### How packages use this (without touching core)
- A package ships **workflow templates** that contain `worker` steps pointing to the package’s topics.
- Core executes the same state machine regardless of what the steps *mean*.
- If the package is uninstalled, those topics simply become unmapped and will DLQ.

---

## 3) Scheduler that is purely config‑driven (no hardcoded “core topics”)

**Status:** Implemented; routing comes from `config/pools.yaml` (topics + pool capabilities).

Core scheduler must do only:
- topic → pool mapping (from config)
- leasing/dispatch semantics
- timeouts, retries, DLQ
- pool backpressure (overload detection)

If no mapping exists: fail fast to DLQ with `no_pool_mapping`.

**Why packages need it:** installing a package becomes “add mapping + deploy workers”, not “change scheduler code”.

### How packages use this (without touching core)
- Package installation adds **config overlays**:
  - `pools.overlay.yaml` (topic → pool)
  - `timeouts.overlay.yaml` (step/job timeouts)
- Workers come online in that pool.
- Scheduler behavior remains unchanged.

---

## 4) Safety Kernel as the single Policy Decision Point

**Status:** Implemented; gRPC `Check/Evaluate/Explain/Simulate` with snapshotting.

Core kernel must evaluate a request and return:
- `ALLOW`
- `DENY`
- `REQUIRE_APPROVAL`
- `ALLOW_WITH_CONSTRAINTS`  
  *(rewrite budgets, sandbox, command allowlist, redaction level)*

Policy management (P0 minimum):
- policy bundles loaded from file/URL + config service fragments (`cfg:system:policy` bundles)
- config-service bundles may include metadata (`author`, `message`, timestamps) and an `enabled` toggle; SecOps overlays live under the `secops/` prefix
- signed + hot reload to new `PolicySnapshot(version, hash)`
- last-known-good fallback if verification fails
- decision audit record for every request:  
  `{rule_id, version, decision, constraints, reason}`

Safety kernel config-service source can be tuned via `SAFETY_POLICY_CONFIG_SCOPE`,
`SAFETY_POLICY_CONFIG_ID`, `SAFETY_POLICY_CONFIG_KEY` (or disabled with `SAFETY_POLICY_CONFIG_DISABLE=1`).

**Why packages need it:** without this, “safe autopatcher” is marketing, not reality.

### How packages use this (without touching core)
- Packages do **not** implement security. They declare:
  - topics/tools they need
  - capability labels + risk tags
- SecOps installs policy overlays that:
  - allow/deny specific capabilities
  - require approvals for risky actions (prod writes, PR creation, shell exec)
  - impose constraints (max diff size, deny-paths, network restrictions)
- Kernel gates every job/run/tool call **before execution**.

---

## 5) Config Service with overlays (the pack hook)

**Status:** Implemented; Redis-backed merge with version/hash snapshot.

Even before you “do packages”, core must support overlay config:
- base config (platform)
- optional fragments (future packages)

Must support:
- merged “effective config” snapshot with a version/hash
- live reload with rollback (scheduler reloads pools/timeouts)
- per-tenant overrides (later)

**Why packages need it:** package install becomes “drop overlay files”, not edit core config manually.

### How packages use this (without touching core)
- A package ships overlays:
  - routing (`pools`)
  - policy fragments (stored under `cfg:system:policy` bundles)
  - budgets/timeouts
  - optional schema registrations
- Installer merges overlays into config service.
- Core reads the “effective config” snapshot and behaves accordingly.

---

## 6) Worker Runtime SDK (even if you ship zero workers)

**Status:** Implemented in `sdk/runtime` (CAP worker runtime).

Core should ship a tiny Go library that defines:
- how a worker connects/subscribes
- how it acks/extends lease
- how it emits progress + heartbeats
- how it writes results as pointers
- how it handles cancel signals

**Why packages need it:** consistent worker behavior + fewer “mystery outages”.

### How packages use this (without touching core)
- Package worker repos import the SDK.
- Upgrades become predictable (protocol_version + SDK versioning).
- Core doesn’t need to change for every new worker.

---

## 7) Control‑plane APIs (gateway) that are pack‑agnostic

**Status:** Implemented in `cmd/coretex-api-gateway` (HTTP/WS + gRPC).

At minimum:

- Workflows: `create/list/get/delete`
- Runs: `start/get/list/cancel/delete`, `rerun`, `timeline`
- Approvals: job approvals + workflow step approvals
- Jobs: `submit/status/get result pointer`
- DLQ: `list/retry/delete`
- Policy: `evaluate/simulate/explain` + snapshot list
- Config: `get/set/effective`
- Schemas: register/get/list/delete
- Locks: acquire/release/renew/get
- Artifacts: put/get
- Audit: decisions + run timeline

**Why packages need it:** packages use the same APIs for operations, UI, and integrations.

### How packages use this (without touching core)
- A package registers workflow templates (optional).
- A package (or an external client) triggers runs via the gateway.
- Ops tooling uses the same APIs to debug failures and inspect evidence pointers.

---

# P1 — Needed for first real packages  
*(SRE Investigator + MCP adapter)*

## 8) Artifact store abstraction (Redis now, pluggable later)

**Status:** Implemented with a Redis-backed store and retention classes.

You need a standard interface:
- `PutArtifact(content, metadata) -> artifact_ptr`
- `GetArtifact(ptr)`

Support:
- size limits
- TTL/retention classes (e.g., 7d/30d)
- optional encryption at rest (later)

**Why packages need it:** logs, test outputs, diffs, evidence = artifacts. Don’t shove them into Redis ctx.

### Package usage model
- SRE package stores “evidence bundle” as artifacts (log tails, kubectl output, CI logs).
- PR summaries link artifacts by pointer.
- Core remains unchanged; only the artifact storage backend may be swapped later.

---

## 9) Secrets reference model (never pass secrets as plaintext)

**Status:** Partially implemented: `secret://` detection + redaction helpers, policy enforcement via risk tags/labels.

Core must support:
- “secret refs”  
  e.g., `secret://vault/path#key` or `secret://k8s/ns/name`
- redaction utility for logs/evidence before LLM
- kernel rules can block flows if `secrets_present` detected

**Why packages need it:** SRE investigator touches logs and env. This is where you get burned.

### Package usage model
- Workers never read raw secrets unless policy allows and runner profile permits.
- Evidence is redacted before it becomes an artifact or LLM input.
- Kernel constraints enforce “no secret material to LLM”.

---

## 10) Capability‑based routing (not just topic→pool)

**Status:** Implemented via pool capability profiles (`config/pools.yaml`) and `JobMetadata.requires`.

Extend scheduler mapping to support constraints:
- pool requires: `docker`, `git`, `kubectl`, `network:egress`, `cpu`, `mem`, `gpu`
- job declares: `requires=[...]`, `risk=[...]`

**Why packages need it:** repo verify needs toolchain; LLM needs GPU; collectors need network.

### Package usage model
- Package job submission includes `requires`.
- Scheduler chooses eligible pool without knowing anything about the domain.

---

## 11) Budgets + quotas (enforced by kernel + scheduler)

**Status:** Partially implemented: safety constraints for max runtime/retries/artifact bytes/concurrency; gateway enforces max concurrent runs.

Per tenant / per actor:
- max concurrent runs
- max runtime
- max artifact bytes
- max retries
- max PR size (files/lines changed) via constraints

**Why packages need it:** “agent went wild” becomes bounded damage.

### Package usage model
- SRE package PR creation step is constrained:
  - max files, max lines, deny paths, require approval in prod
- Kernel returns `ALLOW_WITH_CONSTRAINTS` that the workflow engine/scheduler must honor.

---

## 12) Replay + re‑run semantics

**Status:** Implemented: rerun-from-step, dry-run, and run idempotency keys.

You need:
- rerun a run from step N
- rerun with same inputs (immutable pointers)
- “dry‑run” mode (no external side effects)

**Why packages need it:** debugging and safe iteration.

### Package usage model
- “Incident→PR” can be re-run after policy updates or worker fixes.
- Dry-run supports “propose patch but don’t open PR” safely.

---

# P2 — Enterprise‑grade  
*(don’t block MVP, but know what’s coming)*

## 13) Identity + tenancy model that won’t paint you into a corner

P2 core should evolve to:
- OIDC/JWT auth for humans
- service-to-service auth (mTLS or signed tokens)
- RBAC for control plane actions
- tenant isolation for data (ctx/res/artifacts)

## 14) Full observability
- structured logs with `trace_id/run_id/job_id`
- Prometheus metrics across core services
- tracing propagation

## 15) Versioned migrations and backward compat
- state store migrations (workflow schema evolution)
- protocol version negotiation
- “last-known-good” configs/policies

---

# Pack‑Ready Hooks to Include *Now* (even before packages exist)

Add these fields to the job metadata today:

- `tenant_id`, `actor_id`, `actor_type`
- `idempotency_key`
- `pack_id` (optional, empty now)
- `capability` (semantic action label, not just topic)
- `risk_tags` (`prod/write/network/secrets/exec`)
- `requires` (capabilities for routing)

**Why this matters:** it lets future packages plug into the same enforcement/routing/audit machinery **without core changes**.

Workflow steps support a `meta` block that maps to `JobMetadata`, so package templates can declare
`capability`, `risk_tags`, `requires`, and `pack_id` at the step level without touching core.

---

# Extra Core Primitives (High Leverage, Still Platform‑Pure)

These are additions that pay off massively later without turning core into product soup.

## 1) Policy “explain” + “simulate” APIs (SecOps will demand this)

- `POST /api/v1/policy/evaluate` → decision + matched `rule_id` + constraints
- `POST /api/v1/policy/simulate` → same, but **no side effects** (for CI / PR reviews)
- `GET /api/v1/policy/snapshots` → version/hash currently loaded
- `GET /api/v1/policy/bundles` → list policy bundles
- `GET /api/v1/policy/bundles/{id}` → bundle detail
- `PUT /api/v1/policy/bundles/{id}` → update bundle (requires `X-Principal-Role: secops`)
- `POST /api/v1/policy/bundles/{id}/simulate` → simulate against draft bundle
- `POST /api/v1/policy/publish` → publish bundles (requires `X-Principal-Role: secops`)
- `POST /api/v1/policy/rollback` → rollback bundles (requires `X-Principal-Role: secops`)
- `GET /api/v1/policy/audit` → policy publish/rollback audit

**Why:** makes policy changes reviewable and prevents “security theater”.

**Status:** Implemented in gateway and safety kernel.

Bundle IDs include `/` (e.g. `secops/workflows`). Replace `/` with `~` in the `{id}` path segment
or use the `bundle_id` query parameter.

### Package integration
- Package install pipelines can simulate policies before deployment.
- SecOps can validate “will SRE Investigator be allowed to open PRs in prod?” before enabling.

---

## 2) Schema validation as a first‑class primitive

Core should support:
- registering JSON Schemas (or accepting inline schemas with workflows/jobs)
- validating job inputs/outputs and step outputs

**Why:** packages become reliable and debuggable; you stop passing mystery blobs between steps.

**Status:** Implemented with Redis-backed schema registry and workflow input/step IO validation.

### Package integration
- SRE package enforces a schema for `IncidentContext`, `EvidenceBundle`, `PatchPlan`.
- Kernel can reject malformed or suspicious inputs early.

---

## 3) Resource locks / concurrency guards (prevents chaos)

A tiny “lock service” inside core:
- lock by `{repo}`, `{cluster/ns}`, `{service/env}`, `{incident_id}`
- modes: shared/exclusive, TTL, owner

**Why:** once you run autopatcher or MCP actions, two workflows racing will wreck you.

**Status:** Implemented with Redis-backed shared/exclusive locks and gateway APIs.

### Package integration
- SRE Investigator acquires exclusive lock on `{service/env}` before patch generation/PR open.
- Verify steps can hold shared locks; mutation steps require exclusive.

---

## 4) Runner profiles + constraints (without shipping workers)

Even if core ships zero workers, define **execution profiles** packages can request:
- `sandbox=isolated`
- `network=none|egress-allowlist`
- `fs=ro|rw`
- `tools=git,kubectl,go`

Scheduler routes based on `requires[]`.

**Why:** lets you enforce “this job can’t touch network” at the platform level.

**Status:** Partially implemented: scheduler routes by `requires` and constraints are passed via env; sandbox enforcement is up to workers/runners.

### Package integration
- Collectors request network egress; LLM steps request “no network”.
- Kernel enforces that risky steps can’t run in permissive profiles.

---

## 5) Artifact store abstraction + retention classes

Standardize:
- `artifact_ptr`
- retention class (`short`, `standard`, `audit`)
- max size + chunking policy

**Why:** avoids shoving megabytes into Redis ctx and gives audit durability.

**Status:** Implemented (Redis-backed artifacts + retention classes).

### Package integration
- “evidence” is audit retention; “temp logs” are short retention.

---

## 6) Immutable run/event log (append‑only timeline)

Maintain an append-only timeline:
- state transitions, decisions, approvals, dispatches, result pointers

**Why:** audit, replay, postmortems, “why did it do that?”

**Status:** Implemented (run timeline stored in Redis and exposed via gateway).

### Package integration
- SRE Investigator PR body can link to a canonical run timeline.
- MCP calls can be fully reconstructed for compliance.

---

## 7) First‑class budgets enforced by kernel + scheduler

Budgets are safety:
- max runtime, max retries, max artifact bytes, max concurrent runs
- max diff size, max files touched, deny-path patterns (as constraints)

**Why:** keeps early packages safe and sellable.

**Status:** Partially implemented (policy constraints for runtime/retries/artifacts/concurrency).

### Package integration
- SRE patch generation constrained to `max_files_changed`, `max_lines_changed`.
- Kernel can auto-rewrite budgets per environment (prod stricter than dev).

---

## 8) Idempotency keys + dedupe across the control plane

Make it explicit:
- `idempotency_key` on submit/run
- dedupe window + stable semantics

**Why:** webhook storms, retries, MCP clients will otherwise duplicate actions.

**Status:** Implemented for job submission and workflow run creation.

### Package integration
- Incident ingest uses incident_id as idempotency key.
- “Open PR” step uses `incident_id + repo + branch` as dedupe key.

---

## 9) Ops surfaces (CLI + optional dashboard)

Ship `coretexctl` that can:
- create/run/delete workflows
- approve/reject
- inspect run timeline
- retry DLQ

Optional: a lightweight dashboard that talks to the gateway for run/status visibility.

**Why:** bring-up, debugging, demos without requiring a full UI stack.

**Status:** Implemented (`cmd/coretexctl` + smoke script, plus `dashboard/`).

### Package integration
- Ops can run: `coretexctl pack install <path|url>` / `coretexctl pack uninstall <id>` / `coretexctl pack verify <id>`
- CLI still drives core workflows and approvals with no packs installed.

---

# Don’t Add to Core (It Will Rot You)

- Datadog/Coralogix/GitHub/K8s connectors (**packages only**)
- LLM providers / prompt logic (**packages only**)
- SRE Investigator logic (**package**)
- MCP proxy/controller logic (**separate service/package later**)

Core should provide **governance + runtime**, not domain logic.

---

# If You Add Only 3 Things, Add These

1) **Policy explain/simulate**  
2) **Resource locks**  
3) **Runner profiles + requires/constraints routing**

These three are what make future packages safe and enterprise-real instead of toys.

---

# How Packages Use Core Without Touching Core Code (Concrete Example: SRE Investigator)

When you install `sre-investigator` later, it should consist of:
- workers (containers) that subscribe to `job.sre.*` topics
- workflow templates that orchestrate those workers
- overlays:
  - `pools.overlay.yaml` mapping `job.sre.* → sre-investigator-pool`
  - `timeouts.overlay.yaml` for collector/verify steps
  - `safety.overlay.yaml` adding:
    - allowlist for read-only collectors
    - require approval for PR creation in prod
    - constraints: deny-paths, max diff size, network rules

Core stays unchanged because:
- scheduler already routes by config
- workflow engine already supports job dispatch + approvals + retries
- kernel already evaluates capability/risk + applies constraints
- artifact pointers already store evidence
- audit log already records decisions and run timeline

**Net effect:** new product behavior appears by **installing overlays + deploying workers**, not editing core.

---
