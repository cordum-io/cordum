# Audit Chain — Migration Note

This note explains how to migrate an existing Cordum deployment onto the
per-tenant audit hash chain introduced in the Phase-1 governance release.
Read it once before the upgrade, then again during the cutover.

## What changes on disk

Before this release every audit event landed in the SIEM exporter with
the fields defined in `core/audit/exporter.go` — timestamp, event type,
severity, tenant, action, and the rest. There was no ordering guarantee
beyond NATS delivery order and no tamper evidence.

After this release every event carries three additional JSON fields —
`seq`, `event_hash`, `prev_hash` — populated by the `Chainer` at the
audit consumer. A new per-tenant Redis Stream `audit:chain:<tenant>` and
head pointer `audit:chain:head:<tenant>` persist the chain state.

## What happens to pre-existing events

Events produced before the upgrade have empty `seq` / `prev_hash` /
`event_hash`. They are **invisible to verify** — `GET /api/v1/audit/verify`
walks the Redis Stream, which starts at the first chained event.

**There is no retro-hashing.** The chain begins at the first event that
carries a `prev_hash` field for a given tenant. That event is the
tenant's **genesis**. Verification refuses to cross the genesis
boundary: seqs below the first present entry are `retention_trimmed`
or absent-by-design.

This is deliberate. Retroactively hashing old events would embed a
"last good state" into the chain that nobody signed off on; a real
attacker could have mutated an event in the pre-chain window and we'd
bake that mutation into the verification record.

## Dev / staging re-seed

For dev environments the simplest path is to wipe the old SIEM state
and start clean so the chain is end-to-end verifiable:

```bash
# Stop the gateway + consumer.
make dev-down

# Flush the old audit state — only safe on dev / staging.
redis-cli FLUSHDB
# OR, more surgically:
redis-cli --scan --pattern 'audit:chain:*' | xargs -r redis-cli DEL
redis-cli --scan --pattern 'audit:stream:*' | xargs -r redis-cli DEL

# Bring the stack back up. The first event emitted per tenant becomes
# that tenant's genesis.
make dev-up
```

## Production migration

The production sequence is a staged drain to avoid losing either
pre-chain events (which you still want in the SIEM archive) or
first-chained events (which form the genesis record).

1. **Freeze high-velocity producers** if you can tolerate it. Goal: no
   events in flight during the swap. If you can't freeze, accept that
   a small number of events may straddle the boundary — they'll
   export cleanly to SIEM but verify-walk won't know about them.
2. **Drain the NATS queue.** Watch `sys.audit.export` until the
   depth is zero:
   ```bash
   nats consumer info audit audit-exporters
   ```
3. **Snapshot Redis.** `BGSAVE` + copy the RDB to cold storage. If
   anything goes wrong you can roll back.
4. **Deploy the new gateway + consumer.** The new binaries start
   emitting chain fields immediately; the Redis Streams are created on
   the first event per tenant.
5. **Verify.** Submit a known job (e.g. a noop safety check) to produce
   one audit event per active tenant, then:
   ```bash
   cordumctl audit verify <tenant>
   # Expect: status=ok, total_events=1, retention_boundary_seq=1.
   ```
6. **Unfreeze producers.**

## Cross-boundary verify

If an operator runs `cordumctl audit verify --since 0 <tenant>` after
the migration, the response's `retention_boundary_seq` equals the seq
of the tenant's genesis event. Everything below that boundary is
reported as `retention_trimmed` — expected behaviour, not tampering.
The dashboard's AuditChainCard renders the boundary explicitly so
operators see the cutoff at a glance.

## Rollback

If the chain pipeline misbehaves in production:

1. Switch `CORDUM_AUDIT_CHAIN_FAIL=permissive` as a short-term safety
   net. Events still export even if chain append fails; a WARN is
   logged and SIEM stays intact.
2. If that is not enough, redeploy the pre-chain gateway binary. The
   new chain fields are additive JSON — old exporters ignore them —
   so rolling back does not break the SIEM feed. The chain state in
   Redis is left untouched and can be re-used when you roll forward.

## Checklist

- [ ] Read `docs/deployment/audit-chain.md` first.
- [ ] Backup Redis before upgrade.
- [ ] Drain NATS `audit-exporters` queue.
- [ ] Deploy new binaries.
- [ ] Smoke-test with `cordumctl audit verify`.
- [ ] Update runbooks to include the `compromised` response playbook.
