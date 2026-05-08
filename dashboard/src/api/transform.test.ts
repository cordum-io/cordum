import { describe, expect, it, vi } from "vitest";
import {
  computeUrgencyLevel,
  deriveApprovalActionability,
  deriveApprovalStatus,
  mapApprovalContext,
  mapApprovalItem,
  mapDelegationListResponse,
  mapDelegationView,
  mapDLQEntry,
  mapAgentActionEvent,
  mapAgentActionEventPage,
  mapAgentExecution,
  mapEdgeApproval,
  mapEdgeApprovalPage,
  mapEdgeErrorEnvelope,
  mapEdgeEventStreamEnvelope,
  mapEdgeSession,
  mapEdgeSessionExportBundle,
  mapEdgeSessionPage,
  mapEdgeStreamPayload,
  mapGovernanceDecision,
  mapHeartbeatToWorker,
  mapJobDetail,
  mapJobRecord,
  mapOutputSafetyRecord,
  mapPolicyBundleDetail,
  mapWorkflow,
  mapWorkflowRun,
  mapWorkflowRunStep,
  mapWorkflowStep,
  microsToISO,
  normalizeGovernanceVerdict,
  normalizeDecisionType,
  normalizeJobStatus,
  normalizeOutputDecision,
} from "./transform";

