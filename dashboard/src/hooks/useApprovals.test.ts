import { describe, expect, it } from "vitest";
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
    const original = list.items[0];

    const removed = __approvalsInternal.removeApprovalFromList(list, "a-1");
    expect(removed?.items.map((item) => item.id)).toEqual(["a-2"]);

    const restored = __approvalsInternal.restoreApprovalToList(
      removed,
      "a-1",
      original,
    );
    expect(restored?.items.map((item) => item.id)).toEqual(["a-2", "a-1"]);

    const unchanged = __approvalsInternal.restoreApprovalToList(
      restored,
      "a-1",
      original,
    );
    expect(unchanged).toEqual(restored);
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
      expect(afterRemoveA?.items.map((i) => i.id)).toEqual(["a-2", "a-3"]);

      // Step 2: Approve B → remove B from the already-modified list
      const afterRemoveB = __approvalsInternal.removeApprovalFromList(afterRemoveA!, "a-2");
      expect(afterRemoveB?.items.map((i) => i.id)).toEqual(["a-3"]);

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
      expect(afterRestoreB?.items.map((i) => i.id)).toEqual(["a-3", "a-2"]);
      expect(afterRestoreB?.items.find((i) => i.id === "a-1")).toBeUndefined();
    });

    it("double restore of same item is idempotent", () => {
      const list: ApiResponse<Approval[]> = {
        items: [makeApproval({ id: "a-1" }), makeApproval({ id: "a-2" })],
      };

      const removed = __approvalsInternal.removeApprovalFromList(list, "a-1");
      const original = list.items[0];

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
