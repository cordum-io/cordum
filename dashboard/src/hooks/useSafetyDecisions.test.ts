import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { useSafetyDecisions, __safetyDecisionInternal } from "./useSafetyDecisions";
import { useEventStore } from "../state/events";

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

describe("useSafetyDecisions", () => {
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
    useEventStore.setState({
      status: "disconnected",
      events: [],
      safetyDecisions: [],
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("hydrates safety decisions from recent jobs", async () => {
    mockFetch([
      {
        match: "/jobs?limit=100",
        method: "GET",
        body: {
          items: [
            {
              id: "job-1",
              topic: "job.default",
              state: "DENIED",
              updated_at: 1707000000000000,
              safety_decision: "DENY",
              safety_reason: "blocked by policy",
              safety_rule_id: "rule-secret",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useSafetyDecisions());
    await hook.waitFor(() => {
      expect(hook.result.current?.decisions).toHaveLength(1);
    });

    expect(hook.result.current?.decisions[0]).toMatchObject({
      topic: "job.default",
      decision: "deny",
      matchedRule: "rule-secret",
    });
    hook.unmount();
  });

  it("merges live stream decisions and deduplicates equivalent entries", async () => {
    useEventStore.setState({
      safetyDecisions: [
        {
          id: "live-1",
          timestamp: "2026-02-13T21:00:00.000Z",
          topic: "job.default",
          decision: "deny",
          matchedRule: "rule-secret",
        },
      ],
    });

    mockFetch([
      {
        match: "/jobs?limit=100",
        method: "GET",
        body: {
          items: [
            {
              id: "job-1",
              topic: "job.default",
              state: "DENIED",
              updated_at: 1771016400000000,
              safety_decision: "DENY",
              safety_rule_id: "rule-secret",
            },
            {
              id: "job-2",
              topic: "job.another",
              state: "SUCCEEDED",
              updated_at: 1771016300000000,
              safety_decision: "ALLOW",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useSafetyDecisions());
    await hook.waitFor(() => {
      expect(hook.result.current?.decisions).toHaveLength(2);
    });

    const ids = hook.result.current?.decisions.map((d) => d.id) ?? [];
    expect(ids).toContain("live-1");
    expect(hook.result.current?.decisions[0].topic).toBe("job.default");
    hook.unmount();
  });

  it("sets periodic refresh interval for near-live updates", async () => {
    mockFetch([
      {
        match: "/jobs?limit=100",
        method: "GET",
        body: { items: [] },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(() => useSafetyDecisions(), queryClient);
    await hook.waitFor(() => {
      expect(hook.result.current?.isLoading).toBe(false);
    });

    const query = queryClient.getQueryCache().find({ queryKey: ["jobs", "safety-decisions", 100] });
    const options = query?.options as { refetchInterval?: number } | undefined;
    expect(options?.refetchInterval).toBe(__safetyDecisionInternal.REFRESH_INTERVAL_MS);
    hook.unmount();
  });
});
