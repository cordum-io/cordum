import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi, afterEach } from "vitest";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    datasets: {
      data: { pages: [{ items: [] as unknown[] }] },
      isLoading: false,
      isError: false,
      error: null as Error | null,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
    },
  },
}));

vi.mock("@/hooks/useEvals", () => ({
  useEvalDatasets: () => hookState.datasets,
}));

vi.mock("@/components/evals/DatasetList", () => ({
  DatasetList: () => React.createElement("div", { "data-testid": "mock-list" }),
}));

vi.mock("@/components/evals/IncidentExtractionDialog", () => ({
  IncidentExtractionDialog: ({ open }: { open: boolean }) =>
    open ? React.createElement("div", { "data-testid": "mock-dialog" }) : null,
}));

import EvalsPage from "./EvalsPage";

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <EvalsPage />
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

describe("EvalsPage", () => {
  beforeEach(() => {
    hookState.datasets.data = { pages: [{ items: [] }] };
    hookState.datasets.isLoading = false;
    hookState.datasets.isError = false;
    hookState.datasets.error = null;
    hookState.datasets.hasNextPage = false;
    hookState.datasets.isFetchingNextPage = false;
    vi.clearAllMocks();
  });

  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("shows empty state when there are no datasets", () => {
    const { container, cleanup } = renderPage();
    expect(container.textContent).toContain("No eval datasets yet");
    expect(container.textContent).toContain("Create dataset from incidents");
    cleanup();
  });

  it("renders DatasetList when datasets are present", () => {
    hookState.datasets.data = {
      pages: [
        {
          items: [
            {
              id: "ds-1",
              name: "denies",
              version: 1,
              tenant: "acme",
              entryCount: 1,
              contentHash: "sha",
              createdAt: "",
              updatedAt: "",
            },
          ],
        },
      ],
    };
    const { container, cleanup } = renderPage();
    expect(container.querySelector('[data-testid="mock-list"]')).toBeTruthy();
    cleanup();
  });

  it("renders loading skeletons while datasets load", () => {
    hookState.datasets.isLoading = true;
    hookState.datasets.data = { pages: [] };
    const { container, cleanup } = renderPage();
    // Skeleton renders divs with animation classes; just assert the empty-state absence.
    expect(container.textContent).not.toContain("No eval datasets yet");
    cleanup();
  });

  it("renders error banner when datasets fail to load", () => {
    hookState.datasets.isError = true;
    hookState.datasets.error = new Error("boom");
    hookState.datasets.data = { pages: [] };
    const { container, cleanup } = renderPage();
    expect(container.textContent).toContain("Could not load eval datasets");
    cleanup();
  });

  it("opens the extraction dialog when CTA clicked on empty state", () => {
    const { container, cleanup } = renderPage();
    const buttons = Array.from(container.querySelectorAll("button"));
    const cta = buttons.find((b) => /Create dataset from incidents/i.test(b.textContent ?? ""));
    expect(cta).toBeTruthy();
    act(() => {
      cta!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(container.querySelector('[data-testid="mock-dialog"]')).toBeTruthy();
    cleanup();
  });
});
