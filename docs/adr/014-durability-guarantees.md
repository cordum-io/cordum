# ADR-014: Durability Guarantees

- Status: Proposed
- Date: 2026-11-25
- Scope: M4-01, acknowledgement levels and RPO and RTO targets per state class; not multi-region

## Context

Roadmap M4 needs a written answer to two questions: which state has to outlive a crash, a
failover or a restore, and what a caller may rely on once a write has been acknowledged. This ADR
gives that answer as targets. It changes no setting and no code. No acknowledgement level, RPO or
RTO below is met or measured today: M4-05 (Redis) and M4-06 (JetStream) apply the settings, M4-08
tests a restore, and M4-11 measures RPO and RTO in a recovery drill.

Source baseline: integration/next `074e3bdcda9d480e047afc31b9e3e4e52f10c68b`. Every `path:line`
reference below was read at that commit.

At the baseline:
- Redis holds every state class below that exists today. The production overlay runs Redis Cluster
  with three primaries and one replica each (`deploy/k8s/production/redis.yaml:221`), with
  `appendonly yes` (`deploy/k8s/production/redis.yaml:22`) and no `appendfsync` line, so the
  Redis default `everysec` applies. Redis replicates asynchronously, and no Cordum write path calls
  `WAIT` or `WAITAOF`. A Redis acknowledgement therefore means only that a primary accepted the
  write.
- JetStream is off unless `NATS_USE_JETSTREAM` is set (`core/infra/bus/nats.go:978`); the base
  manifests set it (`deploy/k8s/base.yaml:753`). Streams use file storage
  (`core/infra/bus/nats.go:1149`) with `NATS_JS_REPLICAS` replicas, default 1
  (`core/infra/bus/nats.go:1127`); the production overlay sets 3
  (`deploy/k8s/production/patches/tls-env.yaml:26`). Durable subjects are published with
  `js.Publish`, which returns after the stream acknowledges the message
  (`core/infra/bus/nats.go:447`). Other subjects use core NATS publishes, which are not
  acknowledged.
- Hourly CronJobs snapshot each Redis primary and both streams
  (`deploy/k8s/production/backup.yaml:19`, `deploy/k8s/production/backup.yaml:100`), and a
  retention job deletes backups after 7 days (`deploy/k8s/production/backup.yaml:182`).
  docs/production.md section 6 lists a 1 hour RPO for Redis and for JetStream and gives no RTO.
- The compose HA override keeps Redis and NATS single-instance (`docker-compose.ha.yaml:4`).

The [failure-behavior contract](../contract/failure-behavior.md) (M0-06) names this row as an owner
of gaps G01 (Redis) and G02 (NATS). Its action classes (low, approval, protected) and its rules
RQ-1 to RQ-5 are used here unchanged.

## Decision

- Use three acknowledgement levels. A caller may rely on what its level states, and on nothing
  more:
  - **L0, accepted**: a primary holds the write in memory. A crash or a failover before
    replication can lose it. Every Redis write is L0 today.
  - **L1, replicated**: before success is returned, the write is on the primary and on at least
    one replica. Redis: `WAIT 1 500` returns 1 or more. JetStream: the publish acknowledgement of
    a stream created with `NATS_JS_REPLICAS=3`, which two of the three servers must store. The loss
    of one node keeps the write; losing both copies within one fsync interval can still lose it.
  - **L2, replicated and synced**: before success is returned, the write is fsynced to the
    append-only file on the primary and on at least one replica. Redis: `WAITAOF 1 1 2000`
    (Redis 7.2 or later) returns at least 1 and 1. The write is lost only if both synced copies
    are lost.
- A write that does not reach its level before the timeout is a failed write. For an
  authorization class the effect is not started (RQ-1), and no class reports the operation as
  successful (I08). The write may still have been applied, so every retry must be idempotent.
- Authorization state needs L2: consumed grants, revocations, the authority epoch, execution
  state transitions, approval consumptions, and the evidence for approval-class and protected
  effects. Job state, approval decisions, replay records and low-risk evidence need L1.
- A restore from backup loses every write after the backup. For the authorization classes the
  restore procedure makes that loss unusable instead of trying to prevent it:
  1. advance the authority epoch past every value issued before the restore, so every earlier
     grant fails its epoch check;
  2. treat approvals recorded before the restore point as not consumable for approval-class and
     protected actions until they are approved again;
  3. re-apply revocations and session deletions recorded after the backup point from a copy kept
     outside the restored cluster, and until that is done treat delegation and session tokens
     issued before the restore as revoked;
  4. move every execution record that is not terminal to Outcome unknown, so that it reconciles
     before any retry.
- RPO and RTO are targets for two events: **failover**, the loss of one Redis or NATS node while
  each cluster keeps its quorum, and **restore**, a fresh environment rebuilt from the newest
  backup. The Inventory gives them per class. None is measured.
