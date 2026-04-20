# Redis Operations Guide

This document covers Redis cluster topology, key inventory, per-service dependency mapping, disaster recovery runbooks, and migration procedures.

## 1. Cluster Topology

### Production (deploy/k8s/production/)

- **StatefulSet**: `cordum-redis`, 6 pods (`cordum-redis-0` through `cordum-redis-5`)
- **Layout**: 3 primaries + 3 replicas (`--cluster-replicas 1`)
- **Hash slots**: 16384 total, distributed evenly across 3 primaries (~5461 each)
- **Node timeout**: 5000ms — a node unresponsive for 5s is marked as potentially failed
- **Persistence**: AOF enabled (`appendonly yes`), 20Gi PVC per node
- **TLS**: Full mutual TLS (`tls-auth-clients yes`), plain port disabled (`port 0`)
- **Exporter**: `oliver006/redis_exporter:v1.58.0` sidecar on port 9121
- **Anti-affinity**: Preferred pod anti-affinity on `kubernetes.io/hostname` (best-effort spread)
- **PDB**: `minAvailable: 2` for primaries during node drains (see `ha.yaml`)
  - **Note**: The PDB covers all 6 pods. With `minAvailable: 4`, at most 2 pods can be evicted simultaneously, preserving at least 2 primaries + 2 replicas.

### Base (deploy/k8s/base.yaml)

- **Deployment**: Single-replica `redis` (not a StatefulSet, no cluster mode)
- **No TLS**: Plain `redis-server --appendonly yes --requirepass $REDIS_PASSWORD`
- **Service**: ClusterIP `redis:6379`
- **No exporter**, no cluster init job, no PDB

### DNS Names

| Node | FQDN |
|------|------|
| cordum-redis-0 | `cordum-redis-0.cordum-redis.cordum.svc.cluster.local` |
| cordum-redis-1 | `cordum-redis-1.cordum-redis.cordum.svc.cluster.local` |
| cordum-redis-2 | `cordum-redis-2.cordum-redis.cordum.svc.cluster.local` |
| cordum-redis-3 | `cordum-redis-3.cordum-redis.cordum.svc.cluster.local` |
| cordum-redis-4 | `cordum-redis-4.cordum-redis.cordum.svc.cluster.local` |
| cordum-redis-5 | `cordum-redis-5.cordum-redis.cordum.svc.cluster.local` |

## 2. Key Prefix Inventory

All keys follow the convention `<namespace>:<type>:<id>`. Keys are distributed across hash slots by CRC16 of the key (or hash tag if present).

### Job Keys (`job:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `job:state:<jobID>` | String | 7d | Job state (PENDING, SCHEDULED, RUNNING, etc.) |
| `job:meta:<jobID>` | Hash | 7d | Job metadata (tenant, principal, topic, labels) |
| `job:req:<jobID>` | String | 7d | Serialized job request (protobuf JSON) |
| `job:result_ptr:<jobID>` | String | 7d | Pointer to result data in memory fabric |
| `job:events:<jobID>` | List | 7d | Job state transition events |
| `job:decisions:<jobID>` | List | 7d | Historical safety decisions (JSON) |
| `job:<jobID>:output_decision` | String | 7d | Output policy evaluation result |
| `job:idempotency:<tenant>:<key>` | String | 7d | Tenant-scoped idempotency dedup |
| `job:recent` | Sorted Set | 7d | Recently updated jobs (score = timestamp) |
| `job:deadline` | Sorted Set | 7d | Jobs with deadlines (score = deadline time) |
| `job:index:<state>` | Sorted Set | 7d | Jobs indexed by state |
| `job:tenant:active:<tenant>` | Set | 7d | Active job IDs per tenant |
| `trace:<traceID>` | Set | 7d | Job IDs grouped by execution trace |

TTL controlled by `JOB_META_TTL_SECONDS` (default 604800 = 7 days).

