import { describe, expect, it } from "vitest";
import type { Job } from "@/api/types";
import { __jobsInternal } from "./useJobs";

function makeJob(overrides: Partial<Job>): Job {
  return {
    id: "job-default",
    type: "job.default",
    topic: "job.default",
    status: "pending",
    pool: "default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
    ...overrides,
  };
}

describe("useJobs internals", () => {
  it("maps UI job states to backend state enums", () => {
    expect(__jobsInternal.stateToBackend("approval_required")).toBe(
      "APPROVAL_REQUIRED",
    );
    expect(__jobsInternal.stateToBackend("output_quarantined")).toBe(
      "OUTPUT_QUARANTINED",
    );
    expect(__jobsInternal.stateToBackend("failed")).toBe("FAILED");
  });

  it("converts known time ranges to microsecond boundaries", () => {
    const range = __jobsInternal.rangeToMicros("24h");
    expect(typeof range.after).toBe("number");
    expect(typeof range.before).toBe("number");
    expect((range.before ?? 0) - (range.after ?? 0)).toBeGreaterThan(
      23 * 60 * 60 * 1_000_000,
    );
    expect(__jobsInternal.rangeToMicros("unknown")).toEqual({});
  });

  it("builds query params with state filters and cursor guards", () => {
    const params = __jobsInternal.buildParams({
      state: ["running", "failed"],
      topic: "job.codegen",
      tenant: "acme",
      limit: 50,
      cursor: 12,
    });

    expect(params).toContain("state=RUNNING");
    expect(params).toContain("state=FAILED");
    expect(params).toContain("topic=job.codegen");
    expect(params).toContain("tenant=acme");
    expect(params).toContain("limit=50");
    expect(params).toContain("cursor=12");
  });

  it("applies client-side fallback filtering for state and decision", () => {
    const jobs: Job[] = [
      makeJob({
        id: "job-deny",
        status: "failed",
        safetyDecision: { type: "deny", reason: "blocked" },
      }),
      makeJob({
        id: "job-allow",
        status: "failed",
        safetyDecision: { type: "allow", reason: "ok" },
      }),
      makeJob({
        id: "job-running",
        status: "running",
      }),
    ];

    const filtered = __jobsInternal.filterJobsForClient(jobs, {
      state: ["failed"],
      decision: ["deny"],
    });
    expect(filtered.map((job) => job.id)).toEqual(["job-deny"]);
  });

  it("applies optimistic cancel projections for list and detail records", () => {
    const list = {
      items: [
        makeJob({ id: "job-1", status: "running" }),
        makeJob({ id: "job-2", status: "pending" }),
      ],
      next_cursor: null,
    };

    const updatedList = __jobsInternal.applyOptimisticCancelToList(list, "job-1");
    expect(updatedList?.items[0].status).toBe("cancelled");
    expect(updatedList?.items[1].status).toBe("pending");

    const updatedDetail = __jobsInternal.applyOptimisticCancelToDetail(
      makeJob({ id: "job-3", status: "running" }),
    );
    expect(updatedDetail?.status).toBe("cancelled");
    expect(__jobsInternal.applyOptimisticCancelToDetail(undefined)).toBeUndefined();
  });

  it("validates remediate job ids before mutation requests", () => {
    expect(__jobsInternal.validateRemediateJobId("  job-123  ")).toBe("job-123");
    expect(() => __jobsInternal.validateRemediateJobId("   ")).toThrow(
      "job id is required",
    );
  });

  // ---------------------------------------------------------------------------
  // Mutation safety: optimistic cancel helpers
  // ---------------------------------------------------------------------------

  describe("optimistic cancel safety", () => {
    it("applyOptimisticCancelToList only changes target job status", () => {
      const list = {
        items: [
          makeJob({ id: "job-1", status: "running" }),
          makeJob({ id: "job-2", status: "pending" }),
          makeJob({ id: "job-3", status: "running" }),
        ],
        next_cursor: null,
      };

      const result = __jobsInternal.applyOptimisticCancelToList(list, "job-2");
      expect(result?.items[0].status).toBe("running"); // unchanged
      expect(result?.items[1].status).toBe("cancelled"); // changed
      expect(result?.items[2].status).toBe("running"); // unchanged
    });

    it("applyOptimisticCancelToList preserves all other job fields", () => {
      const original = makeJob({
        id: "job-1",
        status: "running",
        topic: "test.topic",
        pool: "gpu-pool",
      });
      const list = { items: [original], next_cursor: null };

      const result = __jobsInternal.applyOptimisticCancelToList(list, "job-1");
      const updated = result?.items[0];
      expect(updated?.topic).toBe("test.topic");
      expect(updated?.pool).toBe("gpu-pool");
      expect(updated?.id).toBe("job-1");
    });

    it("applyOptimisticCancelToList handles missing item gracefully", () => {
      const list = {
        items: [makeJob({ id: "job-1", status: "running" })],
        next_cursor: null,
      };

      const result = __jobsInternal.applyOptimisticCancelToList(list, "nonexistent");
      // Should return list unchanged (no item has the target ID)
      expect(result?.items[0].status).toBe("running");
    });

    it("applyOptimisticCancelToList returns undefined for undefined input", () => {
      expect(__jobsInternal.applyOptimisticCancelToList(undefined, "job-1")).toBeUndefined();
    });

    it("applyOptimisticCancelToDetail returns undefined for undefined input", () => {
      expect(__jobsInternal.applyOptimisticCancelToDetail(undefined)).toBeUndefined();
    });

    it("concurrent cancel operations produce correct intermediate states", () => {
      // Simulates: cancel A, then cancel B on same list
      const list = {
        items: [
          makeJob({ id: "job-A", status: "running" }),
          makeJob({ id: "job-B", status: "pending" }),
        ],
        next_cursor: null,
      };

      // Cancel A
      const afterCancelA = __jobsInternal.applyOptimisticCancelToList(list, "job-A");
      expect(afterCancelA?.items[0].status).toBe("cancelled");
      expect(afterCancelA?.items[1].status).toBe("pending");

      // Cancel B on already-modified list
      const afterCancelB = __jobsInternal.applyOptimisticCancelToList(afterCancelA!, "job-B");
      expect(afterCancelB?.items[0].status).toBe("cancelled");
      expect(afterCancelB?.items[1].status).toBe("cancelled");
    });
  });
});