describe("api/transform mappings", () => {
  it("maps job records with normalized status, output safety fallback, and deduped capabilities", () => {
    const job = mapJobRecord({
      id: "job-1",
      topic: "job.default",
      state: "RUNNING",
      updated_at: 1_700_000_000_000_000,
      capability: "cap.read",
      requires: ["cap.write", "cap.read"],
      output_decision: "DENY",
    });

    expect(job.status).toBe("running");
    expect(job.capabilities).toEqual(["cap.read", "cap.write"]);
    expect(job.output_safety?.decision).toBe("QUARANTINE");
    expect(job.updatedAt).toContain("T");
  });

  it("maps delegation views with defensive null-coalescing", () => {
    const delegation = mapDelegationView({
      jti: "dlg-1",
      issuer: "agent-a",
      subject: "agent-a",
      audience: "agent-b",
      allowed_actions: ["read"],
      allowed_topics: undefined,
      chain: [
        {
          agent_id: "agent-a",
          issued_at: "2026-04-21T00:00:00Z",
          expires_at: "2026-04-21T01:00:00Z",
          jti: "dlg-1",
          issued_by: "cordum",
        },
      ],
      chain_depth: 1,
      issued_at: "2026-04-21T00:00:00Z",
      expires_at: "2026-04-21T01:00:00Z",
      revoked: undefined,
    });

    expect(delegation.allowedActions).toEqual(["read"]);
    expect(delegation.allowedTopics).toEqual([]);
    expect(delegation.chain[0]).toEqual({
      agentId: "agent-a",
      issuedAt: "2026-04-21T00:00:00Z",
      expiresAt: "2026-04-21T01:00:00Z",
      jti: "dlg-1",
      parentJti: undefined,
      issuedBy: "cordum",
    });
    expect(delegation.revoked).toBe(false);
  });

  it("maps delegation list responses and next cursor safely", () => {
    const response = mapDelegationListResponse({
      items: [{ jti: "dlg-1", issuer: "agent-a", subject: "agent-a", audience: "agent-b" }],
      next_cursor: "cur-2",
    });

    expect(response.items).toHaveLength(1);
    expect(response.items[0]?.allowedActions).toEqual([]);
    expect(response.nextCursor).toBe("cur-2");
  });

  it("maps job detail fields on top of base job mapping", () => {
    const detail = mapJobDetail({
      id: "job-2",
      topic: "job.detail",
      state: "SUCCEEDED",
      context_ptr: "redis://ctx:job-2",
      result_ptr: "redis://res:job-2",
      workflow_id: "wf-1",
      run_id: "run-1",
      labels: { env: "prod" },
      approval_required: true,
      approval_ref: "approval-1",
    });

    expect(detail.workflowId).toBe("wf-1");
    expect(detail.workflowRunId).toBe("run-1");
    expect(detail.contextPtr).toBe("redis://ctx:job-2");
    expect(detail.resultPtr).toBe("redis://res:job-2");
    expect(detail.approvalRequired).toBe(true);
    expect(detail.approvalRef).toBe("approval-1");
    expect(detail.metadata.env).toBe("prod");
  });

  it("maps workflow definitions and run records with normalized step payloads", () => {
    const workflow = mapWorkflow({
      id: "wf-1",
      name: "Workflow One",
      timeout_sec: 120,
      steps: {
        stepA: {
          type: "job",
          topic: "job.default",
          timeout_sec: 30,
          retry: { max_retries: 2 },
          meta: { pack_id: "pack-hello" },
        },
      },
    });
    expect(workflow.steps).toHaveLength(1);
    expect(workflow.steps[0].id).toBe("stepA");
    expect(workflow.steps[0].type).toBe("pack-action");
    expect(workflow.steps[0].retry?.max_retries).toBe(2);

    const run = mapWorkflowRun({
      id: "run-1",
      workflow_id: "wf-1",
      status: "running",
      started_at: "2026-01-01T00:00:00.000Z",
      completed_at: "2026-01-01T00:00:05.000Z",
      steps: {
        stepA: {
          step_id: "stepA",
          status: "SUCCEEDED",
          output: { ok: true },
        },
      },
    });
    expect(run.workflowId).toBe("wf-1");
    expect(run.steps[0].id).toBe("stepA");
    expect(run.duration).toBe(5000);
  });

  it("derives approval statuses for decision/state combinations", () => {
    const cases: Array<{
      jobState?: string;
      decision?: string;
      expected: string;
    }> = [
      { decision: "approved", expected: "approved" },
      { decision: "reject", expected: "rejected" },
      { jobState: "DENIED", expected: "rejected" },
      { jobState: "OUTPUT_QUARANTINED", expected: "approved" },
      { jobState: "TIMEOUT", expected: "expired" },
      { jobState: "APPROVAL_REQUIRED", expected: "pending" },
      { jobState: "SUCCEEDED", expected: "approved" },
      { jobState: "UNKNOWN", expected: "pending" },
    ];

    for (const testCase of cases) {
      expect(
        deriveApprovalStatus(testCase.jobState, testCase.decision),
      ).toBe(testCase.expected);
    }
  });

  it("derives approval actionability from explicit fields or lifecycle fallback", () => {
    expect(deriveApprovalActionability("actionable", "pending")).toBe("actionable");
    expect(deriveApprovalActionability(undefined, "pending")).toBe("actionable");
    expect(deriveApprovalActionability(undefined, "approved")).toBe("resolved");
    expect(deriveApprovalActionability(undefined, "invalidated")).toBe("invalidated");
    expect(deriveApprovalActionability(undefined, "repaired")).toBe("repaired");
  });

  it("maps approval items and handles missing job payload safely", () => {
    expect(mapApprovalItem({})).toBeNull();

    const approval = mapApprovalItem({
      approval_ref: "approval-2",
      policy_reason: "Requires manual review",
      job: {
        id: "job-approval",
        topic: "job.review",
        state: "APPROVAL_REQUIRED",
        updated_at: Date.now() * 1000,
      },
    });

    expect(approval).not.toBeNull();
    expect(approval?.status).toBe("pending");
    expect(approval?.actionability).toBe("actionable");
    expect(approval?.humanSummary).toContain('Job on "job.review"');
    expect(approval?.urgencyLevel).toBeDefined();
  });

  it("maps approval context defensively without leaking malformed backend fields", () => {
    const context = mapApprovalContext({
      approval: { id: "approval-1" },
      blast_radius: {
        systems: ["payments", 42],
        namespaces: ["prod"],
        resources: "not-an-array",
        scope_description: "production payments",
      },
      prior_approvals: [
        {
          job_id: "job-1",
          topic: "job.payments",
          tenant: "default",
          decision: "approve",
          resolved_by: "admin",
          resolved_at: "123",
          was_approved: true,
        },
        "malformed",
      ],
      rollback_hint: "rollback available",
      policy_snapshot_summary: {
        rule_count: "3",
        matched_rule: {
          id: "rule-1",
          description: "manual review",
          decision: "require_human",
          constraints_summary: "amount > threshold",
        },
        policy_version: "v7",
      },
      constraints: { max_runtime_ms: 1000 },
      time_remaining_ms: "5000",
    });

    expect(context.approval).toEqual({ id: "approval-1" });
    expect(context.blastRadius.systems).toEqual(["payments"]);
    expect(context.blastRadius.resources).toEqual([]);
    expect(context.priorApprovals).toHaveLength(1);
    expect(context.priorApprovals[0]).toMatchObject({
      jobId: "job-1",
      wasApproved: true,
    });
    expect(context.policySnapshotSummary.ruleCount).toBe(3);
    expect(context.policySnapshotSummary.matchedRule.id).toBe("rule-1");
    expect(context.constraints).toEqual({ max_runtime_ms: 1000 });
    expect(context.timeRemainingMs).toBe(5000);
  });

  it("drops malformed prior approvals and preserves unknown time remaining", () => {
    const context = mapApprovalContext({
      prior_approvals: ["malformed", null, { job_id: "job-valid", was_approved: false }],
      time_remaining_ms: "not-a-number",
    });

    expect(context.priorApprovals).toEqual([
      expect.objectContaining({ jobId: "job-valid", wasApproved: false }),
    ]);
    expect(context.timeRemainingMs).toBeNull();
  });

  it("maps malformed approval context to safe defaults", () => {
    expect(mapApprovalContext("not-an-object")).toMatchObject({
      approval: {},
      blastRadius: { systems: [], namespaces: [], resources: [], scopeDescription: "" },
      priorApprovals: [],
      rollbackHint: "",
      timeRemainingMs: null,
      constraints: null,
    });
  });

  it("maps decision-first approval summaries into stable frontend fields", () => {
    const approval = mapApprovalItem({
      approval_ref: "approval-rich",
      policy_reason: "fallback policy reason",
      workflow_id: "wf-7",
      workflow_name: "Expense Approval",
      workflow_run_id: "run-7",
      workflow_step_id: "approve-budget",
      step_name: "Budget Review",
      context_ptr: "redis://ctx:job-7",
      job_input: {
        decision: {
          vendor: "Acme Travel",
        },
      },
      decision_summary: {
        source: "workflow_payload",
        completeness: "rich",
        context_status: "available",
        title: "Approve 1250 USD request with Acme Travel",
        subject: "Approve 1250 USD request with Acme Travel",
        why: "manager threshold exceeded",
        next_effect: "Approve to continue Budget Review.",
        amount: 1250,
        currency: "USD",
        vendor: "Acme Travel",
        item_count: 2,
        items_preview: ["flight", "hotel"],
        escalation_reason: "manager threshold exceeded",
        missing_fields: [],
      },
      job: {
        id: "job-7",
        topic: "job.workflow.approval",
        state: "APPROVAL_REQUIRED",
        updated_at: Date.now() * 1000,
      },
    });

    expect(approval).not.toBeNull();
    expect(approval?.decisionSummary?.source).toBe("workflow_payload");
    expect(approval?.decisionSummary?.vendor).toBe("Acme Travel");
    expect(approval?.decisionSummary?.amount).toBe(1250);
    expect(approval?.decisionSummary?.itemsPreview).toEqual(["flight", "hotel"]);
    expect(approval?.reason).toBe("manager threshold exceeded");
    expect(approval?.humanSummary).toBe(
      "Approve 1250 USD request with Acme Travel",
    );
    expect(approval?.workflowContext).toEqual({
      workflowId: "wf-7",
      workflowName: "Expense Approval",
      runId: "run-7",
      stepId: "approve-budget",
      stepIndex: undefined,
      stepName: "Budget Review",
      totalSteps: undefined,
    });
    expect(approval?.contextPtr).toBe("redis://ctx:job-7");
    expect(approval?.jobInput).toEqual({
      decision: {
        vendor: "Acme Travel",
      },
    });
  });

  it("falls back to legacy approval fields when decision summaries are absent or invalid", () => {
    const approval = mapApprovalItem({
      approval_ref: "approval-legacy",
      policy_reason: "Requires manual review",
      decision_summary: {
        source: "policy_only",
        completeness: "minimal",
        context_status: "absent",
        title: "   ",
      },
      job: {
        id: "job-legacy",
        topic: "job.review",
        state: "APPROVAL_REQUIRED",
        updated_at: Date.now() * 1000,
      },
    });

    expect(approval).not.toBeNull();
    expect(approval?.decisionSummary).toBeUndefined();
    expect(approval?.reason).toBe("Requires manual review");
    expect(approval?.humanSummary).toContain('Job on "job.review"');
  });

  it("maps degraded workflow approvals with missing context markers into stable frontend fields", () => {
    const approval = mapApprovalItem({
      approval_ref: "approval-missing-context",
      policy_reason: "manager review required",
      workflow_id: "wf-9",
      workflow_name: "Expense Approval",
      workflow_run_id: "run-9",
      workflow_step_id: "manager-approval",
      step_name: "Manager Approval",
      context_ptr: "redis://ctx:job-9",
      decision_summary: {
        source: "workflow_payload",
        completeness: "partial",
        context_status: "missing",
        title: "Approve manager-approval",
        why: "manager review required",
        missing_fields: ["approval_context", "business_context"],
      },
      job: {
        id: "job-9",
        topic: "job.workflow.approval",
        state: "APPROVAL_REQUIRED",
        updated_at: Date.now() * 1000,
      },
    });

    expect(approval).not.toBeNull();
    expect(approval?.status).toBe("pending");
    expect(approval?.decisionSummary?.source).toBe("workflow_payload");
    expect(approval?.decisionSummary?.contextStatus).toBe("missing");
    expect(approval?.decisionSummary?.completeness).toBe("partial");
    expect(approval?.decisionSummary?.missingFields).toEqual([
      "approval_context",
      "business_context",
    ]);
    expect(approval?.humanSummary).toBe("Approve manager-approval");
    expect(approval?.reason).toBe("manager review required");
    expect(approval?.contextPtr).toBe("redis://ctx:job-9");
    expect(approval?.jobInput).toBeUndefined();
    expect(approval?.workflowContext?.stepName).toBe("Manager Approval");
  });

  it("maps resolved denied workflow approvals without losing decision-first fields", () => {
    const approval = mapApprovalItem({
      approval_ref: "approval-denied",
      policy_reason: "finance approval required",
      resolved_by: "manager-2",
      resolved_at: 1_709_000_002_000_000,
      resolved_comment: "over budget for this quarter",
      approval_status: "rejected",
      approval_actionability: "resolved",
      approval_revision: 2,
      approval_decision: "reject",
      workflow_id: "wf-10",
      workflow_name: "Expense Approval",
      workflow_run_id: "run-10",
      workflow_step_id: "approve",
      step_name: "Manager Approval",
      context_ptr: "redis://ctx:job-10",
      decision_summary: {
        source: "workflow_payload",
        completeness: "rich",
        context_status: "available",
        title: "Approve 8800 USD request with Contoso Travel",
        why: "budget threshold exceeded",
        amount: 8800,
        currency: "USD",
        vendor: "Contoso Travel",
        escalation_reason: "budget threshold exceeded",
      },
      job_input: {
        decision: {
          vendor: "Contoso Travel",
          amount: 8800,
        },
      },
      job: {
        id: "job-10",
        topic: "job.workflow.approval",
        state: "DENIED",
        updated_at: Date.now() * 1000,
      },
    });

    expect(approval).not.toBeNull();
    expect(approval?.status).toBe("rejected");
    expect(approval?.actionability).toBe("resolved");
    expect(approval?.revision).toBe(2);
    expect(approval?.approvalDecision).toBe("reject");
    expect(approval?.actor).toBe("manager-2");
    expect(approval?.comment).toBe("over budget for this quarter");
    expect(approval?.decisionSummary?.vendor).toBe("Contoso Travel");
    expect(approval?.decisionSummary?.contextStatus).toBe("available");
    expect(approval?.humanSummary).toBe(
      "Approve 8800 USD request with Contoso Travel",
    );
    expect(approval?.jobInput).toEqual({
      decision: {
        vendor: "Contoso Travel",
        amount: 8800,
      },
    });
    expect(approval?.resolvedAt).toContain("T");
  });

  it("maps policy bundle detail content and tolerates malformed YAML", () => {
    const mapped = mapPolicyBundleDetail({
      id: "bundle-1",
      content: `
rules:
  - id: deny-admin
    match: {}
    decision: deny
    reason: block admin flows
`,
      enabled: true,
    });
    expect(mapped.id).toBe("bundle-1");
    expect(mapped.rules[0].id).toBe("deny-admin");
    expect(mapped.rules[0].decision).toBe("deny");

    const malformed = mapPolicyBundleDetail({
      id: "bundle-2",
      content: "rules: [invalid",
    });
    expect(malformed.rules).toEqual([]);
  });

  it("maps DLQ entries and worker heartbeat snapshots", () => {
    const dlq = mapDLQEntry({
      job_id: "job-dlq-1",
      topic: "job.default",
      reason: "worker timeout",
      attempts: 3,
      status: "FAILED",
    });
    expect(dlq.jobId).toBe("job-dlq-1");
    expect(dlq.retryCount).toBe(3);
    expect(dlq.error).toBe("worker timeout");

    expect(mapHeartbeatToWorker({})).toBeNull();
    const worker = mapHeartbeatToWorker({
      worker_id: "worker-1",
      pool: "general",
      labels: { name: "Worker One" },
      active_jobs: 2,
      max_parallel_jobs: 0,
      capabilities: ["code.read"],
    });
    expect(worker).not.toBeNull();
    expect(worker?.name).toBe("Worker One");
    expect(worker?.status).toBe("busy");
    expect(worker?.capacity).toBe(2);
  });

  it("maps governance decisions with optional fields and structured constraints", () => {
    const decision = mapGovernanceDecision({
      job_id: "job-gov-1",
      run_id: "run-gov-1",
      step_id: "step-7",
      topic: "jobs.review",
      matched_rule: "rule-42",
      rule_name: "Escalate risky changes",
      verdict: "ALLOW_WITH_CONSTRAINTS",
      reason: "Needs domain allowlist",
      constraints: {
        maxInvocations: 3,
        allowedDomains: ["cordum.io"],
      },
      approval_status: "pending",
      approval_decision: "approve",
      agent_id: "agent-4",
      policy_version: "2026-04-20",
      timestamp: "2026-04-20T08:30:00.000Z",
    });

    expect(decision).toEqual({
      jobId: "job-gov-1",
      runId: "run-gov-1",
      stepId: "step-7",
      topic: "jobs.review",
      matchedRule: "rule-42",
      ruleName: "Escalate risky changes",
      verdict: "constrain",
      reason: "Needs domain allowlist",
      constraints: {
        maxInvocations: 3,
        allowedDomains: ["cordum.io"],
      },
      approvalStatus: "pending",
      approvalDecision: "approve",
      agentId: "agent-4",
      policyVersion: "2026-04-20",
      timestamp: "2026-04-20T08:30:00.000Z",
    });
  });

  it("maps governance decisions when optional fields are absent", () => {
    const decision = mapGovernanceDecision({
      job_id: "job-gov-2",
      topic: "jobs.review",
      matched_rule: "rule-9",
      verdict: "ALLOW",
      reason: "Allowed",
      agent_id: "agent-9",
      timestamp: "2026-04-20T08:45:00.000Z",
    });

    expect(decision).toEqual({
      jobId: "job-gov-2",
      topic: "jobs.review",
      matchedRule: "rule-9",
      verdict: "allow",
      reason: "Allowed",
      agentId: "agent-9",
      timestamp: "2026-04-20T08:45:00.000Z",
    });
  });
});