### Workflow Keys (`wf:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `wf:def:<workflowID>` | String | None | Workflow definition (JSON) |
| `wf:index:org:<orgID>` | Sorted Set | None | Workflow IDs by organization |
| `wf:index:all` | Sorted Set | None | All workflow IDs |
| `wf:run:<runID>` | String | None | Workflow run document (JSON) |
| `wf:runs:<workflowID>` | Sorted Set | None | Run IDs for a workflow |
| `wf:runs:all` | Sorted Set | None | All run IDs |
| `wf:runs:status:<status>` | Sorted Set | None | Run IDs by status |
| `wf:runs:active:<orgID>` | Set | None | Active run IDs per org |
| `wf:run:timeline:<runID>` | List | None | Timeline events (capped at 1000) |
| `wf:run:idempotency:<key>` | String | None | Workflow idempotency dedup |

### Config Keys (`cfg:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `cfg:system:<id>` | String | None | System-level config (default ID: `default`) |
| `cfg:org:<orgID>` | String | None | Organization config |
| `cfg:team:<teamID>` | String | None | Team config |
| `cfg:workflow:<workflowID>` | String | None | Workflow config |
| `cfg:step:<stepID>` | String | None | Step config |

Write-once: services cache on startup. Delete `cfg:system:default` to force reload.

### Auth Keys (`auth:*`, `user:*`, `session:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `auth:<keyID>` | String | None | API key record (bcrypt hash, role, tenant) |
| `auth:prefix:<prefix>` | Set | None | Key IDs by `ck_XXXXXXXX` prefix |
| `auth:tenant:<tenant>` | Set | None | Key IDs per tenant |
| `user:<tenant>:<username>` | String | None | User record (password hash, role) |
| `user:id:<userID>` | String | None | User reference (`tenant:username`) |
| `user:email:<tenant>:<email>` | String | None | Email → username index |
| `user:tenant:<tenant>` | Set | None | User IDs per tenant |
| `session:<token>` | String | 1h | Session token → auth context |
| `login:failed:<user>:<ip>` | String | 15m | Per-IP failed login counter |
| `login:failed:global:<user>` | String | 15m | Global failed login counter |

### Context / Result Keys (`ctx:*`, `res:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `ctx:<jobID>` | String | 24h | Job context/memory window data |
| `res:<jobID>` | String | 24h | Job result data |

TTL controlled by `REDIS_DATA_TTL_SECONDS` (default 86400 = 24h).

### DLQ Keys (`dlq:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `dlq:entry:<jobID>` | String | 30d | Dead letter entry (JSON) |
| `dlq:index` | Sorted Set | None | DLQ entries by creation time |

TTL controlled by `CORDUM_DLQ_ENTRY_TTL_DAYS` (default 30).

### Lock Keys (`cordum:*`)

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `cordum:scheduler:job:<jobID>` | String | 60s | Distributed job processing lock |
| `cordum:reconciler:default` | String | 2×poll | Reconciler leader lock |
| `cordum:wf:run:lock:<runID>` | String | 30s | Workflow run operation lock |
| `cordum:workflow-engine:reconciler:default` | String | 2×poll | Workflow reconciler lock |
| `cordum:scheduler:snapshot:writer` | String | 10s | Snapshot writer leader lock |
| `cordum:dlq:cleanup` | String | 1h | DLQ cleanup leader lock |
| `cordum:wf:delay:timers` | Sorted Set | None | Delay timers (score = fire time) |

### Rate Limit Keys

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `cordum:rl:<tenant>:<unixSec>` | String | 2s | Sliding window rate limit counter |

### Circuit Breaker Keys

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `cordum:cb:safety:failures` | String | 30s | Input safety circuit breaker counter |
| `cordum:cb:safety:output:failures` | String | 30s | Output safety circuit breaker counter |

### Worker / System Keys

| Key Pattern | Type | TTL | Purpose |
|-------------|------|-----|---------|
| `sys:workers:snapshot` | String | None | JSON snapshot of all registered workers |

## 3. Per-Service Redis Dependency