- Run Redis Cluster without Sentinel; see Sentinel.
- Rejected: `appendfsync always` for every write. Every write would wait for a local fsync, a
  write acknowledged by a primary whose disk is lost would still be gone, and the writes that need
  durability can ask for it one at a time with `WAITAOF`.
- Rejected: shrinking the restore loss by backing up after every authorization write. Backups stay
  hourly; the restore rules above carry the safety argument.

## Inventory

Cells give targets, except Lives today, which describes the baseline. "Failover" and "restore" are
the two events defined in the Decision. `<ms>` timeouts are the values named in the Decision.

| Class | Lives today | Ack guarantee | RPO target | RTO target | Provided by | May be lost | Detected by |
|---|---|---|---|---|---|---|---|
| consumed grants | does not exist yet (M2A-06) | L2 before the executor starts the effect | failover 0; restore 1 h, made unusable by restore rule 1 | failover 60 s; restore 4 h | `WAITAOF 1 1 2000` (not set today) with `appendonly yes`, `appendfsync everysec` and `min-replicas-to-write 1` | Failover: nothing acknowledged. Restore: every consumption after the backup | Restore rule 1: a grant issued before the restore fails the epoch check (M2A-05), so a lost consumption cannot be reused. Failover loss: not detected today |
| revocations | delegation tokens `core/auth/delegation/revocation.go:14`; agent session tokens `core/controlplane/scheduler/session_token.go:57`; API keys `core/controlplane/gateway/auth/keystore_redis.go:191`; worker credentials `core/controlplane/workercredentials/service.go:256`, in a config document `core/configsvc/service.go:341`; user sessions are deleted `core/controlplane/gateway/auth/userstore_redis.go:760`; grant, approval and principal revocations: does not exist yet (M2A-05) | L2 before the revoke call returns success | failover 0; restore 1 h, re-applied by restore rule 3 | failover 60 s; restore 4 h | `WAITAOF 1 1 2000` (not set today) with `appendonly yes` and `min-replicas-to-write 1` | Restore: every revocation and session deletion after the backup; the credential works again until it expires or rule 3 re-applies it | Not detected today. Target: restore rule 3 compares revocation audit events newer than the backup with the restored store (M4-08, M4-10) |
| authority epoch | does not exist yet (M2A-05) | L2; the stored value never decreases | failover 0; restore 1 h, then advanced by restore rule 1 | failover 60 s; restore 4 h | `WAITAOF 1 1 2000` (not set today) with `appendonly yes` and `min-replicas-to-write 1` | Restore: every epoch advance after the backup | Target (M2A-05): a grant that carries an epoch above the current one shows a rollback; it is refused and raises an alert. Not detected today |
| execution state | does not exist yet (M2A-06) | L2 for each transition into Claimed and Executing and for the recorded outcome | failover 0; restore 1 h, made safe by restore rule 4 | failover 60 s; restore 4 h | `WAITAOF 1 1 2000` (not set today) with `appendonly yes` and `min-replicas-to-write 1` | Restore: every transition after the backup, including effects the target has already applied | Target (M2A-06): the recovery rule moves an Executing record with no outcome to Outcome unknown; restore rule 4 applies it to every record that is not terminal (M4-11). Not detected today |
| approvals | job approvals in job metadata `core/infra/store/job_store.go:72`, written by `core/infra/store/job_store.go:2027`; Edge approvals `core/edge/approval_store_redis.go:19`, consumed at `core/edge/approval_store_redis.go:414`; MCP tool approvals `core/controlplane/gateway/mcp_approvals.go:40`, consumed at `core/controlplane/gateway/mcp_approvals.go:539` | L2 for a consumption; L1 for a decision | failover 0; restore 1 h, made unusable by restore rule 2 | failover 60 s; restore 4 h | consumption `WAITAOF 1 1 2000`, decision `WAIT 1 500` (neither set today) | Restore: decisions and consumptions after the backup. A lost decision returns the request to pending; a lost consumption makes the approval usable again | Not detected today. Target: restore rule 2 refuses approvals recorded before the restore point for approval-class and protected actions (M4-08) |
| audit evidence | audit chain stream `core/audit/chain.go:36`; decision log `core/infra/store/decision_log_store.go:23`; per-job decisions `core/infra/store/job_store.go:1284`; Edge events `core/edge/store_redis.go:254`; audit export on a durable subject `core/infra/bus/nats.go:1193` | L2 before an approval-class or protected effect (RQ-5); L1 for low-risk, which may be marked pending | approval and protected: failover 0; low: failover 1 s; restore 1 h, with the gap reported | failover 60 s; restore 4 h | `WAITAOF 1 1 2000` and `WAIT 1 500` (not set today); `NATS_JS_REPLICAS=3` for the export | Restore: evidence after the backup. Failover: low-risk evidence inside the replication lag | `VerifyChain` (`core/audit/chain_verify.go:141`) reports missing sequence numbers and hash mismatches inside the chain. A lost tail or a lost stream is not detected today (M4-12) |
| replay records | CAP packet replay `core/infra/replay/redis_store.go:25`; Edge runtime nonces `core/edge/runtimeingest/replay.go:16`; MCP outbound nonces `core/mcp/outbound/nonce.go:91`; worker handshake challenges `core/controlplane/scheduler/handshake_challenge.go:21`; job idempotency keys `core/infra/store/job_store.go:1288`; bus processed markers `core/infra/bus/nats.go:326` | L1 | failover 1 s; restore: entries still inside their TTL are lost | failover 60 s; restore 4 h | `WAIT 1 500` (not set today); `Duplicates: 2 * time.Minute` on both streams | Entries inside the replication lag or newer than the backup; a replay inside the entry's TTL is then accepted as first seen | Not detected today. A lost CAP replay entry is bounded by the packet's expiry (`core/infra/replay/redis_store.go:125`) |
| job state | job records `core/infra/store/job_store.go:27`, `core/infra/store/job_store.go:29`, `core/infra/store/job_store.go:30`; job runtime events `core/infra/store/job_event_store.go:16`; workflow runs `core/workflow/store_redis.go:914`; job messages in the jobs stream `core/infra/bus/nats.go:1165` | L1 before a dispatch is reported (RQ-4) | failover 1 s; restore 1 h | failover 60 s; restore 4 h | `WAIT 1 500` (not set today); `NATS_USE_JETSTREAM=1`, `NATS_JS_REPLICAS=3`, `sync_interval: "1s"`; hourly `cordum-redis-backup` and `cordum-nats-backup` | State changes inside the lag or after the backup; messages older than `NATS_JS_MAX_AGE` | The reconciler moves stale DISPATCHED and RUNNING jobs to TIMEOUT, never SUCCEEDED (`core/controlplane/scheduler/reconciler.go:139`); unacknowledged messages are redelivered (`core/infra/bus/nats.go:486`). A job lost entirely is not detected today |

