# Audit Hash Chain — Tamper Evidence for SIEM Events

Cordum's audit pipeline emits every governance event (safety decisions,
approvals, policy changes, MCP tool resolutions, legal-hold transitions,
…) through a per-tenant append-only **hash chain** before forwarding to
the configured SIEM backend. The chain gives you cryptographic evidence
that:

- no event has been silently mutated in place,
- no event has been removed from the sequence,
- no event has been re-ordered,

within the audit retention window.

This document covers the threat model, the on-disk shape, the operator
commands, how to interpret the verify response, how retention is
distinguished from tampering, and what to do when verify reports
`compromised`.

---

## 1. Threat model

The chain protects against post-hoc modification of the audit trail by
an actor with some Redis access — a misconfigured operator, a rogue
admin, an attacker who pivoted to the state store. Explicitly in scope:

| Threat                                   | Detection signal                                      |
|------------------------------------------|-------------------------------------------------------|
| Modifying an event's payload in Redis    | `hash_mismatch` at the tampered `seq`                 |
| Deleting an event                         | `missing` gap at the deleted `seq`                    |
| Re-ordering events                        | `out_of_order` at the displaced `seq`                 |
| Rolling back the chain to an earlier point| Boundary (`retention_boundary_seq`) shifts forward    |

Explicitly **out of scope** — the chain is not an access-control
mechanism:

- **Log fabrication before Cordum saw the event.** The chain starts
  inside Cordum. An attacker who controls a producer before the
  consumer runs can emit an event of their choosing; the chain will
  faithfully record it. Combine the chain with producer attestation
  (SDK handshake) for end-to-end provenance.
- **Redis availability.** If the store is wiped the chain is gone — it
  cannot self-restore. Pair with cross-region snapshots.
- **Collusion across replicas.** If both the publisher and the
  single-consumer replica are compromised, an event can be spoofed with
  a valid hash. Cordum's queue-group ensures a single replica owns the
  chain at a time, but the chain does not itself prove the replica was
  honest.

---

## 2. On-disk shape

Every tenant gets two Redis keys:

```
audit:chain:<tenant>        — Redis Stream (XADD *), one entry per event
audit:chain:head:<tenant>   — "seq:event_hash" of the tenant's chain head
```

The stream entry has two fields:

| Field   | Type           | Meaning                                                                  |
|---------|----------------|--------------------------------------------------------------------------|
| `seq`   | int64 string   | Monotonic per-tenant sequence. First event is 1.                         |
| `event` | JSON string    | The full `SIEMEvent` payload, including `seq`, `event_hash`, `prev_hash` |

The in-event fields are:

| Field         | Meaning                                                                                            |
|---------------|----------------------------------------------------------------------------------------------------|
| `seq`         | Matches the stream's `seq` field — duplicated for downstream convenience.                           |
| `prev_hash`   | `event_hash` of the previous event (empty string for the genesis event).                           |
| `event_hash`  | SHA-256 (hex) of the canonical JSON of the event with `seq` and `event_hash` cleared. `prev_hash` **is** part of the input so tampering cascades forward. |

The hash is deterministic: struct-field order from Go's `encoding/json`,
alphabetical map-key order for `extra`, empty-string `prev_hash` for
genesis.

---

## 3. Consumer wiring

`NATSAuditConsumer` (in `core/audit/consumer.go`) invokes
`Chainer.Append` immediately before `exporter.Export`. Chain append is
atomic via a CAS Lua script on the head key. In direct-transport mode
(`AUDIT_TRANSPORT` unset), the gateway wraps the write path in an
`auditChainSender` that appends to the chain before forwarding to the
configured exporter, so the chain is populated whether or not a NATS
consumer is running.

> **Default behavior**
> The audit chain is instantiated unconditionally at gateway boot.
> `CORDUM_AUDIT_EXPORT_TYPE` controls only the external SIEM
> destination — leaving it unset does **not** disable the chain. Set
> the env var to `webhook`, `syslog`, `datadog`, or `cloudwatch` to
> stream to a real backend, or to `null`/`discard`/`chain-only` for an
> explicit no-op exporter that still emits audit-export counters.
>
> Older builds required `CORDUM_AUDIT_EXPORT_TYPE=null` as a
> chain-keep-alive incantation; the reference `docker-compose.yml`
> historically hardcoded it for that reason. Current builds no longer
> need the workaround — explicit `null` is still accepted for operators
> who want the DiscardExporter shape, but an unset value is equivalent
> for chain purposes. Cross-ref: task-e1d54a75 (hotfix that kept the
> chain alive by shipping a DiscardExporter); task-096de016 (this
> change, which decoupled the chain from the exporter).

