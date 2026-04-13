# ADR-009: Control Plane Boundary Hardening Canonical Registrations

- Status: Proposed
- Date: 2026-04-07

## Context
Cordum currently treats runtime traffic as both telemetry and authority:

- `PackTopic` defines `name`, `requires`, `risk_tags`, and `capability`, but not pool ownership or topic-bound schemas. Topic-to-pool routing is still maintained separately in `config/pools.yaml`.
- Scheduler worker membership is driven by `workerEntry` in `core/controlplane/scheduler/registry_memory.go`, which stores raw `Heartbeat`, optional `Handshake`, and `lastSeen`. A publisher that can emit heartbeat traffic can currently claim any `worker_id`, `pool`, or label set.
- Config documents are persisted through `configsvc.Document` at `cfg:<scope>:<scope_id>`, which gives us an existing optimistic-locking store for canonical control-plane records.
- Schemas already live in the dedicated schema registry (`schema:<id>`, `schema:index`) and are exposed through gateway CRUD handlers, but submit-time topic validation is not bound to a canonical topic registry.
- CAP wire types do not yet provide a clean authority channel: `Heartbeat` has load and placement metadata only, `Handshake` has role/version/capabilities only, and `BusPacket` has `sender_id` plus an optional signature but no worker attestation token.

The hardening goal is to separate **authority** from **telemetry** without changing the existing heartbeat, routing, pool, or retry mechanics:

- authority: what topics exist, what schema binds to them, which workers are allowed to claim which identities
- telemetry: liveness, readiness, load, and dashboard/runtime state

This ADR defines the canonical records every later implementation task must share.

## Decision
Adopt three canonical records:

1. **TopicRegistration** — source of truth for topic existence, routing ownership, and optional submit-time schemas.
2. **WorkerCredential** — source of truth for worker identity and authorization.
3. **WorkerSnapshot** — runtime-only derived telemetry used for scheduling filters and dashboard views, but never as the source of authority.

Gateway and scheduler must both consume these records so direct CAP publishers cannot bypass validation by skipping the HTTP API.

## Current-State Baseline

| Concern | Current source | Gap |
|---|---|---|
| Topic existence | implicit in `config/pools.yaml` and pack overlays | no canonical registry, no status, no schema binding |
| Topic → pool | `config/pools.yaml` / config overlays | routing exists but is disconnected from topic metadata |
| Worker identity | heartbeat `worker_id` field | spoofable, no attestation or authorization record |
| Worker readiness | optional handshake presence only | no `ready_topics` declaration, no scheduling filter |
| Submit-time schema validation | separate schema registry | no topic-bound lookup at gateway or scheduler boundary |

## TopicRegistration

### Canonical type
```yaml
TopicRegistration:
  name: string                # primary key, e.g. job.demo.echo
  pool: string                # target pool used by routing and availability checks
  input_schema_id: string?    # schema registry id for submit payload validation
  output_schema_id: string?   # schema registry id for result validation / UI expectations
  pack_id: string?            # owning pack, null for admin-registered topics
  requires: []string          # copied from pack topic metadata
  risk_tags: []string         # copied from pack topic metadata
  status: active|deprecated|disabled
```

### Storage
- **Config scope:** `system`
- **Scope ID:** `topics`
- **Redis key:** `cfg:system:topics`
- **Document shape:**

```json
{
  "scope": "system",
  "scope_id": "topics",
  "data": {
    "job.demo.echo": {
      "name": "job.demo.echo",
      "pool": "default",
      "input_schema_id": "demo/EchoInput",
      "output_schema_id": null,
      "pack_id": "demo-pack",
      "requires": [],
      "risk_tags": ["internal"],
      "status": "active"
    }
  }
}
```

