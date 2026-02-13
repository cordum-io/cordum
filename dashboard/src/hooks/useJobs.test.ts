import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __jobsInternal,
  useCancelJob,
  useJob,
  useJobDecisions,
  useJobs,
  useRemediateJob,
  useRetryJob,
  useSubmitJob,
} from "./useJobs";

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

describe("useJobs internals", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("stateToBackend maps all known states and unknown fallback", () => {
    expect(__jobsInternal.stateToBackend("pending")).toBe("PENDING");
    expect(__jobsInternal.stateToBackend("scheduled")).toBe("SCHEDULED");
    expect(__jobsInternal.stateToBackend("dispatched")).toBe("DISPATCHED");
    expect(__jobsInternal.stateToBackend("running")).toBe("RUNNING");
    expect(__jobsInternal.stateToBackend("succeeded")).toBe("SUCCEEDED");
    expect(__jobsInternal.stateToBackend("failed")).toBe("FAILED");
    expect(__jobsInternal.stateToBackend("cancelled")).toBe("CANCELLED");
    expect(__jobsInternal.stateToBackend("approval_required")).toBe("APPROVAL_REQUIRED");
    expect(__jobsInternal.stateToBackend("denied")).toBe("DENIED");
    expect(__jobsInternal.stateToBackend("timeout")).toBe("TIMEOUT");
    expect(__jobsInternal.stateToBackend("output_quarantined")).toBe("OUTPUT_QUARANTINED");
    expect(__jobsInternal.stateToBackend("custom_state" as never)).toBe("CUSTOM_STATE");
  });

  it("rangeToMicros computes expected ranges for known values", () => {
    const nowMicros = Date.now() * 1000;
    expect(__jobsInternal.rangeToMicros("1h")).toEqual({
      after: (Date.now() - 60 * 60 * 1000) * 1000,
      before: nowMicros,
    });
    expect(__jobsInternal.rangeToMicros("24h")).toEqual({
      after: (Date.now() - 24 * 60 * 60 * 1000) * 1000,
      before: nowMicros,
    });
    expect(__jobsInternal.rangeToMicros("7d")).toEqual({
      after: (Date.now() - 7 * 24 * 60 * 60 * 1000) * 1000,
      before: nowMicros,
    });
    expect(__jobsInternal.rangeToMicros("30d")).toEqual({
      after: (Date.now() - 30 * 24 * 60 * 60 * 1000) * 1000,
      before: nowMicros,
    });
    expect(__jobsInternal.rangeToMicros("unknown")).toEqual({});
    expect(__jobsInternal.rangeToMicros(undefined)).toEqual({});
  });

  it("buildParams builds expected query string", () => {
    const query = __jobsInternal.buildParams({
      state: ["running"],
      topic: "sys.job.submit",
      tenant: "tenant-1",
      team: "team-1",
      limit: 20,
      cursor: 10,
      updatedAfter: 111,
      updatedBefore: 222,
    });
    expect(query).toBe(
      "?state=RUNNING&topic=sys.job.submit&tenant=tenant-1&team=team-1&limit=20&cursor=10&updated_after=111&updated_before=222",
    );
  });
});