Fail-mode is controlled by `CORDUM_AUDIT_CHAIN_FAIL`:

| Value         | Behaviour                                                                                                         |
|---------------|-------------------------------------------------------------------------------------------------------------------|
| `strict` (default) | On chain-append failure: log a structured ERROR (event_type, tenant_id, job_id only — **never** the payload), ack the NATS message, and **drop** the event. Un-chained events never reach SIEM, because they would be un-verifiable. |
| `permissive` | On chain-append failure: log a WARN and still forward to SIEM. Use only for dev / incident recovery — the SIEM entry will not cover verify. |

Legal hold (`core/audit/legal_hold.go`) is a retention-policy flag, not
a pipeline filter — events for held tenants flow through the chain
identically to any other tenant's events.

### Backend types

| `CORDUM_AUDIT_EXPORT_TYPE` value    | Behaviour |
|-------------------------------------|-----------|
| unset or empty, `none`              | No external exporter. Chain still engaged. This is the default compose posture. |
| `null`, `discard`, `chain-only`     | Explicit no-op exporter (DiscardExporter). Chain still engaged; audit-export counters report "a backend exists" for metrics parity with real SIEM targets. |
| `webhook`                           | HTTP POST to `CORDUM_AUDIT_EXPORT_WEBHOOK_URL`. |
| `syslog`                            | RFC 5424 stream to `CORDUM_AUDIT_EXPORT_SYSLOG_ADDR`. |
| `datadog`                           | Datadog Logs API using `CORDUM_AUDIT_EXPORT_DD_API_KEY`. |
| `cloudwatch`                        | AWS CloudWatch Logs using the configured log group / stream. |

---

## 4. `cordumctl audit verify`

The operator-facing command. Calls `GET /api/v1/audit/verify` and
renders a human table (or `--json` for CI).

```bash
cordumctl audit verify [tenant] [--since MS] [--until MS] [--limit N] [--json]
```

Flags:

| Flag        | Meaning                                                     |
|-------------|-------------------------------------------------------------|
| `tenant`    | Positional; defaults to the CLI's configured tenant         |
| `--since`   | Inclusive lower bound on stream timestamp (unix ms)         |
| `--until`   | Inclusive upper bound on stream timestamp (unix ms)         |
| `--limit`   | Max events to read (default 10000, max 100000)              |
| `--json`    | Emit the raw JSON response instead of the table             |

Exit codes:

| Code | Meaning                                                                                  |
|------|------------------------------------------------------------------------------------------|
| 0    | `status=ok` — chain intact, no gaps or only a retention-trimmed prefix.                  |
| 2    | `status=compromised` — tampering detected.                                               |
| 3    | `status=partial` — boundary issue, verify could not conclude. Investigate but not panic. |
| 1    | Fatal (network / permissions / usage).                                                   |

Example output:

```
Audit chain verification — tenant default
  status:                 ok
  events checked:         1284
  events verified:        1284
  seq range observed:     1..1284
  retention boundary:     seq 1
  retention window:       168.0 hours
  gaps:                   none
```

Compromised example:

```
Audit chain verification — tenant default
  status:                 compromised
  events checked:         5
  events verified:        4
  seq range observed:     4..8
  retention boundary:     seq 4
  retention window:       168.0 hours
  gaps:                   2 total
    retention_trimmed:    3  [1, 2, 3]
    hash_mismatch:        1  [6]
```

The retention_trimmed row is informational — it describes the expected
shape of a chain that has aged past its retention window. The
`hash_mismatch` row under it is the real signal.

### When NOT to use verify

Do **not** use full-window verify as a readiness probe, hot-path liveness
check, per-request guard, or high-frequency load-test budget. It is an
operator attestation: the handler re-walks and re-hashes the requested
chain window so Redis tampering is detected at read time.

For recurring monitoring, pass `--since` / `--until` cursors and keep each
window small. Tenants above the default 10,000-event window (or the hard
100,000-event maximum) should paginate by time cursor instead of raising
`--limit` until the call becomes a fleet-wide CPU spike. `VerifyChain`
reads the predecessor before a mid-chain `--since` window, so cursor slices
still check cross-window linkage.