### Writers
- **Pack install:** writes or updates registrations for topics owned by the pack. `pool` is derived from the installed topic→pool mapping produced by the pack's config overlay, not from heartbeat traffic.
- **Admin API:** `POST /api/v1/topics` registers external/user-managed topics that are not pack-owned.
- **Pack uninstall:** marks pack-owned topics as `disabled` by default so audit history survives and references remain resolvable during migration.

### Enforcement semantics
- `active`: valid topic
- `deprecated`: valid topic, but gateway/scheduler emit warning/audit signals
- `disabled`: reject at the boundary (HTTP 400 at gateway, direct bus submission rejected before dispatch at scheduler)

### Migration
If `cfg:system:topics` is empty, the first reader/writer synthesizes registrations from the legacy topic→pool map:

- topic name from `config/pools.yaml` / config overlays
- `pool` from the legacy map
- `input_schema_id` / `output_schema_id` = `null`
- `pack_id` = `null` unless a pack registry record can be resolved
- `requires` / `risk_tags` = `[]`
- `status` = `active`

This preserves backward compatibility while establishing a canonical registry for future writes.

### Why a dedicated document instead of extending the pool map
`config/pools.yaml` answers only “which pool handles this topic.” Boundary hardening also needs schema bindings, lifecycle state, and ownership metadata. Keeping `TopicRegistration` in `cfg:system:topics` avoids overloading pool configuration and gives both gateway and scheduler a single lookup point for “does this topic exist and what rules apply.”

## WorkerCredential

### Canonical type
```yaml
WorkerCredential:
  worker_id: string
  credential_hash: string      # PHC-formatted Argon2id hash
  allowed_pools: []string
  allowed_topics: []string
  pack_id: string?             # null for external/user-managed workers
  created_by: pack-install|admin|api
  created_at: RFC3339 timestamp
  revoked_at: RFC3339 timestamp?
```

### Storage
- **Config scope:** `system`
- **Scope ID:** `workers`
- **Redis key:** `cfg:system:workers`
- **Document shape:**

```json
{
  "scope": "system",
  "scope_id": "workers",
  "data": {
    "worker-demo-1": {
      "worker_id": "worker-demo-1",
      "credential_hash": "$argon2id$v=19$m=65536,t=3,p=1$...",
      "allowed_pools": ["default"],
      "allowed_topics": ["job.demo.echo"],
      "pack_id": "demo-pack",
      "created_by": "pack-install",
      "created_at": "2026-04-07T10:00:00Z",
      "revoked_at": null
    }
  }
}
```

### Writers
- **Pack install:** creates credentials for pack-managed workers, prints the plaintext token once, and stores only the Argon2id PHC string in config.
- **Admin API:** `POST /api/v1/workers/credentials` provisions or rotates credentials for external/user-managed workers.
- **Revocation path:** sets `revoked_at` instead of deleting the record immediately so audit and incident response can still resolve past worker IDs.

### Authentication flow
- Add an optional `auth_token` field to the CAP `BusPacket` envelope, not to heartbeat labels.
- Workers include the token on every worker-originated packet (`Heartbeat`, `Handshake`, `JobProgress`, `JobResult`, `JobCancel`).
- Scheduler (and any future direct-bus gateway consumer) validates `sender_id` + `auth_token` against `cfg:system:workers` before treating the packet as authoritative.
- Pool/topic claims are checked against `allowed_pools` and `allowed_topics`; a heartbeat that claims an unauthorized pool becomes non-authoritative even if it is still visible in telemetry during migration modes.

### Why BusPacket auth instead of heartbeat labels or NATS headers
- **Not heartbeat labels:** labels are already placement metadata and are often surfaced in dashboards/logs. They are not a safe secret-bearing channel.
- **Not NATS-only headers:** direct CAP publishers and future transports already standardize on `BusPacket`. Putting the token on the envelope keeps the mechanism transport-agnostic and applies equally to heartbeats, handshakes, and job-result traffic.

## WorkerSnapshot

