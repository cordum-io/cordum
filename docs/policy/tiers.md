# Policy tiers: Job → Workflow → Global

Cordum policy decisions are evaluated across three tiers:

1. **Global** — org-wide defaults and invariants.
2. **Workflow** — overrides for all executions belonging to one workflow.
3. **Job** — overrides attached to one job execution or Edge session.

Fragments without a `tier` field are treated as **global** for backward
compatibility with existing packs and SafetyPolicy YAML.

## Precedence

| Order | Source | Effect |
| --- | --- | --- |
| 1 | Global **Invariants** DENY / approval / throttle | Security floor. Cannot be overridden by workflow or job policy. |
| 2 | Job-tier matching rule | Most-specific rule wins. |
| 3 | Workflow-tier matching rule | Wins over global rules. |
| 4 | Global matching rule | Backward-compatible default behavior. |
| 5 | Most-specific scoped default | Job default → workflow default → global default; empty/invalid defaults fail closed to deny. |

Invariant ALLOW rules are fallback defaults only; they do **not** override a
workflow/job/global DENY.

## YAML shape

```yaml
tier: workflow                 # global | workflow | job
selector:
  workflow_id: deploy-prod     # required for workflow tier
default_decision: deny
rules:
  - id: workflow-deny-prod-write
    decision: deny
    match:
      topics: ["job.deploy"]
      labels:
        command.class: write
```

Job tier accepts either a job attachment or an Edge session attachment:

```yaml
tier: job
selector:
  job_id: job/abc/policy       # or session_id: session/<edge-session-id>/policy
rules:
  - id: job-allow-cleanup-read
    decision: allow
    match:
      topics: ["job.read"]
      labels:
        operation: read
```

## Storage and scope resolution

| Tier | Storage / source | Evaluate-time scope |
| --- | --- | --- |
| Global | Unified Global authority from EDGE-052 / policy bundles. | Always applies. |
| Workflow | `workflow.policy_override` YAML; loaded as synthetic bundle `workflow/{id}/policy`. | Labels such as `workflow_id` / `workflow_run_id`. |
| Job | No new persistence layer. The workflow engine and Edge session creation stamp `policy.attachment_id`. | `policy.attachment_id` first, then job/session labels as fallbacks. |

Edge sessions receive `policy.attachment_id=session/{session_id}/policy`.
Workflow jobs receive `policy.attachment_id=job/{job_id}/policy`.

## Worked examples

### Workflow blocks a global allow

Global:

```yaml
rules:
  - id: global-allow-read
    decision: allow
    match:
      topics: ["job.read"]
      labels: { operation: read }
```

Workflow `deploy-prod`:

```yaml
tier: workflow
selector: { workflow_id: deploy-prod }
default_decision: deny
rules:
  - id: workflow-deny-read
    decision: deny
    match:
      topics: ["job.read"]
      labels: { operation: read }
```

A request with `workflow_id=deploy-prod` and `operation=read` is **DENY** by
`workflow-deny-read`.

### Job flips one workflow decision

Add:

```yaml
tier: job
selector: { job_id: job/abc/policy }
rules:
  - id: job-allow-read
    decision: allow
    match:
      topics: ["job.read"]
      labels: { operation: read }
```

The same workflow request with `policy.attachment_id=job/abc/policy` is
**ALLOW** by `job-allow-read`. Other jobs in `deploy-prod` remain denied.

### Invariant remains uncrossable

```yaml
# secops/invariants
rules:
  - id: inv-deny-env-read
    decision: deny
    reason: .env reads are never allowed
    match:
      topics: ["job.read"]
      labels: { path: ".env" }
```

Even with the job allow above, a request for `.env` is **DENY** by
`inv-deny-env-read`.

## Cache and audit evidence

Agentd safe-allow cache keys include:

```text
(global_policy_snapshot, workflow_override_snapshot, job_override_snapshot)
```

The workflow/job scoped snapshots are deterministic identifiers derived from
the active policy snapshot and the matched workflow/job attachment scope. When
workflow/job snapshots are empty, the cache key retains the old global-only
shape.

Every persisted Edge decision event records:

```json
{
  "rule_id": "job-allow-read",
  "tier": "job",
  "policy_snapshot": "cfg:<sha>"
}
```

The Safety Kernel also records the matched tier in policy-decision telemetry.

## Tests

- Unit/handler coverage: tier parsing, precedence, workflow override storage,
  job attachment labels, audit tier field, and safe-allow cache key isolation.
- Integration coverage:
  `core/controlplane/safetykernel/tier_precedence_integration_test.go`
  (`//go:build integration`) proves global, workflow, job, invariant, and
  scoped-default behavior against one Safety Kernel snapshot.
