import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { summaryState, timeseriesState, comparisonsState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    summaryState: {
      data: undefined as unknown,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
    timeseriesState: {
      data: undefined as unknown,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
    comparisonsState: {
      data: { pages: [{ entries: [] }] } as unknown,
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
  useShadowResultsSummary: () => summaryState,
  useShadowResultsTimeseries: () => timeseriesState,
  useShadowResultsComparisons: () => comparisonsState,
  useShadowPolicy: () => ({ data: null, isLoading: false, isError: false }),
  useActivateShadow: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeactivateShadow: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));

import { ShadowImpactPanel } from "./ShadowImpactPanel";

function render() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <MemoryRouter>
        <ShadowImpactPanel bundleID="secops/b" />
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

describe("ShadowImpactPanel", () => {
  beforeEach(() => {
    summaryState.data = undefined;
    summaryState.isLoading = false;
    summaryState.isError = false;
    summaryState.error = null;
    timeseriesState.data = undefined;
    timeseriesState.isLoading = false;
    timeseriesState.isError = false;
    timeseriesState.error = null;
    comparisonsState.data = { pages: [{ entries: [] }] };
  });

  it("renders summary with would-have-been-blocked total", () => {
    summaryState.data = {
      total_evaluated: 120,
      escalated_count: 40,
      relaxed_count: 10,
      approval_differ_count: 7,
      unchanged_count: 63,
    };
    timeseriesState.data = { buckets: [], window_ms: 86400000 };

    const { container, unmount } = render();
    const callout = container.querySelector("[data-testid=shadow-summary-callout]");
    expect(callout).not.toBeNull();
    // Would-have-blocked = escalated + approval_differ = 40 + 7 = 47
    expect(callout!.textContent).toContain("47");
    expect(callout!.textContent).toContain("of 120");
    unmount();
  });

  it("renders empty state when timeseries is empty", () => {
    summaryState.data = {
      total_evaluated: 0,
      escalated_count: 0,
      relaxed_count: 0,
      approval_differ_count: 0,
      unchanged_count: 0,
    };
    timeseriesState.data = { buckets: [], window_ms: 86400000 };

    const { container, unmount } = render();
    expect(container.textContent).toContain("No shadow evaluations yet");
    unmount();
  });

  it("surfaces summary errors", () => {
    summaryState.isError = true;
    summaryState.error = new Error("summary 500");
    timeseriesState.data = { buckets: [], window_ms: 86400000 };

    const { container, unmount } = render();
    expect(container.textContent).toContain("summary 500");
    unmount();
  });

  it("exposes a time-window radio group", () => {
    summaryState.data = {
      total_evaluated: 0,
      escalated_count: 0,
      relaxed_count: 0,
      approval_differ_count: 0,
      unchanged_count: 0,
    };
    timeseriesState.data = { buckets: [], window_ms: 0 };

    const { container, unmount } = render();
    const radios = container.querySelectorAll("[role=radio]");
    const labels = Array.from(radios).map((r) => r.textContent);
    expect(labels).toContain("1h");
    expect(labels).toContain("24h");
    expect(labels).toContain("7d");
    expect(labels).toContain("30d");
    unmount();
  });
});