// ---------------------------------------------------------------------------
// Contract drift and hardening regression tests
// ---------------------------------------------------------------------------

describe("transform contract hardening", () => {
  describe("normalizeOutputDecision fail-closed (security fix)", () => {
    it("returns QUARANTINE for unknown output decisions instead of ALLOW", () => {
      expect(normalizeOutputDecision("BLOCK")).toBe("QUARANTINE");
      expect(normalizeOutputDecision("HOLD")).toBe("QUARANTINE");
      expect(normalizeOutputDecision("PENDING_REVIEW")).toBe("QUARANTINE");
    });

    it("still returns ALLOW for explicit ALLOW", () => {
      expect(normalizeOutputDecision("ALLOW")).toBe("ALLOW");
      expect(normalizeOutputDecision("allow")).toBe("ALLOW");
    });

    it("returns ALLOW only when value is empty/undefined (no decision made)", () => {
      expect(normalizeOutputDecision(undefined)).toBe("ALLOW");
      expect(normalizeOutputDecision("")).toBe("ALLOW");
    });

    it("maps DENY to QUARANTINE", () => {
      expect(normalizeOutputDecision("DENY")).toBe("QUARANTINE");
    });

    it("maps known decisions correctly", () => {
      expect(normalizeOutputDecision("REDACT")).toBe("REDACT");
      expect(normalizeOutputDecision("QUARANTINE")).toBe("QUARANTINE");
    });
  });

  describe("normalizeJobStatus unknown states", () => {
    it("returns pending for empty/undefined state", () => {
      expect(normalizeJobStatus(undefined)).toBe("pending");
      expect(normalizeJobStatus("")).toBe("pending");
    });

    it("still returns pending for truly unknown states (logged)", () => {
      expect(normalizeJobStatus("PAUSED")).toBe("pending");
      expect(normalizeJobStatus("RETRYING")).toBe("pending");
    });

    it("normalizes all known states", () => {
      expect(normalizeJobStatus("RUNNING")).toBe("running");
      expect(normalizeJobStatus("SUCCEEDED")).toBe("succeeded");
      expect(normalizeJobStatus("FAILED")).toBe("failed");
      expect(normalizeJobStatus("FAILED_RETRYABLE")).toBe("failed");
      expect(normalizeJobStatus("FAILED_FATAL")).toBe("failed");
      expect(normalizeJobStatus("OUTPUT_QUARANTINED")).toBe("output_quarantined");
    });
  });

  describe("computeUrgencyLevel NaN safety", () => {
    it("returns 'fresh' for NaN waitMs (not 'breach')", () => {
      expect(computeUrgencyLevel(NaN)).toBe("fresh");
    });

    it("returns 'fresh' for negative waitMs", () => {
      expect(computeUrgencyLevel(-1000)).toBe("fresh");
    });

    it("returns 'fresh' for Infinity", () => {
      expect(computeUrgencyLevel(Infinity)).toBe("fresh");
    });

    it("correctly classifies valid wait times", () => {
      expect(computeUrgencyLevel(0)).toBe("fresh");
      expect(computeUrgencyLevel(60_000)).toBe("fresh");
      expect(computeUrgencyLevel(5 * 60_000)).toBe("aging");
      expect(computeUrgencyLevel(30 * 60_000)).toBe("critical");
      expect(computeUrgencyLevel(90 * 60_000)).toBe("breach");
    });
  });

  describe("mapWorkflowRunStep status normalization", () => {
    it("normalizes uppercase backend statuses to lowercase", () => {
      const step = mapWorkflowRunStep({ step_id: "s1", status: "SUCCEEDED" }, "fallback");
      expect(step.status).toBe("succeeded");
    });

    it("maps common backend status variants", () => {
      expect(mapWorkflowRunStep({ step_id: "s1", status: "completed" }, "f").status).toBe("succeeded");
      expect(mapWorkflowRunStep({ step_id: "s1", status: "error" }, "f").status).toBe("failed");
      expect(mapWorkflowRunStep({ step_id: "s1", status: "timeout" }, "f").status).toBe("timed_out");
      expect(mapWorkflowRunStep({ step_id: "s1", status: "canceled" }, "f").status).toBe("cancelled");
      expect(mapWorkflowRunStep({ step_id: "s1", status: "queued" }, "f").status).toBe("pending");
      expect(mapWorkflowRunStep({ step_id: "s1", status: "blocked" }, "f").status).toBe("denied");
    });

    it("defaults unknown statuses to pending instead of passing through raw strings", () => {
      const step = mapWorkflowRunStep({ step_id: "s1", status: "UNKNOWN_STATE" }, "f");
      expect(step.status).toBe("pending");
    });

    it("uses fallback ID when step_id is missing", () => {
      const step = mapWorkflowRunStep({}, "fallback-id");
      expect(step.id).toBe("fallback-id");
    });

    it("carries audit_hash from the backend run-step into auditHash (task-913b6c6c)", () => {
      const step = mapWorkflowRunStep(
        { step_id: "s1", status: "succeeded", audit_hash: "abcdef0123456789deadbeefcafebabe" },
        "f",
      );
      expect(step.auditHash).toBe("abcdef0123456789deadbeefcafebabe");
    });

    it("collapses null audit_hash to undefined so the overlay falls back to the muted placeholder", () => {
      const step = mapWorkflowRunStep(
        { step_id: "s1", status: "succeeded", audit_hash: null },
        "f",
      );
      expect(step.auditHash).toBeUndefined();
    });

    it("leaves auditHash undefined when the backend omits the field entirely", () => {
      const step = mapWorkflowRunStep({ step_id: "s1", status: "pending" }, "f");
      expect(step.auditHash).toBeUndefined();
    });
  });

  describe("mapWorkflowStep policy gate threading (task-913b6c6c)", () => {
    it("carries policy_gate=allow from the backend def-step into policyGate", () => {
      const step = mapWorkflowStep({ id: "s1", name: "step", type: "job", policy_gate: "allow" }, "f");
      expect(step.policyGate).toBe("allow");
    });

    it("carries policy_gate=deny from the backend def-step into policyGate", () => {
      const step = mapWorkflowStep({ id: "s1", name: "step", type: "job", policy_gate: "deny" }, "f");
      expect(step.policyGate).toBe("deny");
    });

    it("carries policy_gate=require_approval from the backend def-step into policyGate", () => {
      const step = mapWorkflowStep(
        { id: "s1", name: "step", type: "job", policy_gate: "require_approval" },
        "f",
      );
      expect(step.policyGate).toBe("require_approval");
    });

    it("leaves policyGate undefined when the backend omits the field", () => {
      const step = mapWorkflowStep({ id: "s1", name: "step", type: "job" }, "f");
      expect(step.policyGate).toBeUndefined();
    });
  });

  describe("microsToISO edge cases", () => {
    it("returns null for non-finite values", () => {
      expect(microsToISO(NaN)).toBeNull();
      expect(microsToISO(Infinity)).toBeNull();
      expect(microsToISO(-1)).toBeNull();
      expect(microsToISO(0)).toBeNull();
    });

    it("returns null for non-numbers", () => {
      expect(microsToISO("not a number")).toBeNull();
      expect(microsToISO(null)).toBeNull();
      expect(microsToISO(undefined)).toBeNull();
    });

    it("converts valid microsecond timestamps", () => {
      const result = microsToISO(1_700_000_000_000_000);
      expect(result).toContain("2023");
      expect(result).toContain("T");
    });
  });

  describe("mapOutputSafetyRecord robustness", () => {
    it("returns undefined for null/non-object input", () => {
      expect(mapOutputSafetyRecord(null as unknown as undefined)).toBeUndefined();
      expect(mapOutputSafetyRecord(undefined)).toBeUndefined();
      expect(mapOutputSafetyRecord("string" as unknown as undefined)).toBeUndefined();
    });

    it("returns empty findings array when findings is not an array", () => {
      const result = mapOutputSafetyRecord({ decision: "ALLOW", findings: "not-array" as unknown as undefined });
      expect(result?.findings).toEqual([]);
    });

    it("maps findings correctly when present", () => {
      const result = mapOutputSafetyRecord({
        decision: "QUARANTINE",
        findings: [{ type: "pii", severity: "high", detail: "SSN detected" }],
      });
      expect(result?.decision).toBe("QUARANTINE");
      expect(result?.findings).toHaveLength(1);
      expect(result?.findings?.[0].type).toBe("pii");
    });
  });

  describe("normalizeDecisionType edge cases", () => {
    it("defaults unknown values to deny (fail-closed)", () => {
      expect(normalizeDecisionType("UNKNOWN")).toBe("deny");
      expect(normalizeDecisionType("BLOCK")).toBe("deny");
    });

    it("maps protobuf-prefixed variants", () => {
      expect(normalizeDecisionType("DECISION_TYPE_ALLOW")).toBe("allow");
      expect(normalizeDecisionType("DECISION_TYPE_DENY")).toBe("deny");
      expect(normalizeDecisionType("DECISION_TYPE_REQUIRE_HUMAN")).toBe("require_approval");
    });
  });

  describe("governance decision hardening", () => {
    it("falls back to deny and warns for unknown verdicts", async () => {
      // task-1acf9c07 Pass C: production warn paths now go through the
      // structured logger at src/lib/logger.ts (component + msg + fields)
      // rather than a free-form console.warn template string. The test
      // asserts that contract directly so future migrations to the logger
      // module's wire format don't silently break observability.
      const { logger } = await import("../lib/logger");
      const warn = vi.spyOn(logger, "warn").mockImplementation(() => undefined);

      expect(normalizeGovernanceVerdict("ESCALATE_LATER")).toBe("deny");
      expect(warn).toHaveBeenCalledWith(
        "transform",
        "unknown governance verdict, defaulting to deny",
        { raw: "ESCALATE_LATER" },
      );

      warn.mockRestore();
    });

    it("skips malformed governance timestamps instead of throwing", () => {
      expect(
        mapGovernanceDecision({
          job_id: "job-bad-ts",
          topic: "jobs.review",
          matched_rule: "rule-9",
          verdict: "DENY",
          reason: "invalid timestamp",
          agent_id: "agent-9",
          timestamp: "not-a-date",
        }),
      ).toBeNull();
    });
  });

  describe("mapJobRecord with missing/malformed data", () => {
    it("generates stable ID for empty ID (not undefined)", () => {
      const job = mapJobRecord({ id: "", topic: "test" });
      expect(job.id).toBeTruthy();
      expect(typeof job.id).toBe("string");
    });

    it("handles completely empty record without crashing", () => {
      const job = mapJobRecord({ id: "x" });
      expect(job.id).toBe("x");
      expect(job.topic).toBe("");
      expect(job.status).toBe("pending");
      expect(job.capabilities).toEqual([]);
      expect(job.riskTags).toEqual([]);
    });

    it("maps all known job status values without falling through to default", () => {
      const nonPendingStatuses = [
        "SCHEDULED", "DISPATCHED", "RUNNING", "SUCCEEDED",
        "FAILED", "CANCELLED", "APPROVAL_REQUIRED", "DENIED", "TIMEOUT",
        "OUTPUT_QUARANTINED",
      ];
      for (const s of nonPendingStatuses) {
        const job = mapJobRecord({ id: "j", state: s });
        expect(job.status).not.toBe("pending");
      }
      expect(mapJobRecord({ id: "j", state: "PENDING" }).status).toBe("pending");
    });
  });

  describe("mapJobRecord forwards origin refs (task-aed4eef5)", () => {
    it("forwards workflow_run_id to job.workflowRunId", () => {
      const job = mapJobRecord({ id: "j1", workflow_run_id: "wfr-X" });
      expect(job.workflowRunId).toBe("wfr-X");
    });

    it("forwards metadata.session_id onto job.metadata", () => {
      const job = mapJobRecord({ id: "j1", metadata: { session_id: "sess-X" } });
      expect(job.metadata?.session_id).toBe("sess-X");
    });

    it("forwards metadata.run_id onto job.metadata", () => {
      const job = mapJobRecord({ id: "j1", metadata: { run_id: "wfr-Y" } });
      expect(job.metadata?.run_id).toBe("wfr-Y");
    });

    it("forwards labels onto job.labels", () => {
      const job = mapJobRecord({ id: "j1", labels: { session_id: "sess-Z" } });
      expect(job.labels?.session_id).toBe("sess-Z");
    });

    it("merges actor metadata with origin metadata (no overwrite)", () => {
      const job = mapJobRecord({
        id: "j1",
        actor_id: "a1",
        actor_type: "agent",
        metadata: { session_id: "sess-X" },
      });
      expect(job.metadata?.actor_id).toBe("a1");
      expect(job.metadata?.actor_type).toBe("agent");
      expect(job.metadata?.session_id).toBe("sess-X");
    });

    it("preserves backwards compat when origin fields are absent", () => {
      const job = mapJobRecord({ id: "j1", actor_id: "a1" });
      expect(job.workflowRunId).toBeUndefined();
      expect(job.labels).toBeUndefined();
      expect(job.metadata?.session_id).toBeUndefined();
      expect(job.metadata?.run_id).toBeUndefined();
      expect(job.metadata?.actor_id).toBe("a1");
    });
  });

  describe("mapWorkflowRun status normalization", () => {
    it("normalizes run-level status", () => {
      const run = mapWorkflowRun({
        id: "run-1",
        workflow_id: "wf-1",
        status: "completed",
      });
      expect(run.status).toBe("succeeded");
    });

    it("normalizes queued/blocked run-level aliases", () => {
      const queuedRun = mapWorkflowRun({
        id: "run-queued",
        workflow_id: "wf-1",
        status: "queued",
      });
      expect(queuedRun.status).toBe("pending");

      const blockedRun = mapWorkflowRun({
        id: "run-blocked",
        workflow_id: "wf-1",
        status: "blocked",
      });
      expect(blockedRun.status).toBe("denied");
    });

    it("handles missing steps gracefully", () => {
      const run = mapWorkflowRun({
        id: "run-2",
        workflow_id: "wf-1",
        status: "running",
      });
      expect(run.steps).toEqual([]);
    });
  });

  describe("mapOutputSafetyRecord", () => {
    it("handles finding with all optional fields missing", () => {
      const result = mapOutputSafetyRecord({
        decision: "ALLOW",
        findings: [{ type: "pii", severity: "high", detail: "found SSN" }],
      });
      expect(result).toBeDefined();
      expect(result!.findings).toHaveLength(1);
      expect(result!.findings![0].scanner).toBeUndefined();
      expect(result!.findings![0].confidence).toBeUndefined();
      expect(result!.findings![0].matched_pattern).toBeUndefined();
      expect(result!.findings![0].offset).toBeUndefined();
      expect(result!.findings![0].length).toBeUndefined();
    });

    it("preserves optional fields when present", () => {
      const result = mapOutputSafetyRecord({
        decision: "QUARANTINE",
        findings: [{
          type: "secret",
          severity: "critical",
          detail: "API key",
          scanner: "regex",
          confidence: 0.95,
          matched_pattern: "sk-.*",
          offset: 42,
          length: 51,
        }],
      });
      expect(result!.findings![0].scanner).toBe("regex");
      expect(result!.findings![0].confidence).toBe(0.95);
      expect(result!.findings![0].matched_pattern).toBe("sk-.*");
      expect(result!.findings![0].offset).toBe(42);
      expect(result!.findings![0].length).toBe(51);
    });

    it("returns undefined for null/undefined input", () => {
      expect(mapOutputSafetyRecord(undefined)).toBeUndefined();
      expect(mapOutputSafetyRecord(null as unknown as undefined)).toBeUndefined();
    });

    it("handles empty findings array", () => {
      const result = mapOutputSafetyRecord({ decision: "ALLOW", findings: [] });
      expect(result!.findings).toEqual([]);
    });
  });
});