| Service | Key Prefixes Used | Impact if Redis Down | Graceful Degradation? |
|---------|-------------------|---------------------|----------------------|
| **api-gateway** | `job:*`, `auth:*`, `user:*`, `session:*`, `cfg:*`, `dlq:*`, `wf:*`, `cordum:rl:*` | Cannot authenticate, submit jobs, list state, or serve any API | No — all state is in Redis |
| **scheduler** | `job:*`, `cordum:scheduler:*`, `cordum:reconciler:*`, `cordum:cb:*`, `sys:workers:*`, `cfg:*` | Cannot dispatch, lock, or transition jobs; circuit breaker falls back to local | Partial — circuit breaker has local fallback |
| **safety-kernel** | None directly (policy from file/URL) | No impact | Yes — fully independent of Redis |
| **workflow-engine** | `wf:*`, `job:*`, `cordum:wf:*`, `cfg:*` | Cannot execute workflows, fire timers, or coordinate runs | No — all workflow state is in Redis |
| **context-engine** | `ctx:*`, `res:*` | Cannot store or retrieve context/results | No — Redis is the backing store |
| **dashboard** | None (connects via api-gateway) | Loses API connectivity (shows errors) | N/A — depends on gateway |
| **cordum-mcp** | Via api-gateway | Same as dashboard | N/A |

## 4. Disaster Recovery Runbooks

### Runbook A: Single Replica Failure

**Scenario**: One replica pod (`cordum-redis-3/4/5`) becomes unreachable.

**Impact**: Minimal. Reads from that replica are rerouted to the primary. No data loss.

**Steps**:
1. Check pod status: `kubectl get pods -l app=redis -n cordum`
2. If the pod is in CrashLoopBackOff, check logs: `kubectl logs cordum-redis-N -n cordum`
3. Delete the pod to trigger restart: `kubectl delete pod cordum-redis-N -n cordum`
4. Verify recovery: `redis-cli --tls ... -h cordum-redis-N... CLUSTER INFO` → `cluster_state:ok`

**Recovery time**: Automatic, typically < 30s for pod restart.

### Runbook B: Single Primary Failure

**Scenario**: A primary node (`cordum-redis-0/1/2`) becomes unreachable.

**Impact**: Moderate. Redis Cluster auto-promotes the replica to primary within `cluster-node-timeout` (5s). During failover, writes to the affected hash slots fail with `CLUSTERDOWN` or are redirected.

**Steps**:
1. Verify auto-failover occurred:
   ```bash
   redis-cli --tls ... CLUSTER NODES | grep master
   # Should show 3 masters (one will be the promoted replica)
   ```
2. When the failed node recovers, it rejoins as a replica automatically.
3. If the node doesn't recover, delete the pod:
   ```bash
   kubectl delete pod cordum-redis-N -n cordum
   ```
4. After pod restart, verify it rejoined the cluster:
   ```bash
   redis-cli --tls ... -h cordum-redis-N... CLUSTER NODES
   ```

**Recovery time**: 5-15s for automatic failover, then application reconnection.

### Runbook C: Two or More Nodes Lost

**Scenario**: Two or more nodes are lost simultaneously (e.g., node drain of co-located pods).

**Impact**: If both a primary and its replica for the same slot range are lost, the cluster enters `FAIL` state and cannot serve those slots.

**Steps**:
1. Check cluster state:
   ```bash
   redis-cli --tls ... CLUSTER INFO
   # cluster_state:fail means slots are not covered
   ```
2. Check which slots are unassigned:
   ```bash
   redis-cli --tls ... CLUSTER SLOTS
   ```
3. If pods are recoverable, wait for restart:
   ```bash
   kubectl get pods -l app=redis -n cordum -w
   ```
4. If slots are still uncovered after pod recovery, re-add the node:
   ```bash
   redis-cli --tls ... CLUSTER MEET <recovered-node-ip> 6379
   ```
5. Verify all 16384 slots are assigned:
   ```bash
   redis-cli --tls ... CLUSTER INFO | grep cluster_slots_ok
   # Expected: cluster_slots_ok:16384
   ```

**Prevention**: PDB (`minAvailable: 4`) prevents Kubernetes from evicting more than 2 pods during voluntary disruptions.

### Runbook D: Full Cluster Loss

