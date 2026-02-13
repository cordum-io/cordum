import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __approvalsInternal,
  useApproval,
  useApprovalHistory,
  useApprovals,
  useApproveJob,
  useApproveStep,
  useRejectJob,
} from "./useApprovals";

const { addToastMock, loggerMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../state/toast", () => ({
  useToastStore: {
    getState: () => ({ addToast: addToastMock }),
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useApprovals internals", () => {
  it("buildHistoryParams serializes filters correctly", () => {
    expect(__approvalsInternal.buildHistoryParams({})).toBe("");
    expect(
      __approvalsInternal.buildHistoryParams({ page: 1, perPage: 25, sort: "desc" }),
    ).toBe("?page=1&perPage=25&sort=desc");
  });
});

describe("useApprovals hooks", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("useApprovals fetches items, maps approvals, and filters null records", async () => {
    mockFetch([
      {
        match: "/approvals",
        method: "GET",
        body: {
          items: [
            {
              approval_ref: "a1",
              policy_reason: "needs approval",
              job: {
                id: "j1",
                state: "RUNNING",
                topic: "sys.job.submit",
                updated_at: 1_707_000_000_000_000,
              },
            },
            {},
          ],
          next_cursor: 9,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useApprovals());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data?.items).toHaveLength(1);
    expect(hook.result.current?.data?.items[0].id).toBe("a1");
    expect(hook.result.current?.data?.next_cursor).toBe(9);
    hook.unmount();
  });

  it("useApproval uses cache placeholder and resolves selected approval", async () => {
    mockFetch([
      {
        match: "/approvals",
        method: "GET",
        body: {
          items: [
            {
              approval_ref: "a1",
              job: {
                id: "j1",
                state: "RUNNING",
                updated_at: 1_707_000_000_000_000,
              },
            },
          ],
        },
      },
    ]);
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["approvals", "all"], {
      items: [
        {
          id: "a1",
          jobId: "j1",
          status: "pending",
          requestedAt: "2026-02-13T00:00:00.000Z",
        },
      ],
    });
    const hook = renderWithQueryClient(() => useApproval("a1"), queryClient);

    expect(hook.result.current?.data?.id).toBe("a1");
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data?.id).toBe("a1");
    hook.unmount();
  });

  it("useApproveJob removes from cache optimistically and restores on error", async () => {
    mockFetch([{ match: "/approvals/a1/approve", method: "POST", rejectWith: new Error("approve failed") }]);
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["approvals", "all"], {
      items: [{ id: "a1" }, { id: "a2" }],
    });
    const hook = renderWithQueryClient(() => useApproveJob(), queryClient);

    await expect(
      hook.result.current?.mutateAsync({ id: "a1" }),
    ).rejects.toThrow("approve failed");

    expect(queryClient.getQueryData<{ items: Array<{ id: string }> }>(["approvals", "all"])?.items).toEqual([
      { id: "a1" },
      { id: "a2" },
    ]);
    expect(addToastMock).toHaveBeenCalledWith({
      type: "error",
      title: "Approval failed",
      description: "approve failed",
    });
    hook.unmount();
  });

  it("useRejectJob posts reason payload and supports optimistic update flow", async () => {
    const fetchSpy = mockFetch([{ match: "/approvals/a2/reject", method: "POST", body: {} }]);
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["approvals", "all"], {
      items: [{ id: "a1" }, { id: "a2" }],
    });
    const hook = renderWithQueryClient(() => useRejectJob(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync({ id: "a2", reason: "unsafe", comment: "manual check" });
    });

    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({ reason: "unsafe", note: "manual check" });
    expect(queryClient.getQueryData<{ items: Array<{ id: string }> }>(["approvals", "all"])?.items).toEqual([
      { id: "a1" },
    ]);
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Rejected" });
    hook.unmount();
  });

  it("useApprovalHistory filters approve/reject and parses snapshot_after details", async () => {
    mockFetch([
      {
        match: "/policy/audit?page=1&perPage=20&sort=desc",
        method: "GET",
        body: {
          items: [
            {
              id: "h1",
              action: "approve",
              resource_id: "j1",
              actor_id: "alice",
              created_at: "2026-02-13T10:05:00.000Z",
              snapshot_after: JSON.stringify({
                topic: "sys.job.submit",
                workflow_id: "wf-1",
                requested_at: "2026-02-13T10:00:00.000Z",
              }),
            },
            {
              id: "h2",
              action: "view",
              resource_id: "j2",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useApprovalHistory({ page: 1, perPage: 20, sort: "desc" }),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data?.items).toHaveLength(1);
    expect(hook.result.current?.data?.items[0]).toMatchObject({
      id: "h1",
      action: "approve",
      topic: "sys.job.submit",
      workflowId: "wf-1",
      waitDurationMs: 300000,
    });
    hook.unmount();
  });

  it("useApproveStep validates required params and posts correct endpoint", async () => {
    const modFetch = mockFetch([
      {
        match: "/workflows/wf-1/runs/run-1/steps/step-1/approve",
        method: "POST",
        body: {},
      },
    ]);

    const hook = renderWithQueryClient(() => useApproveStep());

    await expect(
      hook.result.current?.mutateAsync({
        workflowId: "",
        runId: "run-1",
        stepId: "step-1",
      }),
    ).rejects.toThrow("workflowId, runId, and stepId are required");

    await act(async () => {
      await hook.result.current?.mutateAsync({
        workflowId: "wf-1",
        runId: "run-1",
        stepId: "step-1",
      });
    });
    expect(modFetch).toHaveBeenCalledTimes(1);
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Step approved" });
    hook.unmount();
  });
});