describe("api/transform edge mappers", () => {
  const token = "Bearer edge022-secret-token";
  const apiKey = "sk-edge022-secret-value";

  it("maps Edge session pages with camelCase fields, future enum preservation, and cursor pagination", () => {
    const minimal = mapEdgeSession({
      session_id: "edge_sess_min",
      tenant_id: "tenant-a",
      principal_type: "service",
      trace_id: "trace-min",
      policy_mode: "enforce",
      status: "running",
      started_at: "2026-05-02T09:59:00Z",
    });
    const page = mapEdgeSessionPage({
      items: [
        {
          session_id: "edge_sess_1",
          tenant_id: "tenant-a",
          principal_id: "user-1",
          principal_type: "human",
          agent_product: "claude-code",
          agent_version: "1.2.3",
          mode: "future-mode",
          repo: "cordum",
          git_remote: "origin",
          git_branch: "feature/edge",
          git_sha: "abc123",
          cwd: "D:/Cordum/cordum",
          host_id: "host-1",
          device_id: "device-1",
          trace_id: "trace-1",
          workflow_run_id: "run-1",
          job_id: "job-1",
          policy_snapshot: "policy:v1",
          enforcement_layers: { hook: true, mcp: false },
          policy_mode: "observe",
          status: "paused_future",
          risk_summary: {
            denied_count: 2,
            approval_count: 1,
            artifact_count: 3,
            max_risk: "extreme",
          },
          started_at: "2026-05-02T10:00:00Z",
          ended_at: null,
          labels: { safe: "yes", token },
        },
      ],
      next_cursor: "cursor-2",
    });

    expect(minimal).toMatchObject({
      sessionId: "edge_sess_min",
      riskSummary: { deniedCount: 0, approvalCount: 0, artifactCount: 0 },
      endedAt: undefined,
    });
    expect(page.nextCursor).toBe("cursor-2");
    expect(page.items[0]).toMatchObject({
      sessionId: "edge_sess_1",
      tenantId: "tenant-a",
      principalId: "user-1",
      principalType: "human",
      agentProduct: "claude-code",
      mode: "future-mode",
      status: "paused_future",
      workflowRunId: "run-1",
      jobId: "job-1",
      riskSummary: {
        deniedCount: 2,
        approvalCount: 1,
        artifactCount: 3,
        maxRisk: "extreme",
      },
      endedAt: null,
      labels: { safe: "yes" },
    });
    expect(page.items[0].startedAt).toBe("2026-05-02T10:00:00.000Z");
    expect(JSON.stringify(page)).not.toContain(token);
  });

  it("maps Edge executions and action events while preserving only redacted event surfaces", () => {
    const execution = mapAgentExecution({
      execution_id: "exec-1",
      session_id: "edge_sess_1",
      tenant_id: "tenant-a",
      adapter: "custom-adapter",
      mode: "local-dev",
      workflow_run_id: "run-1",
      step_id: "step-1",
      job_id: "job-1",
      attempt: 2,
      trace_id: "trace-1",
      worker_id: "worker-1",
      policy_snapshot: "policy:v1",
      status: "future-status",
      started_at: "2026-05-02T10:00:01Z",
      ended_at: null,
      metrics: {
        events: 4,
        allow: 1,
        deny: 1,
        require_approval: 1,
        artifacts: 2,
        llm_cost_usd: 0.25,
      },
    });

    const event = mapAgentActionEvent({
      event_id: "evt-1",
      session_id: "edge_sess_1",
      execution_id: "exec-1",
      tenant_id: "tenant-a",
      principal_id: "user-1",
      seq: 7,
      ts: "2026-05-02T10:00:02Z",
      layer: "future-layer",
      kind: "hook.pre_tool_use",
      agent_product: "claude-code",
      tool_name: "Bash",
      tool_use_id: "tool-1",
      action_name: "shell.exec",
      capability: "runtime.process",
      risk_tags: ["shell", token],
      input_redacted: {
        safe_summary: "redacted shell request",
        prompt: "raw prompt must disappear",
        tool_input: { command: "rm -rf /" },
        tool_input_redacted: { command_redacted: "rm -rf <path>" },
        nested: {
          Authorization: token,
          input_hash: "sha256:input",
        },
      },
      input_hash: "sha256:input",
      decision: "FUTURE_DECISION",
      decision_reason: apiKey,
      rule_id: "rule-secret-read",
      policy_snapshot: "policy:v1",
      approval_ref: "edge_appr_1",
      artifact_ptrs: [
        {
          artifact_type: "edge.tool_input",
          session_id: "edge_sess_1",
          execution_id: "exec-1",
          event_id: "evt-1",
          tenant_id: "tenant-a",
          retention_class: "audit",
          redaction_level: "strict",
          sha256: "sha256:artifact",
          uri: "artifact://tenant-a/evt-1",
          created_at: "2026-05-02T10:00:03Z",
          size_bytes: 123,
          content_type: "application/json",
        },
        {
          artifact_type: "edge.transcript",
          session_id: "edge_sess_1",
          execution_id: "exec-1",
          event_id: "evt-1",
          tenant_id: "tenant-a",
          retention_class: "audit",
          redaction_level: "strict",
          sha256: "sha256:bad",
          uri: "https://storage.example/download?X-Amz-Signature=secret",
          created_at: "2026-05-02T10:00:04Z",
        },
      ],
      duration_ms: 123,
      status: "degraded",
      error_code: "safety_unavailable",
      error_message: token,
      labels: { safe: "label", signed_url: "https://signed.example?token=secret" },
    });
    const eventPage = mapAgentActionEventPage({
      items: [
        {
          event_id: "evt-page",
          session_id: "edge_sess_1",
          execution_id: "exec-1",
          tenant_id: "tenant-a",
          seq: 8,
          ts: "2026-05-02T10:00:05Z",
          layer: "hook",
          kind: "hook.post_tool_use",
          decision: "RECORDED",
          status: "ok",
        },
      ],
      next_cursor: null,
    });

    expect(execution).toMatchObject({
      executionId: "exec-1",
      status: "future-status",
      metrics: {
        events: 4,
        allow: 1,
        deny: 1,
        requireApproval: 1,
        artifacts: 2,
        llmCostUsd: 0.25,
      },
    });
    expect(event).toMatchObject({
      eventId: "evt-1",
      seq: 7,
      layer: "future-layer",
      decision: "FUTURE_DECISION",
      approvalRef: "edge_appr_1",
      artifactPtrs: [
        { uri: "artifact://tenant-a/evt-1", sizeBytes: 123 },
        { uri: "" },
      ],
      labels: { safe: "label" },
    });
    expect(event.riskTags).toEqual(["shell"]);
    expect(eventPage).toMatchObject({
      nextCursor: null,
      items: [{ eventId: "evt-page", decision: "RECORDED" }],
    });
    expect(event.decisionReason).toBeUndefined();
    expect(event.errorMessage).toBeUndefined();
    expect(event.inputRedacted).toEqual({
      safe_summary: "redacted shell request",
      tool_input_redacted: { command_redacted: "rm -rf <path>" },
      nested: { input_hash: "sha256:input" },
    });
    const serialized = JSON.stringify(event);
    expect(serialized).not.toContain(token);
    expect(serialized).not.toContain(apiKey);
    expect(serialized).not.toContain("raw prompt must disappear");
    expect(serialized).not.toContain("rm -rf /");
    expect(serialized).not.toContain("X-Amz-Signature");
  });

  it("maps pending and resolved Edge approvals with nullable timestamps", () => {
    const pending = mapEdgeApproval({
      approval_ref: "edge_appr_pending",
      tenant_id: "tenant-a",
      session_id: "edge_sess_1",
      execution_id: "exec-1",
      event_id: "evt-1",
      principal_id: "user-1",
      requester: "user-1",
      status: "pending",
      decision: "",
      reason: "requires approval",
      rule_id: "rule-approval",
      policy_snapshot: "policy:v1",
      action_hash: "sha256:action",
      input_hash: "sha256:input",
      created_at: "2026-05-02T10:00:00Z",
      expires_at: null,
      labels: { queue: "default" },
    });
    const resolvedPage = mapEdgeApprovalPage({
      items: [
        {
          approval_ref: "edge_appr_resolved",
          tenant_id: "tenant-a",
          session_id: "edge_sess_1",
          execution_id: "exec-1",
          event_id: "evt-1",
          principal_id: "user-1",
          requester: "user-1",
          resolver_id: "user-2",
          resolved_by: "Alice",
          status: "resolved_future",
          decision: "approve",
          reason: "requires approval",
          resolution_reason: "safe",
          rule_id: "rule-approval",
          policy_snapshot: "policy:v1",
          action_hash: "sha256:action",
          input_hash: "sha256:input",
          created_at: "2026-05-02T10:00:00Z",
          expires_at: "2026-05-02T10:05:00Z",
          resolved_at: "2026-05-02T10:01:00Z",
          consumed_at: null,
          metadata: { safe: "yes", Authorization: token },
        },
      ],
      next_cursor: null,
    });

    expect(pending).toMatchObject({
      approvalRef: "edge_appr_pending",
      status: "pending",
      decision: "",
      expiresAt: null,
    });
    expect(resolvedPage.nextCursor).toBeNull();
    expect(resolvedPage.items[0]).toMatchObject({
      approvalRef: "edge_appr_resolved",
      status: "resolved_future",
      decision: "approve",
      resolverId: "user-2",
      resolvedBy: "Alice",
      consumedAt: null,
      metadata: { safe: "yes" },
    });
    expect(resolvedPage.items[0].resolvedAt).toBe("2026-05-02T10:01:00.000Z");
    expect(JSON.stringify(resolvedPage)).not.toContain(token);
  });

  it("maps Edge export bundles and error envelopes without leaking unsafe details", () => {
    const bundle = mapEdgeSessionExportBundle({
      manifest_version: "edge.export.v1",
      generated_at: "2026-05-02T10:10:00Z",
      tenant_id: "tenant-a",
      redaction_level: "strict",
      session: {
        session_id: "edge_sess_1",
        tenant_id: "tenant-a",
        principal_type: "service",
        trace_id: "trace-1",
        policy_mode: "enforce",
        status: "ended",
        risk_summary: { denied_count: 0, approval_count: 1, artifact_count: 1 },
        started_at: "2026-05-02T10:00:00Z",
      },
      executions: [{ execution_id: "exec-1", session_id: "edge_sess_1", tenant_id: "tenant-a", adapter: "claude-code-hook", mode: "local-dev", status: "succeeded", started_at: "2026-05-02T10:00:01Z" }],
      events: [{ event_id: "evt-1", session_id: "edge_sess_1", execution_id: "exec-1", tenant_id: "tenant-a", seq: 1, ts: "2026-05-02T10:00:02Z", layer: "hook", kind: "hook.pre_tool_use", decision: "ALLOW", status: "ok" }],
      approvals: [{ approval_ref: "edge_appr_1", tenant_id: "tenant-a", session_id: "edge_sess_1", execution_id: "exec-1", event_id: "evt-1", principal_id: "user-1", requester: "user-1", status: "approved", decision: "approve", reason: "safe", rule_id: "rule-1", policy_snapshot: "policy:v1", action_hash: "sha256:action", input_hash: "sha256:input", created_at: "2026-05-02T10:00:00Z" }],
      artifacts: [{ artifact_type: "edge.evidence_bundle", session_id: "edge_sess_1", execution_id: "exec-1", event_id: "evt-1", tenant_id: "tenant-a", retention_class: "audit", redaction_level: "strict", sha256: "sha256:bundle", uri: "edge-artifact://bundle", created_at: "2026-05-02T10:00:03Z" }],
      missing_artifacts: [{ uri: "edge-artifact://missing", sha256: "sha256:missing", artifact_type: "edge.transcript", session_id: "edge_sess_1", execution_id: "exec-1", event_id: "evt-2", reason: "not_found" }],
      job_links: [{ execution_id: "exec-1", job_id: "job-1", workflow_run_id: "run-1", step_id: "step-1" }],
      truncation: { events_truncated: false, event_count: 1, event_scan_limit_hit: false, executions_truncated: false },
    });
    const error = mapEdgeErrorEnvelope({
      code: "idempotency_conflict",
      message: "idempotency key already used",
      request_id: "req-1",
      details: {
        safe_code: "idempotency_conflict",
        token,
        nested: { signed_url: "https://signed.example?token=secret", count: 1 },
      },
    });

    expect(bundle).toMatchObject({
      manifestVersion: "edge.export.v1",
      tenantId: "tenant-a",
      redactionLevel: "strict",
      session: { sessionId: "edge_sess_1" },
      truncation: { eventsTruncated: false, eventCount: 1 },
    });
    expect(bundle.executions).toHaveLength(1);
    expect(bundle.events?.[0].eventId).toBe("evt-1");
    expect(bundle.approvals?.[0].approvalRef).toBe("edge_appr_1");
    expect(bundle.artifacts?.[0].uri).toBe("edge-artifact://bundle");
    expect(bundle.missingArtifacts?.[0].reason).toBe("not_found");
    expect(bundle.jobLinks?.[0]).toEqual({
      executionId: "exec-1",
      jobId: "job-1",
      workflowRunId: "run-1",
      stepId: "step-1",
    });
    expect(error).toEqual({
      code: "idempotency_conflict",
      message: "idempotency key already used",
      requestId: "req-1",
      details: { safe_code: "idempotency_conflict", nested: { count: 1 } },
    });
    expect(JSON.stringify(error)).not.toContain(token);
    expect(JSON.stringify(error)).not.toContain("signed.example");
  });

  it("maps edge.event stream envelopes into safe cache payloads and drops malformed frames", () => {
    expect(mapEdgeEventStreamEnvelope({ type: "bus.packet" })).toBeNull();

    const envelope = mapEdgeEventStreamEnvelope({
      type: "edge.event",
      tenant_id: "tenant-a",
      session_id: "edge_sess_1",
      execution_id: "exec-1",
      event: {
        event_id: "evt-1",
        session_id: "edge_sess_1",
        execution_id: "exec-1",
        tenant_id: "tenant-a",
        seq: 1,
        ts: "2026-05-02T10:00:02Z",
        layer: "hook",
        kind: "approval.requested",
        decision: "REQUIRE_APPROVAL",
        approval_ref: "edge_appr_1",
        status: "blocked",
      },
    });

    expect(envelope).not.toBeNull();
    expect(envelope?.event?.decision).toBe("REQUIRE_APPROVAL");
    expect(mapEdgeStreamPayload(envelope!)).toEqual({
      tenantId: "tenant-a",
      sessionId: "edge_sess_1",
      executionId: "exec-1",
      eventId: "evt-1",
      kind: "approval.requested",
      layer: "hook",
      decision: "REQUIRE_APPROVAL",
      approvalRef: "edge_appr_1",
      artifactPtrs: undefined,
      summary: "approval.requested REQUIRE_APPROVAL",
    });
  });
});

