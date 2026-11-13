# Failure and partition behavior

Status: contract document, roadmap M0. It records what Cordum must do with each class of action
when a dependency is unavailable, and what it does today. Current behavior was read and tested at
cordum `ee58be379d3927f3aec0cac13ce5e09cf605a67d`; every current-behavior cell cites a characterization test by name or says
`not measured`. It changes no runtime behavior. Gaps are owned by the rows named in the Gaps table.

"Unavailable" means down, unreachable, timing out, or returning errors. Partitions are covered only
as seen by one component; split-brain behavior belongs to M4-07.

## Action classes

| Class | Meaning |
|---|---|
| low | The policy decision is ALLOW and the action is not approval-gated or protected. |
| approval | The policy decision is REQUIRE_APPROVAL: the action may run only after a recorded approval is consumed. |
| protected | High risk or protected: the policy can deny it, or it is a protected operation as roadmap row M0-02 defines one. |

The Safety Kernel decides the class from policy. While the kernel or the policy source is unavailable,
the control plane cannot tell the classes apart, so the protected-class requirement applies unless a
local, explicit rule classes the action as low risk.

## Required-behavior rules

The rules cite roadmap invariants by id:
- I01: no authority, no protected effect.
- I02: an unsupported restriction means deny.
- I03: approval never overrides a hard prohibition.
- I04: a different effective policy means a different policy identity.
- I06: duplicate delivery does not imply completed processing.
- I07: revocation takes effect within a published bound.
- I08: an unknown outcome is never reported as success.
- I09: missing required evidence is never reported as verified.

- RQ-1 Protected actions fail closed:
  - No protected effect starts while a dependency that supplies its authority, policy, approval, evidence or entitlement is unavailable.
  - The request is held, requeued or rejected with an explicit error.
  - Its outcome is never reported as success.
  - (I01, I08)
- RQ-2 Approval-required actions never run without a recorded, consumable approval:
  - While the approval path is unavailable, the request is held or rejected with an explicit error.
  - An outage never turns into an approval or an allow.
  - (I01, I03)
- RQ-3 Low-risk actions fail closed by default when a dependency that supplies their authority or policy is unavailable:
  - They may continue only under an explicit offline exception that an operator configured, that is scoped (per tenant or per mode), and that is visible in logs or metrics.
  - An outage of a dependency that low-risk actions do not use does not change their decision.
- RQ-4 Redis and NATS: no action is reported as admitted, dispatched or completed unless its state was recorded:
  - On an outage, the request is rejected or retried, never dispatched without recorded state.
  - A duplicate delivery never implies completion.
  - (I06, I08)
- RQ-5 Audit sink: an approval-required or protected effect needs its evidence recorded durably first:
  - Missing evidence is never reported as verified.
  - A low-risk action may continue with its evidence marked pending.
  - (I09)
- RQ-6 Policy source: enforcement continues on the last verified policy:
  - An unavailable or unverifiable policy never yields an unrestricted allow.
  - Past a published staleness bound, approval-required and protected actions fail closed.
  - (I02, I04, I07)
- RQ-7 License: a missing, invalid or expired license never removes or weakens an enforcement control (deny, approval, policy checks):
  - It may switch off entitlement-gated features in an explicit, documented state.

## Behavior table

How to read a row:
- There is one row per dependency and class.
- Required cites the rules that apply.
- Current describes the entry points that behave differently: gateway submit, scheduler dispatch, MCP tools/call, the Edge hook and workflow steps. Each statement is tagged [a], [b] and so on.
- Evidence lists the characterization tests for each tag, as `TestName` or `TestName/subtest`, for example: [a] `TestName`; [b] not measured.
- A statement that something did not happen (rejected, held, requeued, denied, nothing published or run) cites a test that asserts the effect is absent. A test that checks only an error or a status code supports only that code.
- Gap is `-` when current meets required. Otherwise it is a gap id from the Gaps table.

The characterization tests are hermetic Go tests that use fakes, miniredis and in-memory buses. They
pin current behavior and do not change it. For Edge, the enforcement point is the decision the hook
returns to the agent runtime; whether the runtime honors that decision is outside these tests.