**Scenario**: All 6 Redis pods are lost (e.g., namespace deletion, persistent storage failure).

**Impact**: Total — all Cordum state is lost. Services return HTTP 500.

**Steps**:
1. Re-create the StatefulSet if deleted:
   ```bash
   kubectl apply -k deploy/k8s/production
   ```
2. Wait for all 6 pods:
   ```bash
   kubectl get pods -l app=redis -n cordum -w
   ```
3. Re-run the cluster init job:
   ```bash
   kubectl delete job cordum-redis-cluster-init -n cordum 2>/dev/null
   kubectl apply -k deploy/k8s/production
   kubectl wait --for=condition=complete job/cordum-redis-cluster-init -n cordum --timeout=120s
   ```
4. Restore from latest multi-shard backup:
   ```bash
   # List available backups
   kubectl exec -it <any-pod-with-backup-pvc> -n cordum -- ls -la /backup/redis-*.rdb

   # For each primary, restore the corresponding shard:
   # Stop the cluster, copy RDB files to /data on each primary, restart.
   # WARNING: This is a manual process — coordinate with the team.
   ```
5. Restart all Cordum services to re-establish connections:
   ```bash
   kubectl rollout restart deployment -n cordum
   ```

**Recovery time**: 10-30 minutes depending on data volume.

### Runbook E: Split-Brain

**Scenario**: Network partition causes the cluster to split into two groups, each believing the other has failed.

**Impact**: Both sides may promote replicas, leading to conflicting writes. After partition heals, data on the minority side is lost (Redis uses last-failover-wins).

**Steps**:
1. Identify the partition:
   ```bash
   # Run on each node
   redis-cli --tls ... -h cordum-redis-N... CLUSTER NODES
   # Compare views — nodes on each side will show the other side as "fail"
   ```
2. Wait for the network to heal. Redis will auto-resolve once connectivity is restored.
3. After resolution, check for data consistency:
   ```bash
   redis-cli --tls ... DBSIZE  # On each primary
   ```
4. If data was lost on the minority side, affected keys must be re-created by the application (jobs re-submitted, etc.).

**Prevention**: Use pod anti-affinity to spread Redis pods across nodes/zones.

### Runbook F: Stuck Slot Migration

**Scenario**: A slot is stuck in `migrating` or `importing` state after a failed rebalance operation.

**Steps**:
1. Identify stuck slots:
   ```bash
   redis-cli --tls ... CLUSTER NODES | grep -E "migrating|importing"
   ```
2. Fix the stuck slot:
   ```bash
   redis-cli --tls ... CLUSTER SETSLOT <slot> STABLE
   # Run on BOTH the source and destination node
   ```
3. Verify:
   ```bash
   redis-cli --tls ... CLUSTER INFO | grep cluster_slots_ok
   # Expected: 16384
   ```

### Runbook G: Cluster Init Job Failure

**Scenario**: The `cordum-redis-cluster-init` Job fails during initial deployment.

**Common causes**:
- Not all 6 pods are ready yet (the init job polls with `PING` but may time out)
- TLS certificates not mounted or incorrect
- `REDIS_PASSWORD` secret is empty

**Steps**:
1. Check job logs:
   ```bash
   kubectl logs job/cordum-redis-cluster-init -n cordum
   ```
2. Verify all pods are ready:
   ```bash
   kubectl get pods -l app=redis -n cordum
   # All 6 must be Running and Ready (1/1 or 2/2 with exporter)
   ```
3. Verify TLS secret:
   ```bash
   kubectl get secret cordum-client-tls -n cordum
   ```
4. Delete and re-run:
   ```bash
   kubectl delete job cordum-redis-cluster-init -n cordum
   kubectl apply -k deploy/k8s/production
   ```

## 5. Base to Production Migration

This procedure migrates from the single-node `base.yaml` Redis Deployment to the 6-node production Redis Cluster.

### Pre-Flight Checklist

- [ ] TLS certificates generated (`cordum-redis-server-tls`, `cordum-client-tls` secrets)
- [ ] `cordum-redis-secret` has a strong `REDIS_PASSWORD`
- [ ] PVCs provisioned (6 × 20Gi for data + 20Gi for backups)
- [ ] Application downtime window scheduled (migration requires a service restart)