describe("api/transform evals mappers", () => {
  it("maps a backend eval dataset round-trip", async () => {
    const { mapEvalDataset } = await import("./transform");
    const ds = mapEvalDataset({
      id: "ds-1",
      name: "denies-2026-04",
      version: 3,
      tenant: "acme",
      description: "April denies",
      entry_count: 42,
      content_hash: "sha256:abc",
      created_at: "2026-04-01T00:00:00Z",
      updated_at: "2026-04-02T00:00:00Z",
      created_by: "worker-aa42",
    });
    expect(ds).toEqual({
      id: "ds-1",
      name: "denies-2026-04",
      version: 3,
      tenant: "acme",
      description: "April denies",
      entryCount: 42,
      contentHash: "sha256:abc",
      createdAt: "2026-04-01T00:00:00Z",
      updatedAt: "2026-04-02T00:00:00Z",
      createdBy: "worker-aa42",
    });
  });

  it("defaults missing dataset fields safely", async () => {
    const { mapEvalDataset } = await import("./transform");
    const ds = mapEvalDataset({});
    expect(ds.id).toBe("");
    expect(ds.version).toBe(1);
    expect(ds.entryCount).toBe(0);
    expect(ds.contentHash).toBe("");
    expect(ds.updatedAt).toBe("");
  });

  it("maps an eval entry result with known status and drift", async () => {
    const { mapEvalEntryResult } = await import("./transform");
    const r = mapEvalEntryResult({
      entry_id: "e-1",
      input: { topic: "fs.delete" },
      expected_decision: "deny",
      actual_decision: "allow",
      rule_id: "rule-relaxed",
      reason: "policy relaxed",
      status: "regression",
      drift_direction: "relaxed",
    });
    expect(r.status).toBe("regression");
    expect(r.driftDirection).toBe("relaxed");
    expect(r.expectedDecision).toBe("deny");
    expect(r.actualDecision).toBe("allow");
  });

  it("falls back to error + unchanged on unknown status and drift", async () => {
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { mapEvalEntryResult } = await import("./transform");
    const r = mapEvalEntryResult({
      entry_id: "e-2",
      expected_decision: "weird",
      actual_decision: "also-weird",
      status: "not-a-real-status",
      drift_direction: "sideways",
    });
    expect(r.status).toBe("error");
    expect(r.driftDirection).toBe("unchanged");
    // Unknown expected decision -> defaults to "deny" (safer).
    expect(r.expectedDecision).toBe("deny");
    // Unknown actual decision is passed through as lowercased string.
    expect(r.actualDecision).toBe("also-weird");
    warnSpy.mockRestore();
  });

  it("maps an eval run with coerced scorePercent and summary defaults", async () => {
    const { mapEvalRun, isRegressionRun } = await import("./transform");
    const run = mapEvalRun({
      run_id: "run-1",
      dataset_id: "ds-1",
      dataset_name: "denies",
      dataset_version: 2,
      policy_snapshot: "snap-abc",
      started_at: "2026-04-19T12:00:00Z",
      completed_at: "2026-04-19T12:00:05Z",
      summary: {
        total: 10,
        passed: 7,
        failed: 2,
        regressions: 1,
        errored: 0,
        score_percent: 70,
      },
      entries: [
        {
          entry_id: "e-1",
          expected_decision: "deny",
          actual_decision: "allow",
          status: "regression",
          drift_direction: "relaxed",
        },
      ],
    });
    expect(run.summary.scorePercent).toBe(70);
    expect(isRegressionRun(run)).toBe(true);
    expect(run.entries).toHaveLength(1);
  });

  it("coerces NaN/null scorePercent to null", async () => {
    const { mapEvalRun } = await import("./transform");
    const nan = mapEvalRun({ summary: { score_percent: Number.NaN } });
    expect(nan.summary.scorePercent).toBeNull();
    const nil = mapEvalRun({ summary: { score_percent: null } });
    expect(nil.summary.scorePercent).toBeNull();
    const missing = mapEvalRun({});
    expect(missing.summary.scorePercent).toBeNull();
    expect(missing.summary.total).toBe(0);
  });

  it("isRegressionRun returns false when no regressions", async () => {
    const { isRegressionRun } = await import("./transform");
    expect(
      isRegressionRun({
        summary: { total: 5, passed: 5, failed: 0, regressions: 0, errored: 0, scorePercent: 100 },
      }),
    ).toBe(false);
  });
});
