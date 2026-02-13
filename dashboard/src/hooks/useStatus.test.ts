import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useRecentJobs, useRecentRuns, useStatus, useWorkersSummary } from "./useStatus";

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
});

