import { describe, expect, it } from "vitest";
import {
  computeUrgencyLevel,
  deriveApprovalActionability,
  deriveApprovalStatus,
  mapApprovalItem,
  mapDLQEntry,
  mapHeartbeatToWorker,
  mapJobDetail,
  mapJobRecord,
  mapOutputSafetyRecord,
  mapPolicyBundleDetail,
  mapWorkflow,
  mapWorkflowRun,
  mapWorkflowRunStep,
  microsToISO,
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
