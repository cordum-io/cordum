import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useWorker, useWorkerEvents, useWorkerJobs, useWorkers } from "./useWorkers";

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

describe("useWorkers hooks", () => {
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

  it("useWorkers fetches, maps, filters invalid entries, and polls every 15s", async () => {
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
            max_parallel_jobs: 4,
          },
          {
            pool: "invalid",
          },
        ],
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useWorkers(), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data).toHaveLength(1);
    expect(hook.result.current?.data?.[0]).toMatchObject({ id: "w1", capacity: 4, activeJobs: 1 });

    const query = queryClient.getQueryCache().find({ queryKey: ["workers"] });
    const options = query?.options as { refetchInterval?: number } | undefined;
    expect(options?.refetchInterval).toBe(15_000);

    hook.unmount();
  });

  it("useWorker finds worker by id and errors when missing", async () => {
    mockFetch([
      {
        match: "/workers",
        method: "GET",
        body: [
          {
            worker_id: "w2",
            pool: "default",
            capabilities: ["cap.b"],
            active_jobs: 0,
            max_parallel_jobs: 2,
          },
        ],
      },
    ]);

    const found = renderWithQueryClient(() => useWorker("w2"));
    await found.waitFor(() => {
      expect(found.result.current?.isSuccess).toBe(true);
    });
    expect(found.result.current?.data).toMatchObject({ id: "w2" });
    found.unmount();

    mockFetch([
      {
        match: "/workers",
        method: "GET",
        body: [
          {
            worker_id: "other",
            pool: "default",
          },
        ],
      },
    ]);

    const missing = renderWithQueryClient(() => useWorker("missing"));
    await missing.waitFor(() => {
      expect(missing.result.current?.isError).toBe(true);
    });
    expect((missing.result.current?.error as Error).message).toBe("worker not found");
    missing.unmount();
  });

  it("useWorkerJobs fetches /jobs?limit=20 and maps job records", async () => {
    mockFetch([
      {
        match: "/jobs?limit=20",
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

    const hook = renderWithQueryClient(() => useWorkerJobs("w1"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.[0]).toMatchObject({ id: "j1", status: "running" });
    hook.unmount();
  });

  it("useWorkerEvents invalidates workers cache on worker.* events", async () => {
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useWorkerEvents(), queryClient);

    window.dispatchEvent(new CustomEvent("cordum:event", { detail: { type: "job.result" } }));
    expect(invalidateSpy).not.toHaveBeenCalled();

    window.dispatchEvent(new CustomEvent("cordum:event", { detail: { type: "worker.heartbeat" } }));
    await hook.waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["workers"] });
    });

    hook.unmount();
  });
});

