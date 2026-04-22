import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";
import { createTestQueryClient } from "@/hooks/__tests__/test-utils";
import type { ApprovalAnalyticsResponse } from "@/api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    overall: {
      data: undefined as ApprovalAnalyticsResponse | undefined,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
    grouped: {
      data: undefined as ApprovalAnalyticsResponse | undefined,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
  },
}));

vi.mock("@/hooks/useApprovalAnalytics", () => ({
  useApprovalAnalytics: (args: { groupBy?: string }) => {
    if (!args.groupBy || args.groupBy === "overall") return hookState.overall;
    return hookState.grouped;
  },
}));

import { ApprovalAnalyticsWidget } from "./ApprovalAnalyticsWidget";

function fullSummary(overrides: Partial<ApprovalAnalyticsResponse["summary"]> = {}): ApprovalAnalyticsResponse {
  return {
    window: { since: "2026-04-20T00:00:00Z", until: "2026-04-21T00:00:00Z" },
    summary: {
      total: 42,
      approved: 30,
      rejected: 8,
      expired: 4,
      autoResolved: 4,
      manualResolved: 38,
      avgTimeToApproveSeconds: 180,
      p50: 120,
      p90: 420,
      p99: 900,
      ...overrides,
    },
  };
}

function groupedData(): ApprovalAnalyticsResponse {
  return {
    window: { since: "2026-04-20T00:00:00Z", until: "2026-04-21T00:00:00Z" },
    summary: fullSummary().summary,
    groups: [
      {
        key: "rule-slow",
        label: "rule-slow",
        total: 12,
        approved: 10,
        rejected: 1,
        expired: 1,
        autoCount: 1,
        manualCount: 11,
        avgTtarSeconds: 1200,
        p90Seconds: 1800,
      },
      {
        key: "rule-fast",
        label: "rule-fast",
        total: 20,
        approved: 20,
        rejected: 0,
        expired: 0,
        autoCount: 0,
        manualCount: 20,
        avgTtarSeconds: 30,
        p90Seconds: 60,
      },
    ],
  };
}

function renderWidget() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = createTestQueryClient();
  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <ApprovalAnalyticsWidget />
      </QueryClientProvider>,
    );
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("ApprovalAnalyticsWidget", () => {
  beforeEach(() => {
    hookState.overall.data = undefined;
    hookState.overall.isLoading = false;
    hookState.overall.isError = false;
    hookState.overall.error = null;
    hookState.grouped.data = undefined;
    hookState.grouped.isLoading = false;
    hookState.grouped.isError = false;
    hookState.grouped.error = null;
    vi.clearAllMocks();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders loading skeletons while data is in flight", () => {
    hookState.overall.isLoading = true;
    hookState.grouped.isLoading = true;
    const { container, cleanup } = renderWidget();
    expect(container.textContent).toContain("Approval analytics");
    // No StatTile values yet.
    expect(container.textContent).not.toContain("Total approvals");
    cleanup();
  });

  it("renders empty state when summary.total === 0", () => {
    hookState.overall.data = fullSummary({ total: 0, approved: 0, rejected: 0, expired: 0, autoResolved: 0, manualResolved: 0, avgTimeToApproveSeconds: null, p50: null, p90: null, p99: null });
    const { container, cleanup } = renderWidget();
    expect(container.textContent).toContain("No approvals in this window");
    cleanup();
  });

  it("renders error banner when either query fails", () => {
    hookState.overall.isError = true;
    hookState.overall.error = new Error("boom");
    const { container, cleanup } = renderWidget();
    expect(container.textContent).toContain("Approval analytics unavailable");
    cleanup();
  });

  it("renders approval-rate and breakdown metrics when data loads", () => {
    hookState.overall.data = fullSummary();
    hookState.grouped.data = groupedData();
    const { container, cleanup } = renderWidget();
    expect(container.textContent).toContain("Total approvals");
    expect(container.textContent).toContain("42");
    expect(container.textContent).toContain("Approval rate");
    expect(container.textContent).toContain("71%");
    expect(container.textContent).toContain("Avg time to approve");
    expect(container.textContent).toContain("Auto vs manual");
    expect(container.textContent).toContain("p90 latency");
    expect(container.textContent).toContain("rule-slow");
    cleanup();
  });

  it("flags the slow rule as a bottleneck", () => {
    hookState.overall.data = fullSummary(); // avg 180s
    hookState.grouped.data = groupedData(); // rule-slow at 1200s (>2x avg AND >900s floor)
    const { container, cleanup } = renderWidget();
    const badges = container.querySelectorAll('[role="status"][aria-label="bottleneck"]');
    expect(badges.length).toBeGreaterThan(0);
    cleanup();
  });

  it("does not flag the fast rule as a bottleneck", () => {
    hookState.overall.data = fullSummary();
    hookState.grouped.data = {
      ...groupedData(),
      groups: [
        {
          key: "rule-fast",
          label: "rule-fast",
          total: 20,
          approved: 20,
          rejected: 0,
          expired: 0,
          autoCount: 0,
          manualCount: 20,
          avgTtarSeconds: 30,
          p90Seconds: 60,
        },
      ],
    };
    const { container, cleanup } = renderWidget();
    const badges = container.querySelectorAll('[role="status"][aria-label="bottleneck"]');
    expect(badges.length).toBe(0);
    cleanup();
  });

  it("renders per-group approval-rate column in the bottleneck table", () => {
    hookState.overall.data = fullSummary();
    hookState.grouped.data = groupedData();
    const { container, cleanup } = renderWidget();
    // rule-slow: 10/12 ≈ 83%; rule-fast: 20/20 = 100%
    const labels = Array.from(container.querySelectorAll('[aria-label^="approval rate"]')).map(
      (el) => el.getAttribute("aria-label"),
    );
    expect(labels).toContain("approval rate 83%");
    expect(labels).toContain("approval rate 100%");
    cleanup();
  });

  it("renders approval-rate as — when a group has zero total", () => {
    hookState.overall.data = fullSummary();
    hookState.grouped.data = {
      ...groupedData(),
      groups: [
        {
          key: "rule-empty",
          label: "rule-empty",
          total: 0,
          approved: 0,
          rejected: 0,
          expired: 0,
          autoCount: 0,
          manualCount: 0,
          avgTtarSeconds: null,
          p90Seconds: null,
        },
      ],
    };
    const { container, cleanup } = renderWidget();
    const labels = Array.from(container.querySelectorAll('[aria-label^="approval rate"]')).map(
      (el) => el.getAttribute("aria-label"),
    );
    expect(labels).toContain("approval rate —");
    cleanup();
  });

  it("renders breakdown tabs by rule/agent/topic with role=tablist", () => {
    hookState.overall.data = fullSummary();
    hookState.grouped.data = groupedData();
    const { container, cleanup } = renderWidget();
    const tablists = container.querySelectorAll('[role="tablist"]');
    // One tablist for the window selector + one for the breakdown tabs.
    expect(tablists.length).toBeGreaterThanOrEqual(2);
    const labels = Array.from(container.querySelectorAll("button[role='tab']")).map((b) => b.textContent);
    expect(labels.some((l) => l?.includes("By rule"))).toBe(true);
    expect(labels.some((l) => l?.includes("By agent"))).toBe(true);
    expect(labels.some((l) => l?.includes("By topic"))).toBe(true);
    cleanup();
  });
});
