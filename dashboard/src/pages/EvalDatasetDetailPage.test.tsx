import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    dataset: {
      data: undefined as unknown,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
    runs: {
      data: { pages: [{ items: [] as unknown[] }] },
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    },
    runMutation: { mutate: vi.fn(), isPending: false },
  },
}));

vi.mock("@/hooks/useEvals", () => ({
  useEvalDataset: () => hookState.dataset,
  useEvalRuns: () => hookState.runs,
  useRunEvalDataset: () => hookState.runMutation,
}));

vi.mock("recharts", () => ({
  CartesianGrid: () => null,
  Dot: () => null,
  Line: () => null,
  LineChart: ({ children }: { children?: React.ReactNode }) =>
    React.createElement("div", { "data-testid": "line-chart" }, children),
  ResponsiveContainer: ({ children }: { children?: React.ReactNode }) =>
    React.createElement("div", null, children),
  Tooltip: () => null,
  XAxis: () => null,
  YAxis: () => null,
}));

import EvalDatasetDetailPage from "./EvalDatasetDetailPage";

function renderPage(datasetId = "ds-1") {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/evals/${datasetId}`]}>
          <Routes>
            <Route path="/evals/:datasetId" element={<EvalDatasetDetailPage />} />
          </Routes>
        </MemoryRouter>
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

function mkRun(score: number | null, regressions = 0, runId = "r-1") {
  return {
    runId,
    datasetId: "ds-1",
    datasetName: "denies",
    datasetVersion: 1,
    policySnapshot: "snap",
    startedAt: "2026-04-20T10:00:00Z",
    completedAt: "2026-04-20T10:00:05Z",
    summary: {
      total: 10,
      passed: 10 - regressions,
      failed: 0,
      regressions,
      errored: 0,
      scorePercent: score,
    },
  };
}

describe("EvalDatasetDetailPage", () => {
  beforeEach(() => {
    hookState.dataset.data = {
      id: "ds-1",
      name: "denies",
      version: 2,
      tenant: "acme",
      entryCount: 42,
      contentHash: "sha",
      createdAt: "2026-04-19T00:00:00Z",
      updatedAt: "2026-04-19T00:00:00Z",
    };
    hookState.dataset.isLoading = false;
    hookState.dataset.isError = false;
    hookState.runs.data = { pages: [{ items: [] }] };
    hookState.runs.hasNextPage = false;
    hookState.runMutation.mutate.mockReset();
    hookState.runMutation.isPending = false;
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders StatTiles with dataset summary", () => {
    const { container, cleanup } = renderPage();
    expect(container.textContent).toContain("denies");
    expect(container.textContent).toContain("42"); // entries
    cleanup();
  });

  it("shows regression banner when latest run has regressions", () => {
    hookState.runs.data = { pages: [{ items: [mkRun(40, 3)] }] };
    const { container, cleanup } = renderPage();
    expect(container.querySelector('[role="alert"]')).toBeTruthy();
    expect(container.textContent).toContain("Regression detected");
    cleanup();
  });

  it("does not show regression banner when no regressions", () => {
    hookState.runs.data = { pages: [{ items: [mkRun(100, 0)] }] };
    const { container, cleanup } = renderPage();
    expect(container.querySelector('[role="alert"]')).toBeFalsy();
    cleanup();
  });

  it("fires run mutation when 'Run against current policy' clicked", () => {
    const { container, cleanup } = renderPage();
    const btn = Array.from(container.querySelectorAll("button")).find((b) =>
      /Run against current policy/i.test(b.textContent ?? ""),
    );
    expect(btn).toBeTruthy();
    act(() => {
      btn!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(hookState.runMutation.mutate).toHaveBeenCalledWith({ useCurrentPolicy: true });
    cleanup();
  });

  it("renders ScoreTrendChart without crashing on empty data", () => {
    hookState.runs.data = { pages: [{ items: [] }] };
    const { container, cleanup } = renderPage();
    expect(container.textContent).toContain("No run history yet");
    cleanup();
  });

  it("renders chart container when runs present", () => {
    hookState.runs.data = { pages: [{ items: [mkRun(90), mkRun(80, 0, "r-2")] }] };
    const { container, cleanup } = renderPage();
    expect(container.querySelector('[data-testid="line-chart"]')).toBeTruthy();
    cleanup();
  });

  it("renders error state when dataset query fails", () => {
    hookState.dataset.data = undefined;
    hookState.dataset.isError = true;
    hookState.dataset.error = new Error("boom");
    const { container, cleanup } = renderPage();
    expect(container.textContent).toContain("Could not load dataset");
    cleanup();
  });
});
