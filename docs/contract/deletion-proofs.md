# Deletion and legal-hold proof map

Status: requirements map; cleanup and legal-hold proofs pending the named slices.
Reviewed source: `9de7911274f1574d94cf9c850bd73a88dc2cc123` (58 inventory entries).
Tags: retention, deletion, legal-hold, contract.

This document records requirements and proofs, not qualification of a deployment.

## Rule

A retention proof writes a marker through the real writer, requires a positive
trace before cleanup, drives the real TTL/sweeper/trim path, then finds no trace
in the writer client's Redis keyspace and every export the entry reaches.
`tracescan.Find` searches the configured logical DB or every cluster master,
not other logical DBs. Use quiescent fixtures; SCAN is not a concurrent snapshot.
Non-Redis stores need their own search. Name the exports in the proof.
A category identifies a cleanup path, not a guarantee that every part of a mixed
entry is erased. Partial paths (for example C12 chunks/summary, C24 deadline and tenant-active sets,
C27 idempotency keys and C54 persistent config) must not be treated as full erasure.
A failing proof stays an open finding under the review process, not a weaker test.

## Inventory map

| Entry | Category | Cleanup path | Proof |
| --- | --- | --- | --- |
| DI-E01 | no cleanup path | none | none: No session-state cleanup API; Save replaces a file and removes its temporary file only |
| DI-E02 | no cleanup path | none | none: Process termination and garbage collection; no explicit secure erasure routine |
| DI-E03 | retention cleanup | `core/edge/agentd/cache.go` (`SafeAllowCache.Get`); `core/edge/agentd/cache.go` (`SafeAllowCache.evictOverflowLocked`) | `TestRetentionProofDIE03` |
| DI-E04 | no cleanup path | none | none: Garbage collection of scan buffers; scanner does not delete source configuration files |
| DI-C01 | retention cleanup | `core/edge/sweeper.go` (`SessionSweeper.SweepOnce`); `core/edge/store_redis.go` (`RedisStore.DeleteSession`) | `TestRetentionProofDIC01` |
| DI-C02 | no cleanup path | none | none: No code path deletes approval records at reviewed_at; transitions rewrite records and drop some index refs, while status/principal index refs remain; ApprovalSweeper.SweepOnce only marks past-deadline pending approvals expired; DeleteSession skips approval keys |
| DI-C03 | retention cleanup | `core/edge/shadow/finding_store_redis.go` (`RedisStore.commitTransitionFinding`); `core/edge/shadow/finding_store_redis.go` (`RedisStore.purgeExpired`) | `TestRetentionProofDIC03` |
| DI-C04 | no cleanup path | none | none: No automatic audit-stream trimmer found at reviewed_at; storage deletion is operator-managed |
| DI-C05 | retention cleanup | `core/controlplane/gateway/handlers_policy_bundles.go` (`server.appendPolicyAudit`) | `TestRetentionProofDIC05` |
| DI-C06 | retention cleanup | `core/infra/store/decision_log_store.go` (`RedisDecisionLogStore.AppendDecision`); `core/infra/store/decision_log_store.go` (`RedisDecisionLogStore.cleanupStaleDecisionIDs`); `core/infra/store/decision_log_store.go` (`RedisDecisionLogStore.QueryDecisions`); `cmd/cordumctl-governance-helper/governance.go` (`governanceRuntime.appendDecisionRecord`) | `TestRetentionProofDIC06` |
| DI-C07 | no cleanup path | none | none: No automatic head-key deletion path found; operator-managed storage deletion |
| DI-C08 | retention cleanup | `core/controlplane/safetykernel/kernel.go` (`server.setPolicyWithInvariants`) | `TestRetentionProofDIC08` |
| DI-C09 | retention cleanup | `core/workflow/store_redis.go` (`RedisStore.UpdateAuditHash`); `core/workflow/store_redis.go` (`enqueuePendingAuditDeletes`) | `TestRetentionProofDIC09` |
| DI-C10 | retention cleanup | `core/infra/artifacts/redis_store.go` (`RedisStore.Put`); `core/infra/artifacts/redis_store.go` (`RedisStore.ttlFor`); `core/infra/artifacts/content_store.go` (`RedisStore.PutContent`); `core/infra/artifacts/content_store.go` (`RedisStore.RetainContent`) | `TestRetentionProofDIC10` |
| DI-C11 | retention cleanup | `core/infra/store/redis_store.go` (`RedisStore.DeleteContext`); `core/infra/store/redis_store.go` (`RedisStore.PutResult`) | `TestRetentionProofDIC11` |
| DI-C12 | retention cleanup | `core/contextwindow/engine/service.go` (`Service.UpdateMemory`) | `TestRetentionProofDIC12` |
| DI-C13 | retention cleanup | `core/telemetry/store.go` (`Store.SaveSnapshot`); `core/telemetry/store.go` (`Store.trimHistory`) | `TestRetentionProofDIC13` |
| DI-C14 | no cleanup path | none | none: No automatic deletion of persistent telemetry metadata; operator-managed removal |
| DI-C15 | no cleanup path | none | none: No Cordum deletion path for already exported metrics or telemetry copies |
| DI-C16 | no cleanup path | none | none: No Cordum backup-deletion path; operators manage persistence files and backup copies |
| DI-C17 | no cleanup path | none | none: No Cordum deletion path for NATS snapshot files or external backup copies |
| DI-C18 | retention cleanup | `core/edge/mcp_upstream_store_redis.go` (`RedisMCPUpstreamRegistry.updateExisting`); fixed 30d TTL | `TestRetentionProofDIC18` |
| DI-C19 | explicit delete only | `core/audit/buffer.go` (`BufferedExporter.loop`); `core/audit/buffer.go` (`BufferedExporter.Close`) | none: Queue drain/flush or retry exhaustion, not retention erasure; exported copies remain. |
| DI-C20 | no cleanup path | none | none: No Cordum remote deletion of downloaded evidence; recipient must remove its copies |
| DI-C21 | retention cleanup | `core/infra/bus/nats.go` (`NatsBus.initJetStreamFromEnv`) | `TestRetentionProofDIC21` |
| DI-C22 | retention cleanup | `core/infra/store/job_store.go` (`RedisJobStore.SetStateWithContext`); `core/infra/store/job_event_store.go` (`RedisJobStore.ApplyJobResult`); `core/infra/store/job_store.go` (`RedisJobStore.SetWorkerID`); `core/infra/store/job_index_sweep.go` (`RedisJobStore.SweepExpiredJobIndexes`) | `TestRetentionProofDIC22` |
| DI-C23 | no cleanup path | none | none: No code path deletes trace sets at reviewed_at |
| DI-C24 | retention cleanup | `core/infra/store/job_store.go` (`RedisJobStore.SetStateWithContext`); `core/infra/store/job_index_sweep.go` (`RedisJobStore.SweepExpiredJobIndexes`) | `TestRetentionProofDIC24` |
| DI-C25 | retention cleanup | `core/infra/store/job_store.go` (`RedisJobStore.TrySetIdempotencyKeyScoped`); `core/infra/store/job_store.go` (`RedisJobStore.SetIdempotencyKeyScoped`) | `TestRetentionProofDIC25` |
| DI-C26 | explicit delete only | `core/workflow/store_redis.go` (`RedisStore.DeleteWorkflow`) | none: Definitions persist until DeleteWorkflow. |
| DI-C27 | explicit delete only | `core/workflow/store_redis.go` (`RedisStore.DeleteRun`); `core/workflow/store_redis.go` (`RedisStore.DeleteRunIdempotencyKey`); `core/workflow/store_redis.go` (`RedisStore.RemoveDelayTimer`); `core/workflow/store_redis.go` (`RedisStore.PopFiredDelays`); `core/workflow/store_redis.go` (`RedisStore.CleanStaleDelays`) | none: No TTL setting; runs persist until DeleteRun, timelines keep the newest 1000 events and run idempotency keys never expire. |
| DI-C28 | retention cleanup | `core/infra/store/dlq_store.go` (`DLQStore.Add`); `core/infra/store/dlq_store.go` (`DLQStore.Delete`); `core/infra/store/dlq_store.go` (`DLQStore.CleanupStaleEntries`) | `TestRetentionProofDIC28` |
| DI-C29 | no cleanup path | none | none: No identity deletion; Delete rewrites the record as revoked and UnlinkWorker removes only worker links |
| DI-C30 | retention cleanup | `core/edge/store_redis.go` (`RedisStore.ReserveIdempotency`); `core/edge/store_redis.go` (`RedisStore.ReleaseIdempotency`) | `TestRetentionProofDIC30` |
| DI-C31 | retention cleanup | `core/edge/shadow/finding_store_redis.go` (`RedisStore.cleanupStaleIDTx`); `core/edge/shadow/finding_store_redis.go` (`RedisStore.pruneFindingIndexes`) | `TestRetentionProofDIC31` |
| DI-C32 | no cleanup path | none | none: No exception deletion path; revoke drops the id from the tenant index and CreateException deletes only its own failed write |
| DI-C33 | no cleanup path | none | none: No upstream deletion path at reviewed_at; Disable only flips the enabled flag |
| DI-C34 | retention cleanup | `core/mcp/dedupe_redis.go` (`RedisDedupeStore.Delete`); `core/edge/runtimeingest/replay.go` (`ReplayWindow.Release`); `core/infra/bus/nats.go` (`NatsBus.jsCallbackCtx`) | `TestRetentionProofDIC34`, `TestRetentionProofDIC34/cap_replay`, `TestRetentionProofDIC34/bus_markers` |
| DI-C35 | no cleanup path | none | none: No hold deletion path; ReleaseHold rewrites the record as released |
| DI-C36 | explicit delete only | `core/infra/store/eval_dataset_store.go` (`EvalDatasetStore.DeleteEvalDataset`) | none: Datasets persist until DeleteEvalDataset. |
| DI-C37 | retention cleanup | `core/infra/store/eval_run_store.go` (`EvalRunStore.CreateRun`); `core/infra/store/eval_run_store.go` (`EvalRunStore.DeleteRun`); `core/infra/store/eval_run_store.go` (`EvalRunStore.GCExpired`); `core/controlplane/gateway/handlers_evals_runs.go` (`server.deleteEvalRunPending`) | `TestRetentionProofDIC37` |
| DI-C38 | no cleanup path | none | none: No user-record deletion; Delete disables the record and drops its email index, tenant membership and sessions; user:id entries remain |
| DI-C39 | retention cleanup | `core/controlplane/gateway/auth/userstore_redis.go` (`RedisUserStore.DeleteSession`); `core/controlplane/gateway/auth/userstore_redis.go` (`RedisUserStore.DeleteUserSessions`); `core/controlplane/gateway/auth/userstore_redis.go` (`RedisUserStore.ClearFailedLogins`); `core/controlplane/gateway/auth/userstore_redis.go` (`RedisUserStore.DeleteSAMLState`); `core/controlplane/gateway/auth/oidc_flow.go` (`redisOIDCStateStore.Delete`) | `TestRetentionProofDIC39` |
| DI-C40 | no cleanup path | none | none: No key-record deletion; Revoke marks the record revoked and drops only its prefix lookup |
| DI-C41 | no cleanup path | none | none: No deletion path; the token is overwritten when set again and SCIM user metadata stays after deactivation |
| DI-C42 | explicit delete only | `core/controlplane/gateway/auth/scim.go` (`RedisUserStore.deleteSCIMGroup`) | none: Groups persist until explicit deleteSCIMGroup. |
| DI-C43 | no cleanup path | none | none: No record deletion; revocation rewrites the record and drops the id from the active index only |
| DI-C44 | retention cleanup | `core/auth/delegation/revocation.go` (`RedisRevocationStore.Revoke`); `core/auth/delegation/cascade.go` (`RedisRevocationStore.CascadeRevoke`); `core/controlplane/gateway/handlers_delegation.go` (`server.allowDelegationIssue`) | `TestRetentionProofDIC44`, `TestRetentionProofDIC44Cascade` |
| DI-C45 | retention cleanup | `core/controlplane/scheduler/session_token.go` (`SessionTokenIssuer.persistIssue`); `core/controlplane/scheduler/handshake_challenge.go` (`RedisHandshakeChallengeStore.Consume`) | `TestRetentionProofDIC45` |
| DI-C46 | retention cleanup | `core/controlplane/gateway/mcp_approvals.go` (`MCPApprovalStore.EnqueueMCPApproval`); `core/controlplane/gateway/mcp_approvals.go` (`MCPApprovalStore.pruneIndexFor`) | `TestRetentionProofDIC46` |
| DI-C47 | retention cleanup | `core/controlplane/copilot/store_redis.go` (`RedisStore.AppendMessage`) | `TestRetentionProofDIC47`, open finding PR2-08-F6 |
| DI-C48 | retention cleanup | `core/controlplane/gateway/middleware.go` (`redisRateLimiter.Allow`); `core/controlplane/safetykernel/velocity.go` (`velocityChecker.CheckAndRecord`) | `TestRetentionProofDIC48`, `TestRetentionProofDIC48Velocity` |
| DI-C49 | retention cleanup | `core/infra/store/job_store.go` (`RedisJobStore.ReleaseLock`); `core/infra/locks/redis_store.go` (`RedisStore.Release`) | `TestRetentionProofDIC49` |
| DI-C50 | explicit delete only | `core/controlplane/scheduler/saga.go` (`SagaManager.popCompensation`); `core/controlplane/scheduler/saga.go` (`SagaManager.Rollback`) | none: Rollback drains compensation entries; recorded-step sets never expire. |
| DI-C51 | retention cleanup | `core/controlplane/scheduler/delegation_dispatch.go` (`Engine.claimDelegationLineageAuditEmission`) | `TestRetentionProofDIC51` |
| DI-C52 | no cleanup path | none | none: No Cordum deletion path; operators manage these keys |
| DI-C53 | retention cleanup | `core/infra/store/redis_store.go` (`RedisStore.PutResult`); `core/controlplane/scheduler/engine.go` (`Engine.persistWorkerCount`) | `TestRetentionProofDIC53` |
| DI-C54 | retention cleanup | `core/controlplane/gateway/gateway.go` (`deleteSystemDefaultKeyWithRetry`); `core/controlplane/gateway/handlers_packs.go` (`server.removePolicyOverlay`); `core/controlplane/gateway/handlers_packs.go` (`server.restoreConfigOverlay`); `core/controlplane/gateway/handlers_packs.go` (`server.restorePolicyOverlay`); `core/controlplane/gateway/handlers_policy_bundles.go` (`server.handleDeletePolicyBundle`); `core/controlplane/gateway/handlers_policy_bundles.go` (`server.handlePutPolicyBundle`); `core/controlplane/gateway/handlers_policy_bundles.go` (`server.handlePutPolicyOutputRule`); `core/controlplane/gateway/handlers_policy_bundles.go` (`server.handleCapturePolicyBundleSnapshot`); `core/controlplane/gateway/handlers_policy_bundles.go` (`server.capturePolicyBundleSnapshotWithBundles`); `core/controlplane/gateway/handlers_policy_global.go` (`server.handlePutPolicyGlobal`); `core/controlplane/gateway/handlers_pools.go` (`server.deletePoolFromConfig`); `core/controlplane/gateway/handlers_pools.go` (`server.handleRemoveTopicFromPool`); `core/controlplane/gateway/handlers_topics.go` (`server.removeTopicFromPoolConfigOnce`); `core/controlplane/gateway/handlers_velocity.go` (`server.handleDeleteVelocityRule`); `core/controlplane/topicregistry/service.go` (`Service.DeleteMany`); `core/controlplane/topicregistry/service.go` (`Service.DeleteForTenant`) | `TestRetentionProofDIC54` |

