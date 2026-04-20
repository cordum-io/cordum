import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ShadowComparisonsResponse } from "@/api/types";

const { comparisonsState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    comparisonsState: {
      data: { pages: [{ entries: [] }] } as { pages: ShadowComparisonsResponse[] },
      isLoading: false,
      isError: false,
      error: null as Error | null,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
    },
  };
});

vi.mock("@/hooks/useShadowPolicy", () => ({
  useShadowResultsComparisons: () => comparisonsState,
}));

import { ShadowComparisonsTable } from "./ShadowComparisonsTable";

function render() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <MemoryRouter>
        <ShadowComparisonsTable bundleID="secops/b" />
      </MemoryRouter>,
    );
  });
  return {
    container,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("ShadowComparisonsTable", () => {
  beforeEach(() => {
    comparisonsState.data = { pages: [{ entries: [] }] };
    comparisonsState.isLoading = false;
    comparisonsState.isError = false;
    comparisonsState.hasNextPage = false;
    comparisonsState.isFetchingNextPage = false;
  });

  it("renders empty state when there are no entries", () => {
    const { container, unmount } = render();
    expect(container.textContent).toContain("No comparisons in range");
    unmount();
  });

  it("renders rows with diff badges", () => {
    comparisonsState.data = {
      pages: [
        {
          entries: [
            {
              ts_ms: Date.now(),
              job_id: "job-1",
              agent_id: "agent-1",
              active_verdict: "allow",
              shadow_verdict: "deny",
              diff: "escalated",
              active_rule_id: "r-active",
              shadow_rule_id: "r-shadow",
              seq: 1,
            },
            {
              ts_ms: Date.now(),
              job_id: "job-2",
              agent_id: "agent-2",
              active_verdict: "deny",
              shadow_verdict: "allow",
              diff: "relaxed",
              seq: 2,
            },
          ],
        },
      ],
    };

    const { container, unmount } = render();
    expect(container.textContent).toContain("job-1");
    expect(container.textContent).toContain("job-2");
    expect(container.textContent?.toLowerCase()).toContain("escalated");
    expect(container.textContent?.toLowerCase()).toContain("relaxed");
    unmount();
  });

  it("exposes diff filter chips", () => {
    const { container, unmount } = render();
    const radios = Array.from(container.querySelectorAll("[role=radio]"));
    const labels = radios.map((r) => r.textContent?.trim());
    expect(labels).toContain("All");
    expect(labels).toContain("Escalated");
    expect(labels).toContain("Relaxed");
    expect(labels).toContain("Approval differ");
    expect(labels).toContain("Unchanged");
    unmount();
  });

  it("surfaces errors with retry", () => {
    comparisonsState.isError = true;
    comparisonsState.error = new Error("xrange denied");

    const { container, unmount } = render();
    expect(container.textContent).toContain("xrange denied");
    unmount();
  });

  it("shows load-more when hasNextPage", () => {
    comparisonsState.data = {
      pages: [
        {
          entries: [
            {
              ts_ms: Date.now(),
              job_id: "job-a",
              agent_id: "agent-a",
              active_verdict: "allow",
              shadow_verdict: "allow",
              diff: "unchanged",
            },
          ],
        },
      ],
    };
    comparisonsState.hasNextPage = true;

    const { container, unmount } = render();
    expect(container.textContent).toContain("Load more");
    unmount();
  });
});
