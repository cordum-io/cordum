import { describe, expect, it } from "vitest";
import {
  computeUrgencyLevel,
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
    expect((workflow.steps[0].config as Record<string, unknown>).retryMax).toBe(2);

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
      { decision: "reject", expected: "denied" },
      { jobState: "DENIED", expected: "denied" },
      { jobState: "OUTPUT_QUARANTINED", expected: "quarantined" },
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
    expect(approval?.humanSummary).toContain('Job on "job.review"');
    expect(approval?.urgencyLevel).toBeDefined();
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
    it("returns empty string for non-finite values", () => {
      expect(microsToISO(NaN)).toBe("");
      expect(microsToISO(Infinity)).toBe("");
      expect(microsToISO(-1)).toBe("");
      expect(microsToISO(0)).toBe("");
    });

    it("returns empty string for non-numbers", () => {
      expect(microsToISO("not a number")).toBe("");
      expect(microsToISO(null)).toBe("");
      expect(microsToISO(undefined)).toBe("");
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

    it("handles missing steps gracefully", () => {
      const run = mapWorkflowRun({
        id: "run-2",
        workflow_id: "wf-1",
        status: "running",
      });
      expect(run.steps).toEqual([]);
    });
  });
});
