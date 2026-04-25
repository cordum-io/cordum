import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import type { Approval, ApiResponse } from "@/api/types";
import { __approvalsInternal } from "./useApprovals";

function makeApproval(overrides: Partial<Approval>): Approval {
  return {
    id: "approval-default",
    jobId: "job-default",
    status: "pending",
    requestedAt: "2026-01-01T00:00:00.000Z",
    ...overrides,
  };
}

describe("useApprovals internals", () => {
  it("builds history query params", () => {
    const params = __approvalsInternal.buildHistoryParams({
      page: 2,
      perPage: 25,
      sort: "desc",
    });

    expect(params).toContain("page=2");
    expect(params).toContain("perPage=25");
    expect(params).toContain("sort=desc");
  });

  it("filters approval list by status when requested", () => {
    const approvals = [
      makeApproval({ id: "a-1", status: "pending" }),
      makeApproval({ id: "a-2", status: "approved" }),
      makeApproval({ id: "a-3", status: "rejected" }),
    ];

    expect(__approvalsInternal.filterApprovalsByStatus(approvals, "approved")).toEqual([
      approvals[1],
    ]);
    expect(__approvalsInternal.filterApprovalsByStatus(approvals, undefined)).toEqual(
      approvals,
    );
  });

  it("applies optimistic removal and targeted restoration for approve/reject mutations", () => {
    const list: ApiResponse<Approval[]> = {
      items: [
        makeApproval({ id: "a-1", status: "pending" }),
        makeApproval({ id: "a-2", status: "pending" }),
      ],
    };
    const original = list.items![0];

    const removed = __approvalsInternal.removeApprovalFromList(list, "a-1");
    expect(removed?.items!.map((item) => item.id)).toEqual(["a-2"]);

    const restored = __approvalsInternal.restoreApprovalToList(
      removed,
      "a-1",
      original,
    );
    expect(restored?.items!.map((item) => item.id)).toEqual(["a-2", "a-1"]);

    const unchanged = __approvalsInternal.restoreApprovalToList(
      restored,
      "a-1",
      original,
    );
    expect(unchanged).toEqual(restored);
  });

  it("preserves decision-first approval data across optimistic rollback helpers", () => {
    const enriched = makeApproval({
      id: "a-rich",
      humanSummary: "Approve 1250 USD request with Acme Travel",
      decisionSummary: {
        source: "workflow_payload",
        completeness: "rich",
        contextStatus: "available",
        title: "Approve 1250 USD request with Acme Travel",
        vendor: "Acme Travel",
      },
      workflowContext: {
        workflowId: "wf-1",
        workflowName: "Expense Approval",
        runId: "run-1",
        stepId: "approve",
      },
    });
    const list: ApiResponse<Approval[]> = {
      items: [enriched, makeApproval({ id: "a-2", status: "pending" })],
    };

    const removed = __approvalsInternal.removeApprovalFromList(list, "a-rich");
    expect(removed?.items!.map((item) => item.id)).toEqual(["a-2"]);

    const restored = __approvalsInternal.restoreApprovalToList(
      removed,
      "a-rich",
      enriched,
    );
    const restoredItem = restored?.items?.find((item) => item.id === "a-rich");
    expect(restoredItem?.decisionSummary?.vendor).toBe("Acme Travel");
    expect(restoredItem?.workflowContext?.workflowName).toBe("Expense Approval");
    expect(restoredItem?.humanSummary).toBe(
      "Approve 1250 USD request with Acme Travel",
    );
  });

  it("preserves degraded workflow approval markers across optimistic rollback helpers", () => {
    const degraded = makeApproval({
      id: "a-missing",
      status: "pending",
      humanSummary: "Approve manager-approval",
      decisionSummary: {
        source: "workflow_payload",
        completeness: "partial",
        contextStatus: "missing",
        title: "Approve manager-approval",
        why: "manager review required",
        missingFields: ["approval_context", "business_context"],
      },
      workflowContext: {
        workflowId: "wf-9",
        workflowName: "Expense Approval",
        runId: "run-9",
        stepId: "manager-approval",
      },
      contextPtr: "redis://ctx:job-9",
    });
    const list: ApiResponse<Approval[]> = {
      items: [degraded, makeApproval({ id: "a-2", status: "pending" })],
    };

    const removed = __approvalsInternal.removeApprovalFromList(list, "a-missing");
    const restored = __approvalsInternal.restoreApprovalToList(
      removed,
      "a-missing",
      degraded,
    );
    const restoredItem = restored?.items?.find((item) => item.id === "a-missing");

    expect(restoredItem?.decisionSummary?.contextStatus).toBe("missing");
    expect(restoredItem?.decisionSummary?.missingFields).toEqual([
      "approval_context",
      "business_context",
    ]);
    expect(restoredItem?.workflowContext?.workflowId).toBe("wf-9");
    expect(restoredItem?.contextPtr).toBe("redis://ctx:job-9");
    expect(restoredItem?.humanSummary).toBe("Approve manager-approval");
  });

  it("finds an approval item in mutation snapshots and validates approve-step input", () => {
    const snapshot = {
      previous: [
        [
          ["approvals", "list"],
          {
            items: [makeApproval({ id: "a-1" }), makeApproval({ id: "a-2" })],
          },
        ],
      ],
    };
    const found = __approvalsInternal.findApprovalInSnapshot(
      snapshot as Parameters<typeof __approvalsInternal.findApprovalInSnapshot>[0],
      "a-2",
    );
    expect(found?.id).toBe("a-2");
  });

  it("matches approvals by job id so actions target the backend job identifier", () => {
    const approval = makeApproval({ id: "approval-ref-1", jobId: "job-1" });
    expect(__approvalsInternal.matchesApprovalIdentifier(approval, "approval-ref-1")).toBe(true);
    expect(__approvalsInternal.matchesApprovalIdentifier(approval, "job-1")).toBe(true);
    expect(__approvalsInternal.matchesApprovalIdentifier(approval, "job-2")).toBe(false);
  });

  it("keeps optimistic removal only for already-resolved approval conflicts", () => {
    const alreadyResolved = new ApiError(409, "already resolved", {
      code: "approval_already_resolved",
    });
    const retryable = new ApiError(409, "retry", {
      code: "approval_retryable_lock",
      retryable: true,
    });

    expect(__approvalsInternal.getApprovalConflictCode(alreadyResolved)).toBe(
      "approval_already_resolved",
    );
    expect(__approvalsInternal.getApprovalConflictCode(retryable)).toBe(
      "approval_retryable_lock",
    );
    expect(__approvalsInternal.shouldKeepOptimisticRemoval(alreadyResolved)).toBe(true);
    expect(__approvalsInternal.shouldKeepOptimisticRemoval(retryable)).toBe(false);
    expect(__approvalsInternal.shouldKeepOptimisticRemoval(new Error("boom"))).toBe(false);
  });

  // ---------------------------------------------------------------------------
  // Mutation safety: concurrent optimistic removal + rollback
  // ---------------------------------------------------------------------------

  describe("concurrent optimistic rollback safety", () => {
    it("per-item restore does not interfere with other concurrent removals", () => {
      // Simulates: approve A fires, approve B fires, B fails — only B should be restored
      const list: ApiResponse<Approval[]> = {
        items: [
          makeApproval({ id: "a-1", status: "pending" }),
          makeApproval({ id: "a-2", status: "pending" }),
          makeApproval({ id: "a-3", status: "pending" }),
        ],
      };

      // Step 1: Approve A → remove A
      const afterRemoveA = __approvalsInternal.removeApprovalFromList(list, "a-1");
      expect(afterRemoveA?.items!.map((i) => i.id)).toEqual(["a-2", "a-3"]);

      // Step 2: Approve B → remove B from the already-modified list
      const afterRemoveB = __approvalsInternal.removeApprovalFromList(afterRemoveA!, "a-2");
      expect(afterRemoveB?.items!.map((i) => i.id)).toEqual(["a-3"]);

      // Snapshot for B captured the state AFTER A was removed
      const snapshotB = {
        previous: [[["approvals"], afterRemoveA]] as [unknown, ApiResponse<Approval[]> | undefined][],
      };

      // Step 3: B fails → restore only B using per-item restore
      const originalB = __approvalsInternal.findApprovalInSnapshot(
        snapshotB as Parameters<typeof __approvalsInternal.findApprovalInSnapshot>[0],
        "a-2",
      );
      expect(originalB?.id).toBe("a-2");
      const afterRestoreB = __approvalsInternal.restoreApprovalToList(afterRemoveB!, "a-2", originalB);
      // A should still be removed, only B restored
      expect(afterRestoreB?.items!.map((i) => i.id)).toEqual(["a-3", "a-2"]);
      expect(afterRestoreB?.items!.find((i) => i.id === "a-1")).toBeUndefined();
    });

    it("double restore of same item is idempotent", () => {
      const list: ApiResponse<Approval[]> = {
        items: [makeApproval({ id: "a-1" }), makeApproval({ id: "a-2" })],
      };

      const removed = __approvalsInternal.removeApprovalFromList(list, "a-1");
      const original = list.items![0];

      const restored = __approvalsInternal.restoreApprovalToList(removed!, "a-1", original);
      const restoredAgain = __approvalsInternal.restoreApprovalToList(restored!, "a-1", original);
      // Should be same reference — no-op when item already present
      expect(restoredAgain).toBe(restored);
    });

    it("removeApprovalFromList is safe on undefined/empty input", () => {
      expect(__approvalsInternal.removeApprovalFromList(undefined, "a-1")).toBeUndefined();
      expect(__approvalsInternal.removeApprovalFromList({ items: [] }, "a-1")).toEqual({ items: [] });
    });

    it("restoreApprovalToList is safe when originalItem is undefined", () => {
      const list: ApiResponse<Approval[]> = { items: [makeApproval({ id: "a-1" })] };
      const result = __approvalsInternal.restoreApprovalToList(list, "a-2", undefined);
      // Should return unchanged list
      expect(result).toBe(list);
    });

    it("findApprovalInSnapshot returns undefined for nonexistent IDs", () => {
      const snapshot = {
        previous: [[["key"], { items: [makeApproval({ id: "a-1" })] }]],
      };
      const found = __approvalsInternal.findApprovalInSnapshot(
        snapshot as Parameters<typeof __approvalsInternal.findApprovalInSnapshot>[0],
        "nonexistent",
      );
      expect(found).toBeUndefined();
    });

    it("findApprovalInSnapshot handles empty/undefined snapshot", () => {
      expect(__approvalsInternal.findApprovalInSnapshot(undefined, "a-1")).toBeUndefined();
      expect(
        __approvalsInternal.findApprovalInSnapshot(
          { previous: [] } as Parameters<typeof __approvalsInternal.findApprovalInSnapshot>[0],
          "a-1",
        ),
      ).toBeUndefined();
    });
  });
});

