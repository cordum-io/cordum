import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { __statusInternal, usePipelineMetrics, useRecentJobs, useRecentRuns, useStatus, useWorkersSummary } from "./useStatus";

const { loggerMock } = vi.hoisted(() => ({
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

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

describe("useStatus hooks", () => {
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

  it("useStatus starts in loading state", () => {
    mockFetch([{ match: "/status", method: "GET", body: {} }]);
    const hook = renderWithQueryClient(() => useStatus());
    expect(hook.result.current?.isLoading).toBe(true);
    expect(hook.result.current?.data).toBeUndefined();
    hook.unmount();
  });

  it("useStatus returns error state on fetch failure", async () => {
    mockFetch([{ match: "/status", method: "GET", status: 500, body: { error: "server error" } }]);
    const hook = renderWithQueryClient(() => useStatus());
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useWorkersSummary returns error state on fetch failure", async () => {
    mockFetch([{ match: "/workers", method: "GET", status: 500, body: { error: "server error" } }]);
    const hook = renderWithQueryClient(() => useWorkersSummary());
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useRecentJobs returns error state on fetch failure", async () => {
    mockFetch([{ match: "/jobs", method: "GET", status: 500, body: { error: "server error" } }]);
    const hook = renderWithQueryClient(() => useRecentJobs());
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useStatus fetches /status and configures 10s refetch interval", async () => {
    mockFetch([
      {
        match: "/status",
        method: "GET",
        body: {
          time: "2026-02-13T10:00:00.000Z",
          nats: { connected: true },
          redis: { ok: true },
          workers: { count: 3 },
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useStatus(), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toMatchObject({ workers: { count: 3 } });
    const query = queryClient.getQueryCache().find({ queryKey: ["status"] });
    const options = query?.options as { refetchInterval?: number } | undefined;
    expect(options?.refetchInterval).toBe(10_000);

    hook.unmount();
  });

  it("usePipelineMetrics falls back to /jobs when status pipeline is missing", async () => {
    mockFetch([
      {
        match: "/status",
        method: "GET",
        body: {
          workers: { count: 2 },
        },
      },
      {
        match: "/jobs?limit=300",
        method: "GET",
        body: {
          items: [
            { id: "j1", state: "PENDING" },
            { id: "j2", state: "APPROVAL_REQUIRED" },
            { id: "j3", state: "RUNNING" },
            { id: "j4", state: "SUCCEEDED" },
            { id: "j5", state: "DENIED" },
            { id: "j6", state: "OUTPUT_QUARANTINED" },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => usePipelineMetrics());
    await hook.waitFor(() => {
      expect(hook.result.current?.source).toBe("jobs_fallback");
    });

    expect(hook.result.current?.data).toMatchObject({
      pending: 2,
      dispatched: 0,
      running: 1,
      succeeded: 1,
      failed: 2,
    });
    hook.unmount();
  });

  it("usePipelineMetrics prefers gateway pipeline when available", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/status",
        method: "GET",
        body: {
          pipeline: {
            pending: 3,
            dispatched: 1,
            running: 2,
            succeeded: 8,
            failed: 5,
          },
        },
      },
    ]);

    const hook = renderWithQueryClient(() => usePipelineMetrics());
    await hook.waitFor(() => {
      expect(hook.result.current?.source).toBe("gateway");
    });

    expect(hook.result.current?.data).toMatchObject({
      pending: 3,
      dispatched: 1,
      running: 2,
      succeeded: 8,
      failed: 5,
    });
    expect(fetchSpy.mock.calls.some(([url]) => String(url).includes("/jobs?limit=300"))).toBe(false);
    hook.unmount();
  });

  it("usePipelineMetrics uses /jobs fallback when gateway pipeline is zeroed", async () => {
    mockFetch([
      {
        match: "/status",
        method: "GET",
        body: {
          pipeline: {
            pending: 0,
            dispatched: 0,
            running: 0,
            succeeded: 0,
            failed: 0,
          },
        },
      },
      {
        match: "/jobs?limit=300",
        method: "GET",
        body: {
          items: [
            { id: "j1", state: "PENDING" },
            { id: "j2", state: "RUNNING" },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => usePipelineMetrics());
    await hook.waitFor(() => {
      expect(hook.result.current?.source).toBe("jobs_fallback");
    });

    expect(hook.result.current?.data).toMatchObject({
      pending: 1,
      running: 1,
    });
    hook.unmount();
  });

  it("useWorkersSummary maps worker heartbeats and filters invalid rows", async () => {
    mockFetch([
      {
        match: "/workers",
        method: "GET",
        body: [
          {
            worker_id: "w1",
            pool: "default",
            capabilities: ["cap.a"],
            active_jobs: 1,
            max_parallel_jobs: 5,
            region: "us-east",
          },
          {
            pool: "missing-id",
          },
        ],
      },
    ]);

    const hook = renderWithQueryClient(() => useWorkersSummary());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.items).toHaveLength(1);
    expect(hook.result.current?.data?.items[0]).toMatchObject({ id: "w1", activeJobs: 1, capacity: 5 });
    hook.unmount();
  });

  it("useRecentJobs fetches default limit=10 and maps jobs", async () => {
    mockFetch([
      {
        match: "/jobs?limit=10",
        method: "GET",
        body: {
          items: [
            {
              id: "j1",
              state: "RUNNING",
              topic: "sys.job.submit",
              updated_at: 1707000000000000,
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useRecentJobs());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.items[0]).toMatchObject({ id: "j1", status: "running" });
    hook.unmount();
  });

  it("useRecentJobs supports custom limit", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/jobs?limit=5",
        method: "GET",
        body: { items: [] },
      },
    ]);

    const hook = renderWithQueryClient(() => useRecentJobs(5));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    hook.unmount();
  });

  it("useRecentRuns fetches default limit=10 and maps workflow runs", async () => {
    mockFetch([
      {
        match: "/workflow-runs?limit=10",
        method: "GET",
        body: {
          items: [
            {
              id: "run-1",
              workflow_id: "wf-1",
              status: "RUNNING",
              started_at: "2026-02-13T09:00:00.000Z",
              steps: {
                "step-1": { step_id: "step-1", status: "running" },
              },
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useRecentRuns());
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.items[0]).toMatchObject({ id: "run-1", workflowId: "wf-1" });
    hook.unmount();
  });

  it("pipelineFromJobs maps denied and quarantined states into failed bucket", () => {
    const pipeline = __statusInternal.pipelineFromJobs([
      { id: "a", state: "DENIED" },
      { id: "b", state: "OUTPUT_QUARANTINED" },
      { id: "c", state: "CANCELLED" },
    ]);
    expect(pipeline.failed).toBe(3);
  });

  it("pipelineTotal sums all pipeline counters", () => {
    expect(
      __statusInternal.pipelineTotal({
        pending: 1,
        dispatched: 2,
        running: 3,
        succeeded: 4,
        failed: 5,
      }),
    ).toBe(15);
  });
});
