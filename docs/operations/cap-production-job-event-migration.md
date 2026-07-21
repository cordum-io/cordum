# CAP-PRODUCTION job-event runtime migration

CAP-PRODUCTION dispatch fences and accepted-event state use one Redis Cluster
hash key per job:

```text
job:{<base64url(job_id)>}:runtime
```

The encoded hash tag prevents job IDs containing `{` or `}` from changing the
slot. Dispatch identity, attempt, authenticated worker and tenant, signed
message ID/digest, terminal state, result pointer, and pending outbox effect are
committed through Lua against that single key.
Signed message IDs stay bound to their verified digest across dispatch
attempts; starting a retry does not reset replay history.

## Rolling upgrade behavior

- Existing `job:meta:<job_id>`, `job:state:<job_id>`, and
  `job:result_ptr:<job_id>` values remain readable.
- The first dispatch/event after upgrade copies any legacy fence fields into
  the runtime hash with `HSETNX`; it never overwrites a concurrently-created
  newer fence.
- Runtime state/result values are authoritative. The durable outbox projects
  them back to legacy keys and query indexes for older readers.
- Do not delete legacy keys during a rolling upgrade. They can be retired only
  after every scheduler and reader understands the runtime hash.
- Saga compensation stacks use the same safe pattern at
  `saga:{<base64url(workflow_id)>}:stack` with a sibling `:recorded` set for
  per-job idempotency. Rollback reads the prior `saga:<workflow_id>:stack`
  layout when the tagged stack is empty, so pre-upgrade compensations drain.

An accepted result whose legacy projection or NATS publish is interrupted
stays in the runtime outbox. Scheduler startup and the periodic reconciler retry
the effect. Exact signed redelivery returns `duplicate` and resumes the same
pending effect rather than reapplying the state transition.

NATS delivery remains at-least-once: a process can stop after publishing and
before acknowledging the outbox. The accepted result retains the canonical job
and dispatch identity; workflow terminal-state handling and saga per-job
dedupe make replay one logical effect rather than promising impossible
exactly-once broker delivery. `sys.internal.job.result.accepted` is a durable
JetStream subject. Its broker message ID includes job, dispatch ID and attempt,
so distinct retry attempts remain distinct while the application fence remains
authoritative.