## Legal hold

| Entry | Proof |
| --- | --- |
| DI-C01 | `TestLegalHoldProofDIC01` |

DI-C01 is the only inventory entry whose cleanup consults a legal hold.
No other entry consults a hold. The DI-C01 proof must retain the marker through
the same sweep while held, then find no trace after release and another sweep.

## Backups

For `deploy/k8s/production`, data deleted live leaves the newest backup that holds it within 9 days, provided the shipped jobs run successfully on schedule.
Redis RDB backups and NATS snapshots run hourly (`0 * * * *`). The retention job
runs daily (`30 3 * * *`). Its `find -mtime +7` matches ages of at least 8 days:
8 days plus at most 24 hours of cadence gives the 9-day bound. This applies to
RDB, topology, sha256 sidecars and NATS snapshot directories on the backup volume.
`TestRetentionBackupWindow` pins these schedules and cleanup commands; it is
a manifest contract, not evidence of successful backup deletion in a deployment.
Other deployments manage their own persistence and backup copies (DI-C16, DI-C17);
live-key TTL and stream MaxAge do not erase those copies or recipient downloads.

## Cell grammar

- Each inventory id occurs once in the inventory map; the legal-hold table is separate.
- Categories are `retention cleanup`, `explicit delete only`, or `no cleanup path`.
- `no cleanup path` applies exactly when inventory deletion is `none`.
- Retention and legal-hold Proof cells contain comma-separated references: a
  backticked existing `Test...` function (optionally `/subtest`),
  `pending PR2-08-EDGE`, `pending PR2-08-JOBS`, `pending PR2-08-DATA`,
  `pending PR2-08-SERVICES`, or `open finding PR2-08-F<n>`.
- Other Proof cells start `none: ` and give the inventory reason.
- DI-C34 is jointly owned by EDGE and DATA. SERVICES and SERVICES-B fill the
  same SERVICES label. Replace pending cells only with reviewed proof references.
- `TestRetentionProofMapContract` checks ids, categories, references and hold coverage.