### Canonical type
```yaml
WorkerSnapshot:
  worker_id: string
  pool: string
  region: string
  liveness: bool
  ready: bool
  ready_topics: []string
  active_jobs: int32
  max_parallel_jobs: int32
  cpu_load: float
  memory_load: float
  capabilities: map[string]bool
  last_heartbeat: RFC3339 timestamp
  last_handshake: RFC3339 timestamp?
```

### Source of truth
- **Primary runtime state:** scheduler in-memory registry (successor to `workerEntry`)
- **Derived Redis read model:** existing worker snapshot projection at `sys:workers:snapshot`
- **Not stored in config:** no `cfg:*` key owns `WorkerSnapshot`

### Derivation rules
- `liveness` comes from heartbeat TTL / `lastSeen`
- `ready` comes from handshake + readiness policy:
  - if `WORKER_READINESS_REQUIRED=false`, workers without handshake are treated as ready for backward compatibility
  - if `WORKER_READINESS_REQUIRED=true`, a worker must have a recent handshake to be schedulable
- `ready_topics` comes from the handshake payload
- `capabilities` is the normalized union of heartbeat capability strings and handshake capability flags, represented as a map for dashboard and scheduling filters
- `pool`, `region`, `active_jobs`, `max_parallel_jobs`, `cpu_load`, and `memory_load` remain telemetry copied from heartbeat

### Authority boundary
`WorkerSnapshot` is explicitly **not** an authorization record:

- it cannot create or grant worker identity
- it cannot expand allowed pools/topics
- it can only describe the current runtime state of a worker already recognized through `WorkerCredential`

The scheduler may use `WorkerSnapshot` as a filter (“is this attested worker alive and ready for topic X?”), but never as proof that a worker is allowed to claim `worker_id=X`.

### Why keep WorkerSnapshot runtime-only
Snapshot data is high-churn telemetry. Writing it into `cfg:*` would mix operator-managed authority with rapidly changing runtime state, create unnecessary optimistic-lock contention, and blur the authority boundary this ADR is trying to harden.

## Topic-to-Schema Binding

### Decision
Extend `PackTopic` in `pack.yaml` with optional schema references:

```yaml
topics:
  - name: job.demo.echo
    capability: demo.echo
    requires: []
    riskTags: [internal]
    input_schema_id: demo/EchoInput
    output_schema_id: demo/EchoOutput
```

The pack install flow persists these values into `TopicRegistration.input_schema_id` and `TopicRegistration.output_schema_id`.

### Validation path
- **Gateway boundary:** on HTTP/gRPC submit, resolve `TopicRegistration` by topic name before dispatch
- **Scheduler boundary:** on direct `sys.job.submit` bus intake, resolve the same `TopicRegistration` before routing
- if `input_schema_id` is present, load the schema from the existing registry (`schema:<id>` / `/api/v1/schemas/{id}`) and validate the submit payload
- if `output_schema_id` is present, later result-path or UI workflows may validate output against the same registry
- if no schema ID is present, validation is skipped and behavior remains backward compatible

### Unknown topic vs no workers
- **Unknown topic:** fast reject at the boundary because no `TopicRegistration` exists
- **Known topic with zero workers:** remains valid; scheduler keeps returning the existing degraded/no-workers path instead of treating it as a validation failure

### Why bind schemas at the topic layer
Schemas already exist as first-class records, but submit-time validation needs a deterministic “topic → schema” lookup that survives direct CAP publishers. Extending `PackTopic` keeps the declaration close to the pack-owned topic definition while making `TopicRegistration` the canonical runtime lookup record for both gateway and scheduler.

## CAP Proto Changes

These changes belong in the `cap/` repository and must remain wire-compatible by adding new optional fields only.

### 1. `BusPacket.auth_token`
Recommended change:

```proto
message BusPacket {
  ...
  string auth_token = 18;
}
```

Reasoning:
- one field covers all worker-originated packets instead of only heartbeat traffic
- avoids storing secrets in labels
- keeps attestation transport-agnostic for NATS and any future non-NATS CAP transport