Notes:
- The restore RPO of 1 h is the backup interval and matches docs/production.md section 6. That
  table gives no failover RPO and no RTO; the values above are new targets.
- An RPO of 0 means that no write acknowledged at the class's level is lost. It says nothing about
  writes that failed their level, which the caller has already treated as failed.
- The audit chain lives in the same Redis as the classes it records, so a restore rolls it back
  with them. Only a copy kept outside the cluster can show what a restore lost.
- Every approvals, revocations and replay-records cell above covers several stores. M4-05 applies
  the level to each write path it lists; a path left at L0 is a gap, not an exception.

## Redis and JetStream settings

Redis, production overlay:
- `appendonly yes`: keep. Set at `deploy/k8s/production/redis.yaml:22` and `deploy/k8s/base.yaml:388`.
- `appendfsync everysec`: write it explicitly. Not set today; the Redis default applies.
- `min-replicas-to-write 1` and `min-replicas-max-lag 10`: not set today. A primary that has lost
  its replica stops accepting writes after 10 seconds, which limits what a cut-off primary can
  accept and later lose.
- `cluster-node-timeout 5000`: keep (`deploy/k8s/production/redis.yaml:26`). A primary on the
  minority side of a partition stops accepting writes after this timeout. Writes it accepted in
  that window are lost when its replica is promoted; an L1 or L2 write in that window gets no
  replica acknowledgement and fails, so it is not acknowledged.
- `--cluster-replicas 1`: keep (`deploy/k8s/production/redis.yaml:221`). With one replica per
  primary, the replica that acknowledged `WAIT` or `WAITAOF` is the only failover candidate. More
  replicas per primary would need a higher count for the same property.
- `WAIT 1 500` after each L1 write and `WAITAOF 1 1 2000` after each L2 write: not set today, no
  caller. `WAITAOF` waits for the next fsync, up to about one second with `everysec`, hence the
  2000 ms timeout.
- Redis 7.2 or later, pinned by digest: not set today. The overlay uses the floating `redis:7` tag
  (`deploy/k8s/production/redis.yaml:81`), and `WAITAOF` needs 7.2.
- `REDIS_CLUSTER_ADDRESSES`: keep. The client reads it (`core/infra/redisutil/redisutil.go:24`)
  and the overlay sets all six nodes (`deploy/k8s/production/patches/tls-env.yaml:36`).
- `cordum-redis-backup`, hourly (`deploy/k8s/production/backup.yaml:19`): keep.

JetStream, production overlay:
- `NATS_USE_JETSTREAM=1`: keep (`deploy/k8s/base.yaml:753`). Without it every publish is a core
  NATS publish and has no acknowledgement.