describe("useJobs hooks", () => {
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

  it("useJobs fetches jobs and applies multi-state + decision filtering", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/jobs?topic=sys.job.submit",
        method: "GET",
        body: {
          items: [
            {
              id: "j1",
              state: "RUNNING",
              topic: "sys.job.submit",
              updated_at: 1_707_000_000_000_000,
              safety_decision: "ALLOW",
            },
            {
              id: "j2",
              state: "FAILED",
              topic: "sys.job.submit",
              updated_at: 1_707_000_100_000_000,
              safety_decision: "DENY",
            },
          ],
        },
      },
    ]);
    const hook = renderWithQueryClient(() =>
      useJobs({
        topic: "sys.job.submit",
        state: ["running", "failed"],
        decision: ["deny"],
      }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data?.items).toHaveLength(1);
    expect(hook.result.current?.data?.items[0].id).toBe("j2");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    hook.unmount();
  });

  it("useJob fetches /jobs/{id} and maps detail", async () => {
    mockFetch([
      {
        match: "/jobs/j99",
        method: "GET",
        body: {
          id: "j99",
          state: "RUNNING",
          topic: "sys.job.submit",
          updated_at: 1_707_000_000_000_000,
          context_ptr: "redis://ctx:j99",
          result_ptr: "redis://res:j99",
        },
      },
    ]);
    const hook = renderWithQueryClient(() => useJob("j99"));

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data).toMatchObject({
      id: "j99",
      status: "running",
      contextPtr: "redis://ctx:j99",
      resultPtr: "redis://res:j99",
    });
    hook.unmount();
  });

  it("useJobDecisions maps backend decision rows", async () => {
    mockFetch([
      {
        match: "/jobs/j1/decisions",
        method: "GET",
        body: [
          { decision: "DECISION_TYPE_ALLOW", reason: "ok", rule_id: "r1" },
          { decision: "DENY", reason: "blocked", rule_id: "r2" },
        ],
      },
    ]);
    const hook = renderWithQueryClient(() => useJobDecisions("j1"));

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.data).toEqual([
      { type: "allow", reason: "ok", matchedRule: "r1" },
      { type: "deny", reason: "blocked", matchedRule: "r2" },
    ]);
    hook.unmount();
  });

  it("useSubmitJob posts /jobs payload and invalidates jobs cache", async () => {
    const fetchSpy = mockFetch([
      {
        match: (url, init) => {
          if (!url.includes("/jobs")) return false;
          if ((init?.method ?? "GET").toUpperCase() !== "POST") return false;
          const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
          expect(body).toMatchObject({
            topic: "job.default",
            prompt: "run task",
            priority: "high",
            requires: ["code_execution"],
            risk_tags: ["external_api"],
            labels: { source: "dashboard" },
          });
          return true;
        },
        method: "POST",
        body: { job_id: "job-123", trace_id: "trace-123" },
      },
    ]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useSubmitJob(), queryClient);

    await act(async () => {
      const result = await hook.result.current?.mutateAsync({
        topic: "job.default",
        prompt: "run task",
        priority: "high",
        requires: ["code_execution"],
        risk_tags: ["external_api"],
        labels: { source: "dashboard" },
      });
      expect(result).toEqual({ job_id: "job-123", trace_id: "trace-123" });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["jobs"] });
    hook.unmount();
  });

  it("useCancelJob optimistically updates and restores on error", async () => {
    mockFetch([{ match: "/jobs/j1/cancel", method: "POST", rejectWith: new Error("cancel failed") }]);
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["jobs", {}], {
      items: [
        { id: "j1", status: "running" },
        { id: "j2", status: "pending" },
      ],
    });
    queryClient.setQueryData(["job", "j1"], { id: "j1", status: "running" });
    const hook = renderWithQueryClient(() => useCancelJob(), queryClient);

    await expect(hook.result.current?.mutateAsync("j1")).rejects.toThrow("cancel failed");
    expect(queryClient.getQueryData<{ items: Array<{ id: string; status: string }> }>(["jobs", {}])?.items).toEqual([
      { id: "j1", status: "running" },
      { id: "j2", status: "pending" },
    ]);
    expect(queryClient.getQueryData<{ id: string; status: string }>(["job", "j1"])).toEqual({
      id: "j1",
      status: "running",
    });
    hook.unmount();
  });

  it("useRetryJob posts retry endpoint and invalidates caches", async () => {
    const fetchSpy = mockFetch([{ match: "/dlq/j1/retry", method: "POST", body: {} }]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useRetryJob(), queryClient);

    await act(async () => {
      await hook.result.current?.mutateAsync("j1");
    });
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["jobs"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["job", "j1"] });
    expect(addToastMock).toHaveBeenCalledWith({ type: "success", title: "Retrying job" });
    hook.unmount();
  });

  it("useRemediateJob posts remediation payload and invalidates job caches", async () => {
    const fetchSpy = mockFetch([
      {
        match: (url, init) => {
          if (!url.includes("/jobs/j1/remediate")) return false;
          if ((init?.method ?? "GET").toUpperCase() !== "POST") return false;
          const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
          expect(body).toMatchObject({
            topic: "job.fixed",
            prompt: "retry with constrained prompt",
            reason: "fixed policy-triggering input",
            risk_tags: ["safe"],
          });
          return true;
        },
        method: "POST",
        body: { job_id: "job-remediated-1", trace_id: "trace-remediated-1" },
      },
    ]);
    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useRemediateJob(), queryClient);

    await act(async () => {
      const result = await hook.result.current?.mutateAsync({
        jobId: "j1",
        input: {
          topic: "job.fixed",
          prompt: "retry with constrained prompt",
          reason: "fixed policy-triggering input",
          risk_tags: ["safe"],
        },
      });
      expect(result).toEqual({
        job_id: "job-remediated-1",
        trace_id: "trace-remediated-1",
      });
    });

    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["jobs"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["job", "j1"] });
    hook.unmount();
  });
});

