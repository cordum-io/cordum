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
- **Cluster bus**: TLS on port 16379 (`tls-cluster yes`, `cluster-port 16379`), using the existing server and client certificates
- **Replication**: over TLS (`tls-replication yes`) with the `cordum-client-tls` certificate and `masterauth`
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
1. **Before recreating Redis**, stop the Cordum services, suspend the backup and
   retention CronJobs, and delete their unfinished Jobs using the stop block in
   [production.md](./production.md#restore-procedures). Pause GitOps or other
   controllers that could reapply Deployment replica counts. Keep these controls
   in place through restore verification. If the namespace was lost, restore its
   namespace, secrets, PVCs, and policies first, with workloads still stopped.
2. Re-create only the Redis resources; do **not** apply the full production overlay,
   which would restart services and CronJobs against the empty cluster:
   ```bash
   set -eu
   kubectl delete job cordum-redis-cluster-init -n cordum --ignore-not-found --cascade=foreground --wait --timeout=5m
   kubectl apply -f deploy/k8s/production/redis.yaml
   kubectl apply -f deploy/k8s/production/redis-backup-scripts.yaml
   ```
3. Wait for all 6 pods and the recreated init Job:
   ```bash
   kubectl rollout status statefulset/cordum-redis -n cordum --timeout=5m
   kubectl wait --for=condition=complete job/cordum-redis-cluster-init -n cordum --timeout=300s
   ```
4. With services and scheduled jobs still stopped, restore the latest backup with
   [Restore From Backup](#restore-from-backup). The restore imports every key into
   the re-created cluster by slot, so the new node IDs and slot layout need no
   extra step.
5. Resume the services and the CronJobs as production.md describes
   (`kubectl apply -k deploy/k8s/production` brings the services back at their
   usual replica counts).

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

### Runbook H: Turn On TLS for the Cluster Bus

Use this procedure for a running production cluster deployed from an overlay
without `tls-cluster yes`. Fresh installs form the cluster with TLS on the bus
through the cluster-init Job; they need no runtime switch.

**Before starting**: schedule downtime, stop the Cordum services and take a backup
([section 7](#7-backups)). Deleting a primary causes failover to its replica;
replication is asynchronous, so writes not yet replicated can be lost. Start only
with all six nodes connected, `cluster_state:ok` on every node and all three
replicas reporting `master_link_status:up`.

1. Apply the overlay:
   ```bash
   kubectl apply -k deploy/k8s/production
   ```
   Running Redis pods keep their config: `redis.conf` is a `subPath` mount that
   Redis reads only at startup. Applying the overlay can restore the Cordum
   services' replica counts; stop those services again before continuing.

2. Switch **all six running nodes** before restarting any Redis pod:
   ```bash
   for i in 0 1 2 3 4 5; do
     echo "cordum-redis-$i"
     kubectl exec -n cordum cordum-redis-$i -c redis -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --tls --cacert /etc/redis/tls/ca.crt --cert /etc/redis/client-tls/tls.crt --key /etc/redis/client-tls/tls.key CONFIG SET tls-cluster yes'
   done
   ```
   Require an `OK` from each of the six nodes; stop here if any command fails or
   returns anything else. Existing bus links stay up; new connections use TLS.
   A node that starts with the setting while a peer's bus is still plain stays
   outside the cluster, and rejoins once this runtime switch covers every node.

3. Restart pods **one at a time**, in order **5, 4, 3, 2, 1, 0**. Set `i` to the
   next ordinal only after the checks below pass; do not put these deletes in an
   unattended loop:
   ```bash
   i=5
   kubectl delete pod -n cordum cordum-redis-$i
   kubectl wait -n cordum --for=condition=Ready pod/cordum-redis-$i --timeout=180s
   ```
   If the replacement pod is not yet created, wait for it to appear and repeat
   the Ready check. Then use the same TLS/auth options as step 2 to check
   `CLUSTER INFO`, `CLUSTER NODES` and `INFO replication` on **every node**.
   Require `cluster_state:ok` on all six, exactly six connected nodes in each
   view (no `fail`, `handshake` or `noaddr` flags), and `master_link_status:up` on
   all three replicas. Do not delete the next pod until these checks pass.
   Do not use `kubectl rollout restart`: the TCP readiness probe checks port
   6379, not cluster membership or replica links.

4. Confirm the persisted setting on all six restarted nodes and check the cluster:
   ```bash
   for i in 0 1 2 3 4 5; do
     echo "cordum-redis-$i"
     kubectl exec -n cordum cordum-redis-$i -c redis -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --tls --cacert /etc/redis/tls/ca.crt --cert /etc/redis/client-tls/tls.crt --key /etc/redis/client-tls/tls.key CONFIG GET tls-cluster'
   done
   kubectl exec -n cordum cordum-redis-0 -c redis -- sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli --tls --cacert /etc/redis/tls/ca.crt --cert /etc/redis/client-tls/tls.crt --key /etc/redis/client-tls/tls.key --cluster check cordum-redis-0.cordum-redis.cordum.svc:6379'
   ```
   Each `CONFIG GET` must report `yes`, and `--cluster check` must pass before
   resuming the Cordum services. With the bus on TLS, `redis-cli --cluster reshard`,
   `--cluster rebalance` and `--cluster fix` can move keys between nodes; use the
   same TLS/auth options for these operations.

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
   # Base manifests (deploy/k8s/base.yaml): a single-pod Deployment, no TLS, password required
   REDIS_POD=$(kubectl get pod -n cordum -l app=redis -o jsonpath='{.items[0].metadata.name}')
   kubectl exec -n cordum "$REDIS_POD" -- sh -c 'redis-cli -a "$REDIS_PASSWORD" BGSAVE'
   kubectl cp "cordum/$REDIS_POD:/data/dump.rdb" ./dump.rdb
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
   kubectl exec -n cordum cordum-redis-0 -c redis -- sh -c 'redis-cli --tls \
     --cacert /etc/redis/tls/ca.crt --cert /etc/redis/client-tls/tls.crt --key /etc/redis/client-tls/tls.key \
     -a "$REDIS_PASSWORD" --no-auth-warning CLUSTER INFO'
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

1. Reads each node's own `CLUSTER NODES` entry and records primary node IDs,
   configuration epochs, and slots in `redis-<ts>.topology`.
2. Requires exactly three distinct primaries covering all 16,384 slots once, with
   no migrating/importing slots, and the same topology before and after each RDB.
3. Runs `redis-cli --rdb` and `redis-check-rdb` for every primary.
4. Writes `redis-<ts>.sha256` only after all checks pass, binding all three RDBs and
   the topology. Any failure exits nonzero; incomplete files are not restorable by
   this runbook. Retain the printed manifest digest outside the backup PVC as
   [production.md](./production.md#restore-procedures) describes.

Retention: 7 days (`cordum-backup-retention` CronJob runs at 03:30 UTC daily).

### Restore From Backup

A backup run writes one file per primary, `redis-<ts>-node<N>.rdb`, where `N` is the
ordinal of the pod that was a primary at `<ts>`. The topology and checksum manifest
must be from the same completed run; three RDB files alone do not establish shard
coverage. The restore imports the files into
the running cluster: a throwaway Redis in a toolbox pod loads one file at a time, and
`redis-cli --cluster import` sends each key to the primary that owns its slot now.
That works for any slot layout, including a cluster resharded since the backup or
re-created by [Runbook D](#runbook-d-full-cluster-loss), and no Redis pod stops or
restarts. Do not copy a backup file onto a Redis pod's volume and restart the pod:
with `appendonly yes`, Redis loads its AOF on startup and ignores the RDB file.

Legacy or partial backups without recorded topology cannot establish shard
coverage for this procedure. Do not manufacture topology from the current cluster
or from a count of RDB files; qualify their recovery separately before deleting
production data.

Stop the Cordum services, suspend the backup and retention CronJobs, and delete
active backup and retention Jobs with foreground deletion and wait for their pods to stop first
([production.md](./production.md#restore-procedures)). Then:

1. **Start a toolbox pod.** It has the client certificate, the password and the
   backups, and carries the Redis backup CronJob's label, which the production
   NetworkPolicies admit to Redis's port 6379 and DNS. Its memory request and limit
   let the throwaway Redis load the largest backup file, which can be as large as a
   Redis pod's 1Gi limit, with room to spare; without them the namespace LimitRange
   would cap the pod at 256Mi.

   ```bash
   kubectl apply -n cordum -f - <<'EOF'
   apiVersion: v1
   kind: Pod
   metadata:
     name: cordum-redis-toolbox
     labels:
       app: cordum-redis-backup
   spec:
     restartPolicy: Never
     containers:
     - name: toolbox
       image: redis:7
       command: ["sleep", "infinity"]
       resources:
         requests: {cpu: 500m, memory: 1Gi}
         limits: {cpu: "2", memory: 2Gi}
       env:
       - name: REDISCLI_AUTH
         valueFrom:
           secretKeyRef: {name: cordum-redis-secret, key: REDIS_PASSWORD}
       volumeMounts:
       - {name: client-tls, mountPath: /etc/cordum/tls/client, readOnly: true}
       - {name: backup, mountPath: /backup, readOnly: true}
       - {name: backup-scripts, mountPath: /etc/cordum/backup, readOnly: true}
     volumes:
     - name: client-tls
       secret: {secretName: cordum-client-tls}
     - name: backup
       persistentVolumeClaim: {claimName: cordum-backups, readOnly: true}
     - name: backup-scripts
       configMap: {name: cordum-redis-backup-scripts}
   EOF
   kubectl wait -n cordum --for=condition=Ready pod/cordum-redis-toolbox --timeout=120s
   kubectl exec -n cordum cordum-redis-toolbox -- ls /backup
   ts=20260213T120002Z   # Redis backup id (redis-<ts>-node<N>.rdb), chosen independently of the NATS backup
   redis_manifest_sha256=REPLACE_WITH_CATALOG_SHA256  # from the trusted external backup catalog
   ```

   The commands below run in the toolbox and start with
   `r="redis-cli --tls --cacert ... --key ..."`, the client-certificate flags every
   Redis call needs. `REDISCLI_AUTH` supplies the password.

2. **Check the backup and the cluster, then empty the cluster.** This is the point
   of no return: the script deletes every key in the cluster. It gets there only
   after all six nodes answer, `--cluster check` passes, the trusted manifest binds
   three distinct shards covering every slot, and `redis-check-rdb` accepts every
   file. Redis 7 cluster import embeds its password in a command format string;
   percent signs are format characters and ASCII spaces split arguments. This
   runbook rejects either character before deletion. Rotate to a compatible password or
   qualify another import method first. If a check fails,
   it stops with the cluster unchanged. No other step deletes keys.

   ```bash
   kubectl exec -n cordum cordum-redis-toolbox -- env ts="$ts" redis_manifest_sha256="$redis_manifest_sha256" sh -c '
     set -eu
     . /etc/cordum/backup/guards.sh
     redis_import_password_check
     r="redis-cli --tls --cacert /etc/cordum/tls/client/ca.crt --cert /etc/cordum/tls/client/tls.crt --key /etc/cordum/tls/client/tls.key"
     for i in 0 1 2 3 4 5; do
       pong=$($r -h cordum-redis-$i.cordum-redis.cordum.svc -p 6379 PING)
       if [ "$pong" != PONG ]; then echo "cordum-redis-$i: $pong"; exit 1; fi
     done
     $r --cluster check cordum-redis-0.cordum-redis.cordum.svc:6379
     cd /backup
     test "${#redis_manifest_sha256}" -eq 64
     case "$redis_manifest_sha256" in *[!0-9a-f]*) exit 1 ;; esac
     printf "%s  redis-%s.sha256\n" "$redis_manifest_sha256" "$ts" | sha256sum -c -
     test "$(wc -l < "redis-$ts.sha256")" -eq 4
     sha256sum -c "redis-$ts.sha256"
     awk -f /etc/cordum/backup/coverage.awk "redis-$ts.topology"
     grep -Fx "$(sha256sum "redis-$ts.topology")" "redis-$ts.sha256"
     while read -r node rest; do
       grep -Fx "$(sha256sum "redis-$ts-$node.rdb")" "redis-$ts.sha256"
       redis-check-rdb "redis-$ts-$node.rdb"
     done < "redis-$ts.topology"
     redis_topology > /tmp/restore.topology
     $r --cluster call cordum-redis-0.cordum-redis.cordum.svc:6379 FLUSHALL --cluster-only-masters
     for i in 0 1 2 3 4 5; do
       h=cordum-redis-$i.cordum-redis.cordum.svc
       repl=$($r -h $h -p 6379 INFO replication)
       case $repl in *role:master*)
         n=$($r -h $h -p 6379 DBSIZE)
         if [ "$n" != 0 ]; then echo "cordum-redis-$i: $n keys left"; exit 1; fi ;;
       esac
     done
     echo "cluster empty"'
   ```

3. **Import the backup.** For each file, the script starts a throwaway Redis on
   `127.0.0.1:6390` that loads the file, and `redis-cli --cluster import` sends every
   key to the primary that owns its slot, replacing a key that is already there.
   `--tls-cluster yes` and the client certificate make the throwaway's MIGRATE
   connections use TLS and present that certificate, as port 6379 requires. MIGRATE
   keeps each key's remaining TTL. Keys with a TTL can expire while the restore runs,
   so the script counts the keys without one: the primaries must end up holding
   exactly as many as the files.

   ```bash
   kubectl exec -n cordum cordum-redis-toolbox -- env ts=$ts sh -c '
     set -eu
     . /etc/cordum/backup/guards.sh
     redis_import_password_check
     r="redis-cli --tls --cacert /etc/cordum/tls/client/ca.crt --cert /etc/cordum/tls/client/tls.crt --key /etc/cordum/tls/client/tls.key"
     t="env -u REDISCLI_AUTH redis-cli -p 6390"
     persistent() { printf "%s\n" "$1" | awk -F "[=,]" "/^db0:/ {n += \$2 - \$4} END {print n + 0}"; }
     want=0
     redis_topology > /tmp/import.topology
     cmp /tmp/restore.topology /tmp/import.topology
     while read -r node rest; do
       f=/backup/redis-$ts-$node.rdb
       redis_topology > /tmp/current.topology
       cmp /tmp/import.topology /tmp/current.topology
       test -s "$f"
       $t SHUTDOWN NOSAVE >/dev/null 2>&1 || true
       redis-server --port 6390 --bind 127.0.0.1 --dir /backup --dbfilename "$(basename "$f")" \
         --appendonly no --save "" --daemonize yes --logfile /tmp/throwaway.log --tls-cluster yes \
         --tls-cert-file /etc/cordum/tls/client/tls.crt --tls-key-file /etc/cordum/tls/client/tls.key \
         --tls-ca-cert-file /etc/cordum/tls/client/ca.crt
       i=0
       until [ "$($t PING 2>/dev/null)" = PONG ]; do
         i=$((i + 1))
         if [ $i -gt 600 ]; then cat /tmp/throwaway.log; exit 1; fi
         sleep 1
       done
       info=$($t INFO keyspace)
       want=$((want + $(persistent "$info")))
       if ! $r --cluster import cordum-redis-0.cordum-redis.cordum.svc:6379 \
           --cluster-from 127.0.0.1:6390 --cluster-copy --cluster-replace >/tmp/import.log 2>&1; then
         tail -n 20 /tmp/import.log; exit 1
       fi
       echo "$f: imported"
       redis_topology > /tmp/current.topology
       cmp /tmp/import.topology /tmp/current.topology
     done < /backup/redis-$ts.topology
     $t SHUTDOWN NOSAVE >/dev/null 2>&1 || true
     got=0
     for i in 0 1 2 3 4 5; do
       h=cordum-redis-$i.cordum-redis.cordum.svc
       repl=$($r -h $h -p 6379 INFO replication)
       case $repl in *role:master*)
         info=$($r -h $h -p 6379 INFO keyspace)
         got=$((got + $(persistent "$info"))) ;;
       esac
     done
     echo "keys without a TTL: $want in the backup, $got in the cluster"
     test "$got" -eq "$want"
     redis_topology > /tmp/current.topology
     cmp /tmp/import.topology /tmp/current.topology'
   ```

   The topology checks compare primary identities, epochs, and slots before and
   after each import and after counting. A failover or reshard invalidates the
   restore even when TTL-excluded counts match. If any check fails, keep services
   stopped and restart at **step 2** after the cluster stabilizes: revalidate the
   backup and cluster, clear the partially restored keys, then import every shard
   again. Do not replace the saved topology file to bypass a mismatch.

4. **Verify:**

   ```bash
   kubectl exec -n cordum cordum-redis-toolbox -- sh -c '
     set -eu
     . /etc/cordum/backup/guards.sh
     r="redis-cli --tls --cacert /etc/cordum/tls/client/ca.crt --cert /etc/cordum/tls/client/tls.crt --key /etc/cordum/tls/client/tls.key"
     $r --cluster check cordum-redis-0.cordum-redis.cordum.svc:6379
     for i in 0 1 2 3 4 5; do
       repl=$($r -h cordum-redis-$i.cordum-redis.cordum.svc -p 6379 INFO replication)
       case $repl in
         *role:master*) echo "cordum-redis-$i: primary" ;;
         *master_link_status:up*) echo "cordum-redis-$i: replica, link up" ;;
         *) echo "cordum-redis-$i: replica link not up"; exit 1 ;;
       esac
     done
     redis_topology > /tmp/current.topology
     cmp /tmp/import.topology /tmp/current.topology'
   ```

   Expect `[OK] All 16384 slots covered.`, no open slots, and every replica's link up.
   A replica whose link is still down is catching up with its primary; run the check
   again a minute later. Then delete the toolbox pod
   (`kubectl delete pod -n cordum cordum-redis-toolbox`) and continue with
   [production.md](./production.md#restore-procedures), which ends with **Resume**.

## Cross-References

- [K8s Deployment Guide](./k8s-deployment.md) — Cluster init, client connection
- [Production Guide](./production.md) — Redis connection lost runbook
- [Horizontal Scaling Guide](./horizontal-scaling.md) — PDB rationale
- [Configuration Reference](./configuration-reference.md) — Redis env vars
