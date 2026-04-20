import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createTestQueryClient,
  mockFetch,
  renderWithQueryClient,
} from "./__tests__/test-utils";
import {
  useGovernanceDecisions,
  useGovernanceDecisionsFlat,
} from "./useGovernanceDecisions";

const { mockConfigState, loggerMock } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
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

describe("useGovernanceDecisions", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000456",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns one page of governance decisions", async () => {
    mockFetch([
      {
        match: "/governance/decisions",
        method: "GET",
        body: {
          items: [
            {
              job_id: "job-1",
              topic: "jobs.review",
              matched_rule: "rule-1",
              verdict: "ALLOW",
              reason: "approved",
              agent_id: "agent-1",
              timestamp: "2026-04-20T09:00:00.000Z",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useGovernanceDecisions({ jobId: "job-1" }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.pages).toEqual([
      {
        items: [
          {
            jobId: "job-1",
            topic: "jobs.review",
            matchedRule: "rule-1",
            verdict: "allow",
            reason: "approved",
            agentId: "agent-1",
            timestamp: "2026-04-20T09:00:00.000Z",
          },
        ],
      },
    ]);

    hook.unmount();
  });

  it("fetches the next page when nextCursor is present", async () => {
    mockFetch([
      {
        match: (url) => {
          const parsed = new URL(url, "http://localhost");
          return (
            parsed.pathname.endsWith("/governance/decisions") &&
            !parsed.searchParams.get("cursor")
          );
        },
        method: "GET",
        body: {
          items: [
            {
              job_id: "job-1",
              topic: "jobs.review",
              matched_rule: "rule-1",
              verdict: "DENY",
              reason: "blocked",
              agent_id: "agent-1",
              timestamp: "2026-04-20T09:02:00.000Z",
            },
          ],
          nextCursor: "cursor-2",
        },
      },
      {
        match: (url) => {
          const parsed = new URL(url, "http://localhost");
          return parsed.searchParams.get("cursor") === "cursor-2";
        },
        method: "GET",
        body: {
          items: [
            {
              job_id: "job-1",
              topic: "jobs.review",
              matched_rule: "rule-2",
              verdict: "THROTTLE",
              reason: "rate limited",
              agent_id: "agent-1",
              timestamp: "2026-04-20T09:03:00.000Z",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useGovernanceDecisions({ jobId: "job-1" }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.data?.pages).toHaveLength(1);
    });

    await act(async () => {
      await hook.result.current?.fetchNextPage();
    });

    await hook.waitFor(() => {
      expect(hook.result.current?.data?.pages).toHaveLength(2);
    });

    expect(hook.result.current?.data?.pages[1]?.items[0]?.matchedRule).toBe(
      "rule-2",
    );

    hook.unmount();
  });

  it("surfaces errors from the governance decisions endpoint", async () => {
    mockFetch([
      {
        match: "/governance/decisions",
        method: "GET",
        status: 500,
        body: { error: "backend unavailable" },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useGovernanceDecisions({ jobId: "job-1" }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });

    expect(hook.result.current?.error).toBeInstanceOf(Error);
    expect(hook.result.current?.error?.message).toContain("backend unavailable");

    hook.unmount();
  });

  it("returns an empty result set when no governance decisions exist", async () => {
    mockFetch([
      {
        match: "/governance/decisions",
        method: "GET",
        body: { items: [] },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useGovernanceDecisions({ jobId: "job-1" }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.pages[0]?.items).toEqual([]);

    hook.unmount();
  });

  it("serializes governance filter params onto the request", async () => {
    let capturedUrl = "";
    mockFetch([
      {
        match: (url) => {
          capturedUrl = url;
          return url.includes("/governance/decisions");
        },
        method: "GET",
        body: { items: [] },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useGovernanceDecisions({
        jobId: "job-1",
        runId: "run-9",
        limit: 25,
        filters: {
          verdict: "require_approval",
          ruleId: "rule-44",
          agentId: "agent-7",
          since: "2026-04-20T00:00:00.000Z",
          until: "2026-04-20T12:00:00.000Z",
        },
      }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const parsed = new URL(capturedUrl, "http://localhost");
    expect(parsed.searchParams.get("job_id")).toBe("job-1");
    expect(parsed.searchParams.get("run_id")).toBe("run-9");
    expect(parsed.searchParams.get("verdict")).toBe("require_approval");
    expect(parsed.searchParams.get("rule_id")).toBe("rule-44");
    expect(parsed.searchParams.get("agent_id")).toBe("agent-7");
    expect(parsed.searchParams.get("since")).toBe("2026-04-20T00:00:00.000Z");
    expect(parsed.searchParams.get("until")).toBe("2026-04-20T12:00:00.000Z");
    expect(parsed.searchParams.get("limit")).toBe("25");

    hook.unmount();
  });

  it("flattens and sorts governance decisions chronologically", async () => {
    mockFetch([
      {
        match: "/governance/decisions",
        method: "GET",
        body: {
          items: [
            {
              job_id: "job-1",
              topic: "jobs.review",
              matched_rule: "rule-late",
              verdict: "DENY",
              reason: "late",
              agent_id: "agent-1",
              timestamp: "2026-04-20T10:00:00.000Z",
            },
            {
              job_id: "job-1",
              topic: "jobs.review",
              matched_rule: "rule-early",
              verdict: "ALLOW",
              reason: "early",
              agent_id: "agent-1",
              timestamp: "2026-04-20T09:00:00.000Z",
            },
          ],
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(
      () => useGovernanceDecisionsFlat({ jobId: "job-1" }),
      queryClient,
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.items).toHaveLength(2);
    });

    expect(hook.result.current?.items.map((item) => item.matchedRule)).toEqual([
      "rule-early",
      "rule-late",
    ]);

    hook.unmount();
  });
});