Use `cordum_audit_verify_inflight` to detect operator-induced pressure. A
non-zero value that stays elevated means active verify walks are consuming
work; reduce cadence, narrow the time window, or wait for the current
attestation to finish. `cordum_audit_verify_coalesced_total` increasing is
healthy under bursts of identical checks because it means duplicate callers
shared the same leader walk.

---

## 5. Interpreting the verify response

`status` is derived as follows:

| Observed                                                                               | Status          |
|----------------------------------------------------------------------------------------|-----------------|
| No missing / mismatched / out-of-order gaps within the walk.                            | `ok`            |
| Some gaps, **all** classified as `retention_trimmed`.                                   | `partial`       |
| Any `missing`, `hash_mismatch`, or `out_of_order` gap.                                  | `compromised`   |

`retention_boundary_seq` is the lowest seq currently present in the
stream. Gaps strictly below it are retention-trimmed (expected); gaps
at or above are real integrity failures.

`retention_window_hours` mirrors `CORDUM_AUDIT_RETENTION_HOURS` so
dashboards can render "your policy is N hours" without round-tripping
to the config service.

The response **never** contains raw event bodies — verify is an
integrity-reporting endpoint, not an event-retrieval API.

---

## 6. Retention vs tampering

Both retention expiry and tampering produce absent seq numbers. The
chain distinguishes them by construction:

1. **`retention_boundary_seq`** is the oldest seq still present in the
   stream. It is a concrete, cryptographically grounded cutoff — the
   chain cannot lie about it without breaking every downstream hash.
2. **Gaps below the boundary** were trimmed by retention policy: either
   stream max-length eviction or explicit admin-driven XDEL. They are
   reported as `retention_trimmed` and do **not** flip status to
   compromised.
3. **Gaps at or above the boundary** happen inside the "should still
   exist" window. They are reported as `missing` and **do** flip status
   to compromised.

The window is the operator-set `CORDUM_AUDIT_RETENTION_HOURS` (default
168h / 7 days). If the oldest present event has a timestamp older than
`now − retention_window`, something has held the chain open — either
legal hold, or the retention reaper is behind. Neither is inherently
bad; surface it on the dashboard.

---

## 7. Runbook — verify reported `compromised`

1. **Do NOT restart the safety kernel or gateway.** A restart loses
   in-memory state (unflushed consumers, partial NATS redeliveries)
   that may be needed for the investigation.
2. **Snapshot Redis immediately.** `BGSAVE` + copy the resulting RDB
   file to cold storage. The chain lives entirely in Redis; once
   mutated, the original bytes are unrecoverable.
3. **Freeze the write path for the affected tenant.** If the tampering
   is in-flight, pause the producer (scheduler drain) to keep the
   evidence consistent.
4. **Re-run verify with `--json`** and attach the output to the
   incident ticket. Operators care about `first_seq`, `last_seq`,
   `retention_boundary_seq`, and the gap list.
5. **Diff against the backup.** If you have a Redis replica / snapshot
   from before the incident, compare the stream contents. A hash
   mismatch at seq=N with a pre-incident copy of seq=N tells you
   exactly what was altered.
6. **Rotate access.** If the tampering was deliberate, the actor had
   Redis ACL or network access; assume the credential is compromised.
7. **Do NOT attempt to repair the chain.** The chain is append-only and
   cannot be retro-fitted. A compromised chain stays compromised from
   that seq forward — the integrity signal itself is the record.

If `GET /api/v1/audit/verify` returns `503 audit chainer not installed`
on a current build, the gateway failed to construct the chainer at
boot — the only real path to that outcome is a Redis client failure
during `initAuditPipeline`. Check the gateway logs for the
`audit chain enabled` line; if it is missing, Redis connectivity is the
root cause, not the audit export config. The chain is no longer gated
on `CORDUM_AUDIT_EXPORT_TYPE`, so setting the env var will not revive
it. Older builds (pre-task-096de016) did disable the chain when
`CORDUM_AUDIT_EXPORT_TYPE` was unset; if you are on a pre-fix image,
set it to `null` as a temporary workaround and upgrade.

---

## 8. Migration note

Events produced before Cordum shipped the chain have no `seq` /
`prev_hash` — they are invisible to verify. The chain begins at the
first event whose `prev_hash` field is present per tenant; that event
is the tenant's **genesis**. Verify refuses to cross the genesis
boundary. Dev-env re-seed + prod migration plan live in
`docs/deployment/audit-chain-migration.md`.
