import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createTestQueryClient,
  mockFetch,
  renderWithQueryClient,
} from "./__tests__/test-utils";
import {
  __approvalAnalyticsInternal,
  useApprovalAnalytics,
} from "./useApprovalAnalytics";

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

vi.mock("../lib/logger", () => ({ logger: loggerMock }));

describe("useApprovalAnalytics", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000001",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("builds the query string from window + group_by + limit", () => {
    const qs = __approvalAnalyticsInternal.buildQuery({
      window: "7d",
      groupBy: "rule",
      limit: 25,
    });
    expect(qs).toContain("window=7d");
    expect(qs).toContain("group_by=rule");
    expect(qs).toContain("limit=25");
  });

  it("clamps limit to the 1..50 range", () => {
    const over = __approvalAnalyticsInternal.buildQuery({ window: "24h", limit: 999 });
    expect(over).toContain("limit=50");
    const under = __approvalAnalyticsInternal.buildQuery({ window: "24h", limit: 0 });
    expect(under).toContain("limit=1");
  });

  it("fetches + maps a successful response", async () => {
    mockFetch([
      {
        match: "/governance/approvals/analytics",
        method: "GET",
        body: {
          window: { since: "2026-04-20T00:00:00Z", until: "2026-04-21T00:00:00Z" },
          summary: {
            total: 12,
            approved: 9,
            rejected: 2,
            expired: 1,
            auto_resolved: 1,
            manual_resolved: 11,
            avg_time_to_approve_seconds: 125,
            p50: 60,
            p90: 300,
            p99: 900,
          },
          groups: [
            {
              key: "rule-fast",
              total: 5,
              approved: 5,
              auto_count: 0,
              manual_count: 5,
              avg_ttar_seconds: 45,
              p90_seconds: 60,
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useApprovalAnalytics({ window: "24h" }),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    const data = hook.result.current?.data;
    expect(data?.summary.total).toBe(12);
    expect(data?.summary.avgTimeToApproveSeconds).toBe(125);
    expect(data?.groups?.[0]?.label).toBe("rule-fast");
  });

  it("maps null-percentile responses without coercing to zero", async () => {
    mockFetch([
      {
        match: "/governance/approvals/analytics",
        method: "GET",
        body: {
          window: { since: "x", until: "y" },
          summary: {
            total: 0,
            approved: 0,
            rejected: 0,
            expired: 0,
            auto_resolved: 0,
            manual_resolved: 0,
            avg_time_to_approve_seconds: null,
            p50: null,
            p90: null,
            p99: null,
          },
        },
      },
    ]);
    const hook = renderWithQueryClient(() =>
      useApprovalAnalytics({ window: "24h" }),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    const s = hook.result.current?.data?.summary;
    // Load-bearing: nulls must remain nulls, not 0 s.
    expect(s?.avgTimeToApproveSeconds).toBeNull();
    expect(s?.p50).toBeNull();
    expect(s?.p90).toBeNull();
    expect(s?.p99).toBeNull();
  });

  it("propagates fetch errors to the hook state", async () => {
    mockFetch([
      {
        match: "/governance/approvals/analytics",
        method: "GET",
        status: 503,
        body: { error: "decision log store unavailable" },
      },
    ]);
    const qc = createTestQueryClient();
    const hook = renderWithQueryClient(
      () => useApprovalAnalytics({ window: "24h" }),
      qc,
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
  });
});
