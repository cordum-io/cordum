import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { __dlqInternal, useDLQ, useDeleteDLQ, useRetryDLQ } from "./useDLQ";
import type { ApiResponse, DLQEntry } from "../api/types";

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

describe("useDLQ internals", () => {
  it("buildParams serializes supported filters", () => {
    expect(__dlqInternal.buildParams({})).toBe("");
    expect(
      __dlqInternal.buildParams({
        limit: 25,
        cursor: 10,
        topic: "sys.job.submit",
        since: "2026-02-13T00:00:00.000Z",
      }),
    ).toBe("?limit=25&cursor=10&topic=sys.job.submit&since=2026-02-13T00%3A00%3A00.000Z");
  });
});

describe("useDLQ hooks", () => {
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

  it("useDLQ starts in loading state", () => {
    mockFetch([
      { match: "/dlq/page", method: "GET", body: { items: [], next_cursor: 0 } },
    ]);

    const hook = renderWithQueryClient(() => useDLQ({}));
    expect(hook.result.current?.isLoading).toBe(true);
    expect(hook.result.current?.data).toBeUndefined();
    hook.unmount();
  });

  it("useDLQ returns error state on fetch failure", async () => {
    mockFetch([
      { match: "/dlq/page", method: "GET", status: 500, body: { error: "server error" } },
    ]);

    const hook = renderWithQueryClient(() => useDLQ({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useDLQ fetches /dlq/page with params and maps entries", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/dlq/page?limit=20&cursor=4&topic=sys.job.submit&since=2026-02-13T00%3A00%3A00.000Z",
        method: "GET",
        body: {
          items: [
            {
              job_id: "j1",
              topic: "sys.job.submit",
              reason: "failed",
              attempts: 2,
              created_at: "2026-02-13T10:00:00.000Z",
            },
          ],
          next_cursor: 8,
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useDLQ({
        limit: 20,
        cursor: 4,
        topic: "sys.job.submit",
        since: "2026-02-13T00:00:00.000Z",
      }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.items[0]).toMatchObject({
      id: "j1",
      jobId: "j1",
      originalTopic: "sys.job.submit",
      error: "failed",
    });
    expect(hook.result.current?.data?.next_cursor).toBe(8);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    hook.unmount();
  });

  it("useRetryDLQ optimistically removes entry and restores on error", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/dlq/j1/retry",
        method: "POST",
        rejectWith: new Error("retry failed"),
      },
    ]);

    const queryClient = createTestQueryClient();
    queryClient.setQueryData<ApiResponse<DLQEntry[]>>(["dlq", { limit: 10 }], {
      items: [
        { id: "j1", jobId: "j1", status: "failed" },
        { id: "j2", jobId: "j2", status: "failed" },
      ],
    });

    const hook = renderWithQueryClient(() => useRetryDLQ(), queryClient);

    await expect(
      hook.result.current?.mutateAsync({ id: "j1" }),
    ).rejects.toThrow("retry failed");

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(
      queryClient.getQueryData<ApiResponse<DLQEntry[]>>(["dlq", { limit: 10 }])?.items,
    ).toEqual([
      { id: "j1", jobId: "j1", status: "failed" },
      { id: "j2", jobId: "j2", status: "failed" },
    ]);
    expect(addToastMock).toHaveBeenCalledWith({
      type: "error",
      title: "Failed to retry entry",
      description: "retry failed",
    });

    hook.unmount();
  });

  it("useDeleteDLQ calls delete endpoint, invalidates cache, and shows toast", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/dlq/j9",
        method: "DELETE",
        body: null,
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useDeleteDLQ(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync("j9");
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["dlq"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Entry deleted" });

    hook.unmount();
  });
});

