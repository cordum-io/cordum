# Audit Chain Architecture

> Tamper-evident, append-only audit logging for Cordum.

## Overview

Cordum's audit chain provides **cryptographic tamper-evidence** for every
security-relevant event that flows through the control plane. It combines:

1. **SHA-256 hash chaining** — each event's hash includes its predecessor's
   hash, so modifying any historical event invalidates every successor.
2. **HMAC-SHA256 authentication** (optional) — each event is signed with a
   shared secret so only processes holding the key can produce valid entries.
3. **Redis Streams** as the append-only storage layer, with a CAS-guarded
   head pointer for concurrent safety.

Together these properties satisfy the SOC2 CC7.2 (monitoring of controls)
and CC7.3 (detection of security incidents) trust criteria documented in
`core/audit/soc2.go`.

## Data Model

### Redis Keyspace

| Key Pattern | Type | Purpose |
|---|---|---|
| `audit:chain:<tenant>` | Stream | Ordered event log, one entry per event |
| `audit:chain:head:<tenant>` | String | `"<seq>:<event_hash>"` — current chain tip |

### Event Schema (`SIEMEvent`)

| Field | Type | Description |
|---|---|---|
| `timestamp` | RFC 3339 | When the event occurred |
| `event_type` | string | e.g. `safety.decision`, `system.auth` |
| `severity` | string | `CRITICAL` / `HIGH` / `MEDIUM` / `LOW` / `INFO` |
| `tenant_id` | string | Tenant scope (chain is per-tenant) |
| `seq` | int64 | Monotonic per-tenant sequence number (1-based) |
| `event_hash` | hex string | SHA-256 of canonical payload (see below) |
| `prev_hash` | hex string | `event_hash` of the previous event (empty for genesis) |
| `hmac` | hex string | HMAC-SHA256 of canonical payload (optional, see below) |

## Hash Chain Mechanics

### Canonical Hashing

The event hash is SHA-256 of the JSON encoding with these fields **zeroed
before hashing**: `seq`, `event_hash`, `hmac`. Critically, `prev_hash` is
**included** in the hashed bytes:

```text
event_hash = SHA-256(canonical_json(event with seq=0, event_hash="", hmac=""))
```

This means:

- Modifying any field of any past event changes its `event_hash`.
- Because the next event's `prev_hash` recorded the original hash, the next
  event now fails verification too.
- The corruption **cascades forward** through the entire chain.

### CAS Append (Lua Script)

Appending is protected by a Compare-and-Set Lua script that atomically:

1. Reads the current head pointer
2. Compares it to the caller's expected value
3. If they match: `XADD` the event, `SET` the new head
4. If they don't: returns 0 (CAS miss → Go retries with jitter)

This prevents two concurrent producers from both seeing the same head and
both committing at the same sequence number.

```text
┌─────────┐    ┌──────────┐    ┌───────────┐
│ Go CAS  │───▶│ Lua      │───▶│ Redis     │
│ Retry   │    │ Script   │    │ Stream    │
│ Loop    │◀───│ (atomic) │    │ + Head    │
└─────────┘    └──────────┘    └───────────┘
  (1024 max      (single-
  retries)        threaded)
```

### Anti-Head-Poison Guard

If an attacker `DEL`s the head key while the stream still has entries, a
naive CAS check would allow seq=1 to be reused. The Lua script guards
against this: when the caller claims genesis (empty head), it additionally
checks `XLEN == 0`.

## HMAC Authentication

### Threat Model

SHA-256 hash chaining detects **external tampering** (someone modifies
entries in the stream). However, an attacker with **Redis write access**
could recompute the entire chain from any point — the hashes are
deterministic from the event payload alone.

HMAC-SHA256 closes this gap. With HMAC enabled, producing a valid chain
entry requires possession of the signing key. An attacker who can write to
Redis but doesn't have the key cannot forge valid HMAC tags.

### Configuration