### Migration Steps

1. **Export data from single-node Redis** (optional if starting fresh):
   ```bash
   kubectl exec -it redis-0 -n cordum -- redis-cli BGSAVE
   kubectl cp cordum/redis-0:/data/dump.rdb ./dump.rdb
   ```

2. **Scale down all Cordum services** to prevent writes during migration:
   ```bash
   kubectl scale deployment --all --replicas=0 -n cordum
   ```

3. **Apply production overlay** (creates StatefulSet, headless service, init job):
   ```bash
   kubectl apply -k deploy/k8s/production
   ```

4. **Wait for cluster init** to complete:
   ```bash
   kubectl wait --for=condition=complete job/cordum-redis-cluster-init -n cordum --timeout=300s
   ```

5. **Verify cluster health**:
   ```bash
   kubectl exec -it cordum-redis-0 -n cordum -- redis-cli --tls \
     --cacert /etc/redis/tls/ca.crt --cert /etc/redis/tls/tls.crt --key /etc/redis/tls/tls.key \
     CLUSTER INFO
   # Expect: cluster_state:ok, cluster_slots_ok:16384, cluster_known_nodes:6
   ```

6. **Update service environment variables** to use cluster addresses:
   ```
   REDIS_CLUSTER_ADDRESSES=cordum-redis-0.cordum-redis.cordum.svc:6379,...,cordum-redis-5.cordum-redis.cordum.svc:6379
   ```

7. **Delete the old single-node Deployment**:
   ```bash
   kubectl delete deployment redis -n cordum
   kubectl delete service redis -n cordum
   ```

8. **Scale Cordum services back up**:
   ```bash
   kubectl scale deployment --all --replicas=1 -n cordum
   # Or apply your desired replica counts
   ```

9. **Verify**:
   - Submit a test job and verify it completes
   - Check dashboard connectivity
   - Verify backup CronJob triggers on schedule

### Rollback

If the migration fails, scale down services, delete the production overlay resources, and re-apply `base.yaml`. The single-node Redis will start fresh (data from the cluster is not backward-compatible with single-node).

## 6. Monitoring & Alerts

Six Redis cluster alerts are defined in `deploy/k8s/production/monitoring.yaml`:

| Alert | Expression | For | Severity |
|-------|-----------|-----|----------|
| `CordumRedisClusterDegraded` | `min(redis_cluster_state) == 0` | 1m | critical |
| `CordumRedisClusterNodeDown` | `count(up{service="cordum-redis"} == 1) < 6` | 5m | warning |
| `CordumRedisClusterSlotsIncomplete` | `min(redis_cluster_slots_ok) < 16384` | 2m | critical |
| `CordumRedisMemoryHigh` | `max(redis_memory_used / redis_memory_max) > 0.8` | 10m | warning |
| `CordumRedisReplicationBroken` | `min(redis_connected_slaves{role="master"}) < 1` | 5m | warning |
| `CordumRedisBackupFailed` | `kube_job_status_failed{job_name=~"cordum-redis-backup.*"} > 0` | 1h | warning |

All metrics are exposed by the existing `redis_exporter` sidecar.

## 7. Backups

The `cordum-redis-backup` CronJob runs hourly and backs up **all primaries** (not just node-0):

1. Iterates all 6 nodes, identifies primaries via `INFO replication`
2. Runs `redis-cli --rdb` against each primary: `redis-{timestamp}-node{N}.rdb`
3. Tolerates individual node failures (partial backup is better than no backup)
4. Exits non-zero only if zero primaries were backed up

Retention: 7 days (`cordum-backup-retention` CronJob runs at 03:30 UTC daily).

## Cross-References

- [K8s Deployment Guide](./k8s-deployment.md) — Cluster init, client connection
- [Production Guide](./production.md) — Redis connection lost runbook
- [Horizontal Scaling Guide](./horizontal-scaling.md) — PDB rationale
- [Configuration Reference](./configuration-reference.md) — Redis env vars