describe("useApproval pagination lookup (task-f9ea8fe2)", () => {
  // Minimal BackendApprovalItem fixture: approval_ref drives Approval.id;
  // job.id satisfies mapApprovalItem's `if (!item.job) return null` guard.
  const backendItem = (id: string) => ({
    approval_ref: id,
    job: { id: `job-${id}`, state: "approval", tenant: "default", topic: "topic.test" },
  });

  it("returns the approval when found on the first page (no second fetch)", async () => {
    const calls: string[] = [];
    const fetcher = async (url: string) => {
      calls.push(url);
      return { items: [backendItem("a-1"), backendItem("a-2")], next_cursor: 100 };
    };
    const result = await __approvalsInternal.lookupApprovalById("a-2", fetcher);
    expect(result.id).toBe("a-2");
    expect(calls).toEqual(["/approvals"]);
  });

  it("walks the paginated list and returns an approval found past page 1 (closes 'approval not found' ghost)", async () => {
    const calls: string[] = [];
    const fetcher = async (url: string) => {
      calls.push(url);
      if (url === "/approvals") {
        return { items: [backendItem("a-1")], next_cursor: 100 };
      }
      if (url === "/approvals?cursor=100") {
        return { items: [backendItem("a-101")], next_cursor: null };
      }
      throw new Error(`unexpected url: ${url}`);
    };
    const result = await __approvalsInternal.lookupApprovalById("a-101", fetcher);
    expect(result.id).toBe("a-101");
    expect(calls).toEqual(["/approvals", "/approvals?cursor=100"]);
  });

  it("throws 'approval not found' when the entire list has been walked without a hit", async () => {
    const fetcher = async (url: string) => {
      if (url === "/approvals") {
        return { items: [backendItem("a-1")], next_cursor: null };
      }
      throw new Error(`unexpected url: ${url}`);
    };
    await expect(__approvalsInternal.lookupApprovalById("nope", fetcher)).rejects.toThrow(
      "approval not found",
    );
  });

  it("throws a max-pages error rather than spinning forever on a misbehaving server", async () => {
    // Simulated server that ALWAYS returns next_cursor (no terminator).
    const fetcher = async () => ({
      items: [backendItem("ignore")],
      next_cursor: 999,
    });
    await expect(
      __approvalsInternal.lookupApprovalById("never-found", fetcher, 3),
    ).rejects.toThrow(/exceeded 3 pages/);
  });
});