Set the `CORDUM_AUDIT_HMAC_KEY` environment variable to a **hex-encoded**
key of at least 32 bytes (256 bits). Hex is consistent with `event_hash`
encoding elsewhere in the audit chain:

```bash
# Generate a 256-bit key (64 hex chars = 32 bytes)
openssl rand -hex 32

# Set it
export CORDUM_AUDIT_HMAC_KEY="<hex-encoded-key>"
```

When not set, HMAC is disabled and the chain operates with SHA-256 only.

### Key Rotation

HMAC key rotation is a two-phase process:

1. **Deploy new key**: Update `CORDUM_AUDIT_HMAC_KEY` on all gateway replicas
   and restart them. New events are signed with the new key.

2. **Understand verification semantics during rotation**:
   - **Events with no `hmac` tag** (pre-HMAC era): reported as `hmac_skipped`
     — these are not treated as failures.
   - **Events signed with the old key**: when verified with the new key, they
     will show `hmac_mismatch` — this is expected during rotation. Operators
     should note the `first_seq` / `last_seq` boundary where the key changed.
   - **Events signed with the new key**: will verify as `hmac_verified`.

> **Security note**: The HMAC key is sourced from the `CORDUM_AUDIT_HMAC_KEY`
> environment variable at gateway boot and is NOT accepted as a query parameter.
> URLs are routinely logged, cached by proxies, and stored in browser history,
> making them unsafe for secret material.

## Verification

### Verify Endpoint

`GET /api/v1/audit/verify?tenant=<tenant_id>`

Optional parameters:
- `since` — unix millisecond timestamp (inclusive lower bound on stream IDs)
- `until` — unix millisecond timestamp (inclusive upper bound on stream IDs)
- `limit` — max events to scan (default: 10,000; max: 100,000)

### Verify Result

```json
{
  "status": "ok",
  "total_events": 150,
  "verified_events": 150,
  "gaps": [],
  "retention_boundary_seq": 1,
  "first_seq": 1,
  "last_seq": 150,
  "hmac_verified": 150,
  "hmac_skipped": 0
}
```

### Status Values

| Status | Meaning |
|---|---|
| `ok` | Every event verified (hash + linkage + HMAC) |
| `compromised` | At least one event failed verification |
| `partial` | Gaps exist but are all `retention_trimmed` (expected) |

### Gap Types

| Gap Type | Meaning |
|---|---|
| `missing` | Seq absent from stream (after retention boundary) |
| `out_of_order` | Seq appeared before its predecessor |
| `hash_mismatch` | Event hash or prev_hash linkage failed |
| `hmac_mismatch` | HMAC-SHA256 tag verification failed |
| `retention_trimmed` | Expected gap due to log retention policy |

## Pipeline Architecture

```text
┌──────────────┐      ┌──────────┐      ┌──────────────┐      ┌──────────┐
│ Gateway      │─────▶│ NATS Bus │─────▶│ Consumer     │─────▶│ SIEM     │
│ Handler      │      │          │      │ (chain then  │      │ Exporter │
│              │      │          │      │  export)     │      │          │
└──────────────┘      └──────────┘      └──────────────┘      └──────────┘
  emit SIEMEvent       sys.audit.        1. Chainer.Append     webhook/
                       export            2. Export              syslog/
                                                                dd/cw
```

The consumer owns chain ordering — it runs as a single NATS queue-group
replica per tenant, so concurrent producers across gateway replicas don't
race on sequence numbers.

## Failure Modes

Controlled by `CORDUM_AUDIT_CHAIN_FAIL`:

| Mode | Behavior on chain failure |
|---|---|
| `strict` (default) | Drop event, ack NATS message |
| `permissive` | Log warning, export without chain fields |

## Retention

Audit retention is entitlement-gated:

| Plan | Retention |
|---|---|
| Community | 7 days |
| Pro | Configurable |
| Enterprise | Unlimited + legal hold |

The verify endpoint distinguishes retention-trimmed gaps from tampering
so operators don't get false positives on long-running chains.
