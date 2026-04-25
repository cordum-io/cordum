import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi, afterEach } from "vitest";
import { DatasetList } from "./DatasetList";
import type { EvalDataset, EvalRun } from "@/api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function ds(overrides: Partial<EvalDataset> = {}): EvalDataset {
  return {
    id: "ds-1",
    name: "denies",
    version: 1,
    tenant: "acme",
    entryCount: 10,
    contentHash: "sha",
    createdAt: "2026-04-19T00:00:00Z",
    updatedAt: "2026-04-19T00:00:00Z",
    ...overrides,
  };
}

function run(score: number | null, regressions = 0): EvalRun {
  return {
    runId: "r-1",
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

function renderList(props: Parameters<typeof DatasetList>[0]) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <MemoryRouter>
        <DatasetList {...props} />
      </MemoryRouter>,
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

describe("DatasetList", () => {
  afterEach(() => {
    document.body.innerHTML = "";
  });

  it("renders datasets with score badges", () => {
    const { container, cleanup } = renderList({
      datasets: [ds({ id: "ds-a", name: "high" }), ds({ id: "ds-b", name: "med" }), ds({ id: "ds-c", name: "low" })],
      latestRunsByDatasetId: {
        "ds-a": run(98),
        "ds-b": run(85),
        "ds-c": run(40),
      },
      hasNextPage: false,
      onLoadMore: vi.fn(),
      onCreateFromIncidents: vi.fn(),
    });

    expect(container.textContent).toContain("high");
    expect(container.textContent).toContain("98%");
    expect(container.textContent).toContain("85%");
    expect(container.textContent).toContain("40%");
    cleanup();
  });

  it("shows regression dot when latest run has regressions", () => {
    const { container, cleanup } = renderList({
      datasets: [ds()],
      latestRunsByDatasetId: { "ds-1": run(70, 3) },
      hasNextPage: false,
      onLoadMore: vi.fn(),
      onCreateFromIncidents: vi.fn(),
    });
    const dot = container.querySelector('[aria-label*="regression"]');
    expect(dot).toBeTruthy();
    cleanup();
  });

  it("fires onCreateFromIncidents when the primary CTA is clicked", () => {
    const cb = vi.fn();
    const { container, cleanup } = renderList({
      datasets: [ds()],
      latestRunsByDatasetId: {},
      hasNextPage: false,
      onLoadMore: vi.fn(),
      onCreateFromIncidents: cb,
    });
    const buttons = Array.from(container.querySelectorAll("button"));
    const cta = buttons.find((b) => /Create from incidents/i.test(b.textContent ?? ""));
    expect(cta).toBeTruthy();
    act(() => {
      cta!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(cb).toHaveBeenCalledOnce();
    cleanup();
  });

  it("disables the Upload dataset button with Coming soon tooltip", () => {
    const { container, cleanup } = renderList({
      datasets: [ds()],
      hasNextPage: false,
      onLoadMore: vi.fn(),
      onCreateFromIncidents: vi.fn(),
    });
    const buttons = Array.from(container.querySelectorAll("button"));
    const upload = buttons.find((b) => /Upload dataset/i.test(b.textContent ?? ""));
    expect(upload).toBeTruthy();
    expect(upload!.disabled).toBe(true);
    expect(upload!.getAttribute("title")).toBe("Coming soon");
    cleanup();
  });

  it("renders a Load more button when hasNextPage is true", () => {
    const cb = vi.fn();
    const { container, cleanup } = renderList({
      datasets: [ds()],
      hasNextPage: true,
      onLoadMore: cb,
      onCreateFromIncidents: vi.fn(),
    });
    const buttons = Array.from(container.querySelectorAll("button"));
    const loadMore = buttons.find((b) => /Load more/i.test(b.textContent ?? ""));
    expect(loadMore).toBeTruthy();
    act(() => {
      loadMore!.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });
    expect(cb).toHaveBeenCalledOnce();
    cleanup();
  });
});