| Dependency | Class | Required | Current | Evidence | Gap |
|---|---|---|---|---|---|
| redis | low | RQ-3, RQ-4 | [a] Scheduler: a failed DISPATCHED-state write leaves SCHEDULED and publishes nothing; a retry after recovery publishes once. [b] Gateway limiter returns false on a Redis error (limiter-only probe). [c] Scheduler: in open and closed input modes, an ALLOW decision whose decision-record write fails returns a retryable error with no publish and no DISPATCHED/RUNNING state. [d] Other gateway, MCP, workflow, configuration and authorization outage paths: not measured. | [a] `TestDuplicateDispatchOnDispatchedStateFailure`; [b] `TestRedisRateLimiterFallback`; [c] `TestDecisionStoreWriteFailureRequeuesInBothInputModes`; [d] not measured | G01 |
| redis | approval | RQ-2, RQ-4 | [a] Scheduler: an approval-fence read error returns a retryable error with no publish or DISPATCHED/RUNNING state. [b] Workflow approval step: a context-store error leaves the step FAILED with no bus publish. [c] Gateway approve and reject: a store read error for the job state, tenant, stored request, submitter, workflow run or (approve only) safety decision returns HTTP 503; the job stays in APPROVAL, no approval is recorded and nothing is published. A job with no recorded submitter, or whose workflow run does not exist, is handled as before. [d] Scheduler: in open and closed input modes, a REQUIRE_APPROVAL decision whose decision-record write fails returns a retryable error with no publish and no DISPATCHED/RUNNING state. [e] Submit and other store-error paths: not measured. | [a] `TestProcessJob_HashFenceReadFailureDoesNotFailOpen`; [b] `TestWorkflowApprovalStepFailsWhenContextStoreUnavailable`; [c] `TestApproveRejectMissingRecordsKeepEarlierHandling`, `TestApproveRejectStoreReadErrorsReturn503`; [d] `TestDecisionStoreWriteFailureRequeuesInBothInputModes`; [e] not measured | G01 |
| redis | protected | RQ-1, RQ-4 | [a] Scheduler: when persisting DENIED fails, it returns an error and publishes nothing, including no DLQ message. [b] Scheduler: in open and closed input modes, a DENY decision whose decision-record write fails returns a retryable error with no publish and no DISPATCHED/RUNNING state. [c] Other entry points and full-outage behavior: not measured. | [a] `TestProcessJob_SetStateDeniedFailure_ReturnsRetryable`; [b] `TestDecisionStoreWriteFailureRequeuesInBothInputModes`; [c] not measured | G01 |
| nats | low | RQ-3, RQ-4 | [a] Gateway submit: a publish error returns 5xx and the context blob is absent afterward; this does not prove the eventual action outcome. [b] Scheduler: two failed publish attempts return retryable errors and record four scheduling attempts. [c] Delivery, acknowledgement, recovery and partition guarantees: not measured. | [a] `TestSubmitJobHTTP_PublishFails_GCsOrphanedContext`; [b] `TestProcessJobPublishFailureScheduledReplayIncrementsAttempts`; [c] not measured | G02 |
| nats | approval | RQ-2, RQ-4 | not measured | not measured | G02 |
| nats | protected | RQ-1, RQ-4 | not measured | not measured | G02 |
| safety-kernel | low | RQ-3 | [a] Scheduler default-closed probe: retryable error, no publish, no DISPATCHED/RUNNING/DENIED state. [b] Gateway closed submit: HTTP 403 with DENIED state and only a DLQ publish; gRPC PermissionDenied with no publish. [c] Gateway POLICY_CHECK_FAIL_MODE=open probe publishes a submit. [d] Scheduler open input mode: a job whose topic no scheduler.input_fail_open_scope rule names is requeued with no publish and no DISPATCHED/RUNNING state; a job whose topic a rule names is admitted, and its bypass audit event and fail-open metric carry the rule. A kernel decision that arrives at the check deadline is kept. [e] Edge enforce known-safe request returns DENY/deny, degraded/safety_unavailable, with a persisted DENY event. [f] MCP, output checks and other paths: not measured. | [a] `TestFailureBehaviorKernelUnavailableClosedDispatchesNothing`; [b] `TestHandleSubmitJobHTTP_PolicyFailClosed`, `TestSubmitJobGRPC_PolicyFailClosed`; [c] `TestHandleSubmitJobHTTP_PolicyFailOpen`; [d] `TestFailOpenScopeRequeuesUnlistedTopic`, `TestFailOpenScopeTenantRulesReplaceSystem`, `TestFailOpenEmitsAuditEvent`, `TestFailOpenScopeRuleLabelsPromMetric`, `TestSafetyUnavailable_FailOpen_Metric`, `TestFailOpenLateKernelAnswerKeepsDecision`; [e] `TestFailureBehaviorEdgeEnforceKernelUnavailableDeniesKnownSafe`; [f] not measured | G03 |
| safety-kernel | approval | RQ-2 | [a] Scheduler default-closed probe: retryable error, no publish, no DISPATCHED/RUNNING/DENIED state. [b] Gateway closed submit: HTTP 403 with DENIED state and only a DLQ publish; gRPC PermissionDenied with no publish. [c] Gateway POLICY_CHECK_FAIL_MODE=open probe publishes a submit. [d] Scheduler open input mode: a job whose topic no scheduler.input_fail_open_scope rule names is requeued with no publish and no DISPATCHED/RUNNING state; a job whose topic a rule names is admitted, and its bypass audit event and fail-open metric carry the rule. A kernel decision that arrives at the check deadline is kept. [e] Edge enterprise-strict low-risk and enforce high-risk probes return DENY/deny with degraded/safety_unavailable; observe returns ALLOW/allow with degraded. [f] Class-specific approvals, MCP, output checks and other paths: not measured. | [a] `TestFailureBehaviorKernelUnavailableClosedDispatchesNothing`; [b] `TestHandleSubmitJobHTTP_PolicyFailClosed`, `TestSubmitJobGRPC_PolicyFailClosed`; [c] `TestHandleSubmitJobHTTP_PolicyFailOpen`; [d] `TestFailOpenScopeRequeuesUnlistedTopic`, `TestFailOpenScopeTenantRulesReplaceSystem`, `TestFailOpenEmitsAuditEvent`, `TestFailOpenScopeRuleLabelsPromMetric`, `TestSafetyUnavailable_FailOpen_Metric`, `TestFailOpenLateKernelAnswerKeepsDecision`; [e] `TestGatewayEdgeEvaluateSafetyUnavailableByPolicyMode`; [f] not measured | G03 |
| safety-kernel | protected | RQ-1 | [a] Scheduler default-closed probe: retryable error, no publish, no DISPATCHED/RUNNING/DENIED state. [b] Gateway closed submit: HTTP 403 with DENIED state and only a DLQ publish; gRPC PermissionDenied with no publish. [c] Gateway POLICY_CHECK_FAIL_MODE=open probe publishes a submit. [d] Scheduler open input mode: a job whose topic no scheduler.input_fail_open_scope rule names is requeued with no publish and no DISPATCHED/RUNNING state; a job whose topic a rule names is admitted, and its bypass audit event and fail-open metric carry the rule. A kernel decision that arrives at the check deadline is kept. [e] Edge enterprise-strict low-risk and enforce high-risk probes return DENY/deny with degraded/safety_unavailable; observe returns ALLOW/allow with degraded. [f] Class-specific approvals, MCP, output checks and other paths: not measured. | [a] `TestFailureBehaviorKernelUnavailableClosedDispatchesNothing`; [b] `TestHandleSubmitJobHTTP_PolicyFailClosed`, `TestSubmitJobGRPC_PolicyFailClosed`; [c] `TestHandleSubmitJobHTTP_PolicyFailOpen`; [d] `TestFailOpenScopeRequeuesUnlistedTopic`, `TestFailOpenScopeTenantRulesReplaceSystem`, `TestFailOpenEmitsAuditEvent`, `TestFailOpenScopeRuleLabelsPromMetric`, `TestSafetyUnavailable_FailOpen_Metric`, `TestFailOpenLateKernelAnswerKeepsDecision`; [e] `TestGatewayEdgeEvaluateSafetyUnavailableByPolicyMode`; [f] not measured | G03 |
| gateway-from-edge | low | RQ-3 | [a] Hook enforce safe-command timeout probe emits permissionDecision deny. [b] Hook observe unavailable probe emits empty output and a degraded warning. [c] Enterprise-strict agentd unavailable/policy-unavailable probes return degraded DENY. [d] Full hook-to-Gateway outage and offline exceptions: not measured. | [a] `TestRunEnforceModeDeniesPreToolUseOnAgentdTimeoutWithoutFailClosed`; [b] `TestRunObserveModeAllowsNoopWhenAgentdUnavailable`; [c] `TestApplyFailModeMatrixCoversObserveEnforceEnterpriseStrict`; [d] not measured | G04 |
| gateway-from-edge | approval | RQ-2 | [a] Enterprise-strict inline approval wait returns DENY on a Gateway error or timeout. [b] Full hook-to-Gateway outage, approval consumption and other modes: not measured. | [a] `TestAgentdDecisionFromEvaluateInlineWaitRejectedTimeoutAndErrorDeny`; [b] not measured | G04 |
| gateway-from-edge | protected | RQ-1 | [a] Enforce hook degraded-response probe emits permissionDecision deny for Edit. [b] Enterprise-strict hook malformed-agentd-response probe emits permissionDecision deny. [c] Full hook-to-Gateway outage and other protected operations: not measured. | [a] `TestRunEnforceDeniesDegradedPreToolUse`; [b] `TestRunEnterpriseStrictDeniesMalformedAgentdResponse`; [c] not measured | G04 |
| approval-store | low | RQ-3 | [a] MCP registry ungated tool executes once without consulting an unavailable approval gate. [b] Gateway, Edge and other low-risk paths: not measured. | [a] `TestFailureBehaviorApprovalStoreOutageLeavesUngatedToolsUnaffected`; [b] not measured | G05 |
| approval-store | approval | RQ-2 | [a] MCP HTTP approval-gated tool returns sanitized -32096 and invokes no handler during a store outage; the recovery control invokes it once. [b] Scheduler approval-gate persistence error returns retryable with no publish. [c] Other approve, Edge and workflow paths: not measured. | [a] `TestMCPHTTP_ApprovalStoreOutageReturnsSanitized32096`; [b] `TestProcessJobApprovalGateStoreFailureReturnsRetryableError`; [c] not measured | G05 |
| approval-store | protected | RQ-1, RQ-2 | [a] MCP HTTP approval-gated tool returns sanitized -32096 and invokes no handler during a store outage; the recovery control invokes it once. [b] Scheduler approval-gate persistence error returns retryable with no publish. [c] Other approve, Edge and workflow paths: not measured. | [a] `TestMCPHTTP_ApprovalStoreOutageReturnsSanitized32096`; [b] `TestProcessJobApprovalGateStoreFailureReturnsRetryableError`; [c] not measured | G05 |
| audit-sink | low | RQ-5 | [a] Gateway Edge evaluate with an event-append error returns 5xx and emits no stream event. [b] agentd event-batch failure returns 503 and persists zero events. [c] Pending evidence, SIEM delivery and verification completeness: not measured. | [a] `TestGatewayEdgeEvaluateDoesNotStreamWhenPersistenceFails`; [b] `TestHandleHookEventsAreAtomicAcrossBatchFailure`; [c] not measured | G06 |
| audit-sink | approval | RQ-2, RQ-5 | [a] Scheduler approval-gate persistence-error probe returns retryable with no publish. [b] Durable evidence before effects at other entry points, exports and verification completeness: not measured. | [a] `TestProcessJobApprovalGateStoreFailureReturnsRetryableError`; [b] not measured | G07 |
| audit-sink | protected | RQ-1, RQ-5 | [a] Scheduler approval-gate persistence-error probe returns retryable with no publish. [b] Durable evidence before effects at other entry points, exports and verification completeness: not measured. | [a] `TestProcessJobApprovalGateStoreFailureReturnsRetryableError`; [b] not measured | G07 |
| policy-source | low | RQ-3, RQ-6 | [a] Kernel reload with a corrupt file retains the old policy and snapshot in memory. [b] Kernel with no policy returns decision DENY (decision-only probe). [c] Last-policy verification, staleness bounds, other source failures and caller effects: not measured. | [a] `TestWatchPolicyReloadFailureKeepsOldPolicy`; [b] `TestEvaluateNilPolicyDeniesFailClosed`; [c] not measured | G08 |
| policy-source | approval | RQ-2, RQ-6 | [a] Kernel reload with a corrupt file retains the old policy and snapshot in memory. [b] Kernel with no policy returns decision DENY (decision-only probe). [c] Last-policy verification, staleness bounds, other source failures and caller effects: not measured. | [a] `TestWatchPolicyReloadFailureKeepsOldPolicy`; [b] `TestEvaluateNilPolicyDeniesFailClosed`; [c] not measured | G08 |
| policy-source | protected | RQ-1, RQ-6 | [a] Kernel reload with a corrupt file retains the old policy and snapshot in memory. [b] Kernel with no policy returns decision DENY (decision-only probe). [c] Last-policy verification, staleness bounds, other source failures and caller effects: not measured. | [a] `TestWatchPolicyReloadFailureKeepsOldPolicy`; [b] `TestEvaluateNilPolicyDeniesFailClosed`; [c] not measured | G08 |
| license | low | RQ-7 | [a] Resolver invalid-license probe returns community plan with fallback status. [b] Gateway HTTP grace-state submit probe returns 503 and publishes nothing. [c] Enforcement-control preservation, expiry while running, approvals, MCP, Edge and workflow behavior: not measured. | [a] `TestEntitlementResolverInvalidLicenseFallsBackToCommunity`; [b] `TestBreakGlass_GraceStateJobSubmitReturns503`; [c] not measured | G09 |
| license | approval | RQ-2, RQ-7 | [a] Resolver invalid-license probe returns community plan with fallback status. [b] Gateway HTTP grace-state submit probe returns 503 and publishes nothing. [c] Enforcement-control preservation, expiry while running, approvals, MCP, Edge and workflow behavior: not measured. | [a] `TestEntitlementResolverInvalidLicenseFallsBackToCommunity`; [b] `TestBreakGlass_GraceStateJobSubmitReturns503`; [c] not measured | G09 |
| license | protected | RQ-1, RQ-7 | [a] Resolver invalid-license probe returns community plan with fallback status. [b] Gateway HTTP grace-state submit probe returns 503 and publishes nothing. [c] Enforcement-control preservation, expiry while running, approvals, MCP, Edge and workflow behavior: not measured. | [a] `TestEntitlementResolverInvalidLicenseFallsBackToCommunity`; [b] `TestBreakGlass_GraceStateJobSubmitReturns503`; [c] not measured | G09 |

