# Edge retention, caps, and cleanup

Edge evidence is intentionally bounded in Redis. Large evidence bodies belong in
the artifact store behind pointer metadata; Redis stores only session,
execution, event index, and compact event records needed for policy/audit flows.

## Write-side caps

| Cap | Default | Where enforced | Failure behavior |
| --- | ---: | --- | --- |
| Executions per session | `100` | `RedisStore.CreateExecution` under WATCH on the session execution index, plus the Gateway pre-check controlled by `CORDUM_EDGE_MAX_EXECUTIONS_PER_SESSION`. | The create is rejected with `ErrSessionExecutionFanoutExceeded` / HTTP `429 max_executions_exceeded`; no execution is written. |
| Events per execution | `5000` | `RedisStore.AppendEvents` and `AppendEventsWithIdempotency` inside the watched append transaction. | The append is rejected with `ErrExecutionEventCapExceeded` / HTTP `429 event_cap_exceeded`; no event in the batch is written. |

Worst-case P0 session fanout is therefore bounded at:

```text
100 executions/session × 5000 events/execution = 500,000 events/session
```

Caps are hard failures, not silent drops. The caller must end the session or
execution and start a new one if more evidence is needed.

## DeleteSession cleanup

`DeleteSession` is idempotent and bounded:

- Scans the session execution index with Redis `ZSCAN Count=100`.
- Loads at most one scan page of executions at a time.
- Deletes Redis keys in `DEL` batches of at most 100 keys.
- Removes secondary index members for job, trace, workflow run, tenant, and
  principal indexes.
- Records `cordum_edge_session_cleanup_duration_seconds`.
- Records `cordum_edge_session_cleanup_keys_deleted_total`.
- Uses a 30 second foreground deadline.

If the foreground cleanup reaches the 30 second deadline, it returns the typed
`ErrSessionCleanupDeadlineExceeded`, emits
`cordum_edge_session_cleanup_deadline_total`, and schedules a best-effort
background continuation from the last cleanup cursor. Operators can safely retry
`DeleteSession`; already-deleted keys and index removals are no-ops.

Production cleanup code must never use Redis `KEYS`.

## Background retention sweeper

Gateway starts an Edge session sweeper at boot. Defaults:

| Setting | Default | Meaning |
| --- | ---: | --- |
| `CORDUM_EDGE_SESSION_RETENTION_TTL` | `720h` / 30 days | Sessions whose `ended_at` is older than this are eligible for cleanup. If `ended_at` is absent, `started_at` is used. |
| `CORDUM_EDGE_SESSION_SWEEP_INTERVAL` | `1h` | How often the sweeper scans for aged sessions after its initial boot sweep. |

Both environment values must parse as positive Go durations when explicitly
set. `0`, negative durations, and invalid strings fail Gateway startup instead
of silently weakening retention.

The sweeper:

- Uses Redis `SCAN Count=100` over `edge:session:*`.
- Delegates deletion to `DeleteSession`, so it inherits the same ZSCAN,
  batched-DEL, and deadline behavior.
- Respects context cancellation during Gateway shutdown.
- Emits `cordum_edge_session_swept_total` once for each session it removes.

## Operational notes

- Retention cleanup removes Edge session/execution/event Redis keys and their
  secondary indexes. Artifact bodies follow their artifact-store retention
  class and are not inlined in Edge events.
- A non-zero `cordum_edge_session_event_cap_rejected_total` indicates an agent
  loop or unusually large execution. Investigate before raising caps.
- A non-zero `cordum_edge_session_cleanup_deadline_total` indicates cleanup is
  taking longer than the foreground budget; retry is safe and background
  continuation should reduce remaining work.