### 2. `Handshake.ready_topics`
Recommended change:

```proto
message Handshake {
  ...
  repeated string ready_topics = 6;
}
```

Reasoning:
- handshake is the correct place for explicit readiness declaration
- readiness can differ from broad credential authorization (`allowed_topics`)
- empty/missing `ready_topics` remains backward compatible when `WORKER_READINESS_REQUIRED=false`

### No heartbeat auth field
`Heartbeat` should not carry a separate token field if `BusPacket.auth_token` exists. A single envelope field avoids duplicate auth surfaces and guarantees that heartbeat, handshake, progress, and result packets all use the same attestation mechanism.

## External Worker Registration

External, user-managed workers are first-class citizens in this model:

- operators register topics through `POST /api/v1/topics`
- operators provision credentials through `POST /api/v1/workers/credentials`
- `pack_id` is `null` for these records
- `allowed_topics` and `allowed_pools` are explicit, operator-managed allowlists

This avoids making pack installation the only path for trusted workers and preserves support for customer-managed fleets.

## Migration and Feature Flags

Feature flags live under the system config document `cfg:system:default`, for example:

```json
{
  "scope": "system",
  "scope_id": "default",
  "data": {
    "boundary_hardening": {
      "schema_enforcement": "warn",
      "worker_attestation": "off",
      "worker_readiness_required": false
    }
  }
}
```

### Flag definitions
- `SCHEMA_ENFORCEMENT = off | warn | enforce` (default `warn`)
- `WORKER_ATTESTATION = off | warn | enforce` (default `off`)
- `WORKER_READINESS_REQUIRED = true | false` (default `false`)

### Rollout phases
1. **Phase 1 — Observe**
   - populate `cfg:system:topics` and `cfg:system:workers`
   - keep `SCHEMA_ENFORCEMENT=warn`
   - keep `WORKER_ATTESTATION=off` or `warn`
   - keep `WORKER_READINESS_REQUIRED=false`
   - emit audit logs and metrics for missing topic registrations, missing schema bindings, unattested workers, and workers without ready topics

2. **Phase 2 — Enforce for new installs**
   - newly installed packs must write `TopicRegistration`
   - newly provisioned workers must use `WorkerCredential`
   - new packs may declare `input_schema_id` / `output_schema_id`
   - clusters may move `WORKER_ATTESTATION` to `warn` or `enforce` for newly enrolled workers while grandfathering existing workers

3. **Phase 3 — Enforce globally**
   - `SCHEMA_ENFORCEMENT=enforce` for registered topics with bound schemas
   - `WORKER_ATTESTATION=enforce`
   - `WORKER_READINESS_REQUIRED=true` where operators want explicit readiness gating
   - legacy pools-only topic discovery becomes read-only migration input, not the authority source

### Backward compatibility guarantees
- existing packs without topic schema fields still register topics with `input_schema_id=null` / `output_schema_id=null`
- existing topics from `config/pools.yaml` continue to work via auto-migration into `cfg:system:topics`
- existing workers without tokens are still accepted when `WORKER_ATTESTATION` is `off` or `warn`
- existing workers without handshake or `ready_topics` are treated as ready when `WORKER_READINESS_REQUIRED=false`
- known topics with zero workers remain valid and continue using the existing degraded/no-workers scheduling path rather than becoming schema/registration failures

## Consequences

Positive:
- authority and telemetry are cleanly separated
- both gateway and scheduler can enforce the same topic existence and schema rules
- worker identity supports both pack-managed and external fleets
- dashboard and scheduler can share one runtime snapshot model without making it authoritative

Tradeoffs:
- pack install and admin flows must now maintain new system config documents
- migration requires dual-read behavior from legacy topic→pool routing during rollout
- worker publishers will need CAP updates to send `auth_token` and `ready_topics`