### Scope and operator modes

The class is unknown while the kernel or policy source is unavailable: the common kernel-outage
probes above do not establish class-specific authorization. New tests on this branch exercise the
unchanged runtime at the source revision above, except the redis/approval [c] statement, which
describes the Gateway approve and reject handlers as changed by row PR1-17 on this branch, and the
PR1-16 tests cited in the redis and safety-kernel rows, which exercise the scoped input fail-open
behavior added on this branch. Isolated store errors are not full Redis outages, and a decision or
status code alone does not prove that a downstream action did not run.

Edge observe is the default for both the hook and newly created sessions. It is an evidence-recording,
non-enforcing mode during outages and otherwise, not a guarantee that evidence survives an outage.
The Edge enforcement statements above describe enforce or enterprise-strict; observe probes are
explicitly labeled. Actual agent-runtime compliance with a hook decision is not measured here.

The [MCP server guide](../mcp-server.md#policy-gate--approval-hold-edge-102--edge-103)
documents the optional action-gate pipeline. It does not establish kernel-outage behavior;
MCP remains not measured in the safety-kernel rows.

The [input fail-mode guide](../safety-kernel.md#10-input-policy-fail-mode) documents the open
operator mode and its risk. In the scheduler binary, the config-service resolver selects
`scheduler.input_fail_mode` and `scheduler.output_fail_mode`; unset modes are closed.
The production identity profile forces closed. `POLICY_CHECK_FAIL_MODE` selects the Gateway
submit pre-check, not the scheduler's effective mode when that resolver is wired.
`scheduler.input_fail_open_scope` is a system- or tenant-level list of named rules with exact
topics; a tenant list replaces the system list, and an empty list opts out. Malformed entries,
wildcard topics and approval-gate subjects are ignored. Open input mode admits only listed topics
while the kernel is unavailable. The operator's rule, not the kernel, classes a listed topic as low risk.
The Gateway pre-check decides acceptance only; the scheduler check decides dispatch.
The table's engine probes do not qualify every production wiring or output-check path.
Open-mode results are not evidence that RQ-1 or RQ-2 is satisfied.

Operator procedures: [Production guide, section 11: Incident Runbooks](../production.md#11-incident-runbooks).

## Gaps

A gap is a cell whose current behavior differs from the required behavior, or is not measured.
Owner is a finding id (R01-R15) or the board row key that will close or measure it.

| Gap | Cells | Owner |
|---|---|---|
| G01 | redis/low, redis/approval, redis/protected | M4-01, M4-03, M4-05, M4-07, PR1-13, PR1-15, R10 |
| G02 | nats/low, nats/approval, nats/protected | M4-01, M4-03, M4-06, M4-07, PR1-13, R10 |
| G03 | safety-kernel/low, safety-kernel/approval, safety-kernel/protected | PR1-14, M1-R14A, R15 |
| G04 | gateway-from-edge/low, gateway-from-edge/approval, gateway-from-edge/protected | PR1-14, M1-R14A |
| G05 | approval-store/low, approval-store/approval, approval-store/protected | R15 |
| G06 | audit-sink/low | M4-12 |
| G07 | audit-sink/approval, audit-sink/protected | R12, R11, M4-12 |
| G08 | policy-source/low, policy-source/approval, policy-source/protected | PR1-13, R07 |
| G09 | license/low, license/approval, license/protected | PR2-06 |

## Verification

Run from the repository root. Every test named in the Evidence column appears in one of these commands.

```bash
go test ./core/controlplane/scheduler -run '^(TestDecisionStoreWriteFailureRequeuesInBothInputModes|TestDuplicateDispatchOnDispatchedStateFailure|TestFailOpenEmitsAuditEvent|TestFailOpenLateKernelAnswerKeepsDecision|TestFailOpenScopeRequeuesUnlistedTopic|TestFailOpenScopeRuleLabelsPromMetric|TestFailOpenScopeTenantRulesReplaceSystem|TestFailureBehaviorKernelUnavailableClosedDispatchesNothing|TestProcessJobApprovalGateStoreFailureReturnsRetryableError|TestProcessJobPublishFailureScheduledReplayIncrementsAttempts|TestProcessJob_HashFenceReadFailureDoesNotFailOpen|TestProcessJob_SetStateDeniedFailure_ReturnsRetryable|TestSafetyUnavailable_FailOpen_Metric)$' -count=1
go test ./core/controlplane/gateway -run '^(TestApproveRejectMissingRecordsKeepEarlierHandling|TestApproveRejectStoreReadErrorsReturn503|TestBreakGlass_GraceStateJobSubmitReturns503|TestFailureBehaviorEdgeEnforceKernelUnavailableDeniesKnownSafe|TestGatewayEdgeEvaluateDoesNotStreamWhenPersistenceFails|TestGatewayEdgeEvaluateSafetyUnavailableByPolicyMode|TestHandleSubmitJobHTTP_PolicyFailClosed|TestHandleSubmitJobHTTP_PolicyFailOpen|TestMCPHTTP_ApprovalStoreOutageReturnsSanitized32096|TestRedisRateLimiterFallback|TestSubmitJobGRPC_PolicyFailClosed|TestSubmitJobHTTP_PublishFails_GCsOrphanedContext)$' -count=1
go test ./core/workflow -run '^(TestWorkflowApprovalStepFailsWhenContextStoreUnavailable)$' -count=1
go test ./core/edge/claude -run '^(TestRunEnforceDeniesDegradedPreToolUse|TestRunEnforceModeDeniesPreToolUseOnAgentdTimeoutWithoutFailClosed|TestRunEnterpriseStrictDeniesMalformedAgentdResponse|TestRunObserveModeAllowsNoopWhenAgentdUnavailable)$' -count=1
go test ./core/edge/agentd -run '^(TestAgentdDecisionFromEvaluateInlineWaitRejectedTimeoutAndErrorDeny|TestApplyFailModeMatrixCoversObserveEnforceEnterpriseStrict|TestHandleHookEventsAreAtomicAcrossBatchFailure)$' -count=1
go test ./core/mcp -run '^(TestFailureBehaviorApprovalStoreOutageLeavesUngatedToolsUnaffected)$' -count=1
go test ./core/controlplane/safetykernel -run '^(TestEvaluateNilPolicyDeniesFailClosed|TestWatchPolicyReloadFailureKeepsOldPolicy)$' -count=1
go test ./core/licensing -run '^(TestEntitlementResolverInvalidLicenseFallsBackToCommunity)$' -count=1
```