- `NATS_JS_REPLICAS=3` on a 3-server cluster: keep (`deploy/k8s/production/patches/tls-env.yaml:26`,
  `deploy/k8s/production/nats.yaml:47`, disruption budget `deploy/k8s/production/ha.yaml:209`).
  An existing stream keeps its replica count, because stream setup treats an existing stream as
  success and does not update it (`core/infra/bus/nats.go:1158`).
- File storage (`core/infra/bus/nats.go:1149`) and `sync_interval: "1s"`
  (`deploy/k8s/production/patches/nats-config.yaml:15`): keep. With three replicas this gives L1.
  `sync_interval: always` would be needed for L2, and no class above needs L2 from JetStream.
- `NATS_JS_MAX_AGE`, default 7 days (`core/infra/bus/nats.go:81`): a message older than this is
  removed even if no consumer acknowledged it. Target: longer than the longest time a job may
  wait, and at least the backup retention.
- `Duplicates: 2 * time.Minute` (`core/infra/bus/nats.go:1152`): the message-id window. A publish
  retried after it is stored twice, so consumers must deduplicate (I06).
- `AckExplicit`, `NATS_JS_ACK_WAIT` (default 10 minutes, `core/infra/bus/nats.go:80`) and
  `MaxDeliver(100)` (`core/infra/bus/nats.go:489`): keep. They make delivery to consumers at
  least once, never exactly once.
- `cordum-nats-backup`, hourly (`deploy/k8s/production/backup.yaml:100`): keep.

Compose and any single-node Redis cannot provide L1 or L2: `WAIT` returns 0 there.

## Sentinel

Decision: Redis Sentinel is not needed in addition to Redis Cluster, and the qualified profile
does not use it.

- Cluster already fails a primary over to its replica through its own failure detection, bounded
  by `cluster-node-timeout`. Sentinel provides failover for a primary and replicas that do not run
  in cluster mode. The two are alternative topologies; Sentinel does not manage a Cluster.
- Neither topology changes what an acknowledgement means. Both replicate asynchronously, so the
  levels come from `WAIT`, `WAITAOF` and `min-replicas-to-write`, which work the same in either.
- The client has no Sentinel mode. `core/infra/redisutil` builds cluster options from
  `REDIS_CLUSTER_ADDRESSES` (`core/infra/redisutil/redisutil.go:102`) and sets no Sentinel master
  name, so Sentinel would need client changes and a second topology for M4-05 to qualify.
- Rejected: Sentinel with a single primary. It would remove the single-slot limit on scripts, but
  it puts every write on one primary and adds a Sentinel quorum to operate.

docs/production.md section 1 lists Redis Sentinel as an option. Correcting that statement belongs
to M4-01-DOCS; this row does not edit it.

## Consequences

- M2A-05, M2A-06 and M2A-09 write their records at L2 and treat a write that misses its level as
  failed. M4-05 applies the Redis settings to the existing approval, revocation, evidence, replay
  and job write paths and runs the failover test; M4-06 does the same for JetStream.
- M4-08 implements restore rules 1 to 4 and reports the evidence gap after the backup point.
  M4-11 measures RPO and RTO for both events. M4-12 makes a lost audit tail detectable.
- An L2 write waits for the next append-only-file fsync on the primary and on the replica, up to
  about one second. Protected operations accept that latency.
- With `min-replicas-to-write 1`, a primary whose replica is gone refuses writes. Authorization
  writes then fail closed until the replica returns or the cluster fails over.
- Restore rules 2 and 3 cost availability: approvals must be given again, and agents and users
  must obtain new tokens. That is the price of a 1 h restore RPO for authorization state.
- A deployment without replicas, including compose, cannot acknowledge L1 or L2 writes, so it
  cannot run an authorization write that requires them.

## Open questions

1. How the client sends `WAIT` and `WAITAOF` on the connection that made the write. Both commands
   have no key, and a cluster client may route them to another node (M4-05).
2. Whether `WAIT 1 500` and `WAITAOF 1 1 2000` are the right timeouts. They are starting targets;
   M4-05 sets them from measurements.
3. Which copy outside the cluster restore rule 3 reads (the SIEM export or the JetStream audit
   export), and how long it is kept (M4-08, M4-10).
4. Whether signed inputs issued before a restore are refused for the rest of their TTL, because
   their replay records may be gone (M4-08).
5. How a single-node development deployment runs authorization writes: through an explicit,
   logged mode that is not qualified, or not at all (M4-05).
6. How existing streams move to three replicas, since stream setup does not update them (M4-06).
7. How a job removed from its stream by `NATS_JS_MAX_AGE` before any consumer acknowledged it is
   detected (M4-06).
8. Redis and JetStream snapshots are taken independently, not at one point in time. How restore
   reconciles the two (M4-08).
