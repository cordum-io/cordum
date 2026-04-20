import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// matchMedia must be defined before any component import
vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
});

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { Job, ApiResponse } from "../api/types";

// ---------------------------------------------------------------------------
// Mock state
// ---------------------------------------------------------------------------

const { mockJobsResult, mockNavigate, mockAddToast } = vi.hoisted(() => ({
  mockJobsResult: {
    data: undefined as ApiResponse<Job[]> | undefined,
    isLoading: false,
    isError: false,
    dataUpdatedAt: Date.now(),
    refetch: vi.fn(),
    isRefetching: false,
  },
  mockNavigate: vi.fn(),
  mockAddToast: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("react-router-dom", () => ({
  useNavigate: () => mockNavigate,
  Link: ({ children, to }: { children: React.ReactNode; to: string }) =>
    React.createElement("a", { href: to }, children),
}));

vi.mock("../hooks/useJobs", () => ({
  useJobs: () => mockJobsResult,
}));

vi.mock("../hooks/usePageTitle", () => ({
  usePageTitle: vi.fn(),
}));

vi.mock("../state/toast", () => {
  const store = { addToast: mockAddToast };
  const useToastStore = Object.assign(
    (selector: (s: typeof store) => unknown) => selector(store),
    { getState: () => store },
  );
  return { useToastStore };
});

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// Mock child components as simple stubs
vi.mock("../components/StatusBadge", () => ({
  JobStatusBadge: ({ state }: { state: string }) =>
    React.createElement("span", { "data-testid": "status-badge" }, state),
}));

vi.mock("../components/jobs/JobFiltersBar", () => ({
  JobFiltersBar: ({ onChange }: { onChange: (v: Record<string, unknown>) => void }) =>
    React.createElement(
      "div",
      { "data-testid": "filters-bar" },
      React.createElement(
        "button",
        { "data-testid": "apply-filter", onClick: () => onChange({ state: ["running"] }) },
        "Apply Filter",
      ),
    ),
}));

vi.mock("../components/jobs/JobSubmitDrawer", () => ({
  JobSubmitDrawer: ({
    open,
    onClose,
    onSuccess,
  }: {
    open: boolean;
    onClose: () => void;
    onSuccess: (result: { job_id: string }) => void;
  }) =>
    open
      ? React.createElement(
          "div",
          { "data-testid": "submit-drawer" },
          React.createElement("button", { "data-testid": "drawer-close", onClick: onClose }, "Close"),
          React.createElement(
            "button",
            { "data-testid": "drawer-submit", onClick: () => onSuccess({ job_id: "j-new-123" }) },
            "Submit",
          ),
        )
      : null,
}));

vi.mock("../components/ui/Badge", () => ({
  Badge: ({ children, variant }: { children: React.ReactNode; variant?: string }) =>
    React.createElement("span", { "data-testid": "badge", "data-variant": variant }, children),
}));

vi.mock("../components/ui/Button", () => ({
  Button: ({
    children,
    onClick,
    disabled,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
    disabled?: boolean;
  }) =>
    React.createElement("button", { onClick, disabled, "data-testid": "button" }, children),
}));

vi.mock("../components/ui/EmptyState", () => ({
  TableEmptyState: ({ title, description }: { title: string; description: string; colSpan: number; icon: unknown }) =>
    React.createElement(
      "tr",
      {},
      React.createElement("td", { "data-testid": "empty-state" }, `${title} ${description}`),
    ),
}));

vi.mock("../components/ui/Skeleton", () => ({
  SkeletonRow: ({ columns }: { columns: number }) =>
    React.createElement("tr", { "data-testid": "skeleton-row" }, React.createElement("td", { colSpan: columns }, "loading...")),
}));

vi.mock("../components/ui/DataFreshness", () => ({
  DataFreshness: ({ onRefresh }: { onRefresh: () => void; dataUpdatedAt: number; isRefetching: boolean }) =>
    React.createElement("button", { "data-testid": "refresh", onClick: onRefresh }, "Refresh"),
}));

vi.mock("../lib/utils", () => ({
  cn: (...args: unknown[]) => args.filter(Boolean).join(" "),
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let JobsPage: React.ComponentType;

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "j-test-00000001",
    type: "default",
    topic: "sys.job.submit",
    status: "succeeded",
    pool: "default-pool",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-02-13T12:00:00.000Z",
    updatedAt: "2026-02-13T12:05:00.000Z",
    ...overrides,
  };
}

function render() {
  act(() => {
    root.render(React.createElement(JobsPage));
  });
}

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  // Reset mock state
  mockJobsResult.data = undefined;
  mockJobsResult.isLoading = false;
  mockJobsResult.isError = false;
  mockJobsResult.dataUpdatedAt = Date.now();
  mockJobsResult.refetch = vi.fn();
  mockJobsResult.isRefetching = false;
  mockNavigate.mockClear();
  mockAddToast.mockClear();
  vi.clearAllMocks();

  const mod = await import("./JobsPage");
  JobsPage = mod.default;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("JobsPage", () => {
  it("renders page heading", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("Jobs");
    const h1 = container.querySelector("h1");
    expect(h1).toBeTruthy();
    expect(h1?.textContent).toBe("Jobs");
  });

  it("shows loading skeleton rows while loading", () => {
    mockJobsResult.isLoading = true;
    render();
    const skeletons = container.querySelectorAll("[data-testid='skeleton-row']");
    expect(skeletons.length).toBe(8);
  });

  it("shows error state when query fails", () => {
    mockJobsResult.isError = true;
    render();
    expect(container.textContent).toContain("Failed to load jobs");
  });

  it("shows empty state when no jobs match filters", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    const empty = container.querySelector("[data-testid='empty-state']");
    expect(empty).toBeTruthy();
    expect(empty?.textContent).toContain("No actions match current filters");
  });

  it("renders job rows when data is present", () => {
    mockJobsResult.data = {
      items: [
        makeJob({ id: "j-aaa-11111111", topic: "ml.train", status: "running", pool: "gpu-pool" }),
        makeJob({ id: "j-bbb-22222222", topic: "data.process", status: "succeeded", pool: "cpu-pool" }),
      ],
      next_cursor: null,
    };
    render();

    // ID truncated to first 8 chars
    expect(container.textContent).toContain("j-aaa-11");
    expect(container.textContent).toContain("j-bbb-22");
    // Topic names
    expect(container.textContent).toContain("ml.train");
    expect(container.textContent).toContain("data.process");
    // Pool names
    expect(container.textContent).toContain("gpu-pool");
    expect(container.textContent).toContain("cpu-pool");
  });

  it("renders status badges for each job row", () => {
    mockJobsResult.data = {
      items: [
        makeJob({ id: "j-1", status: "running", updatedAt: "2026-02-13T12:10:00.000Z" }),
        makeJob({ id: "j-2", status: "failed", updatedAt: "2026-02-13T12:05:00.000Z" }),
      ],
      next_cursor: null,
    };
    render();
    const badges = container.querySelectorAll("[data-testid='status-badge']");
    expect(badges.length).toBe(2);
    // Default sort: updatedAt desc — j-1 (12:10) comes first, j-2 (12:05) second
    expect(badges[0]?.textContent).toBe("running");
    expect(badges[1]?.textContent).toBe("failed");
  });

  it("renders filter controls", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    const filtersBar = container.querySelector("[data-testid='filters-bar']");
    expect(filtersBar).toBeTruthy();
  });

  it("renders pagination with Newer and Older buttons", () => {
    mockJobsResult.data = {
      items: [makeJob()],
      next_cursor: 100,
    };
    render();
    expect(container.textContent).toContain("Newer");
    expect(container.textContent).toContain("Older");
  });

  it("disables Newer on first page and enables Older when next_cursor exists", () => {
    mockJobsResult.data = {
      items: [makeJob()],
      next_cursor: 100,
    };
    render();
    const buttons = container.querySelectorAll("[data-testid='button']");
    // Find the Newer and Older buttons
    let newerBtn: HTMLButtonElement | null = null;
    let olderBtn: HTMLButtonElement | null = null;
    buttons.forEach((btn) => {
      if (btn.textContent?.includes("Newer")) newerBtn = btn as HTMLButtonElement;
      if (btn.textContent?.includes("Older")) olderBtn = btn as HTMLButtonElement;
    });

    expect(newerBtn).toBeTruthy();
    expect(olderBtn).toBeTruthy();
    // On first page, Newer should be disabled
    expect(newerBtn!.disabled).toBe(true);
    // Older should be enabled since next_cursor exists
    expect(olderBtn!.disabled).toBe(false);
  });

  it("navigates to job detail when row is clicked", () => {
    mockJobsResult.data = {
      items: [makeJob({ id: "j-click-test123" })],
      next_cursor: null,
    };
    render();
    const row = container.querySelector("tbody tr") as HTMLTableRowElement;
    expect(row).toBeTruthy();
    act(() => row.click());
    expect(mockNavigate).toHaveBeenCalledWith("/jobs/j-click-test123");
  });

  it("shows New Job button that opens submit drawer", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("New Job");

    // Click New Job
    const buttons = container.querySelectorAll("[data-testid='button']");
    let newJobBtn: HTMLButtonElement | null = null;
    buttons.forEach((btn) => {
      if (btn.textContent?.includes("New Job")) newJobBtn = btn as HTMLButtonElement;
    });
    expect(newJobBtn).toBeTruthy();
    act(() => newJobBtn!.click());

    // Drawer should be open
    expect(container.querySelector("[data-testid='submit-drawer']")).toBeTruthy();
  });

  it("hides pagination during loading", () => {
    mockJobsResult.isLoading = true;
    render();
    expect(container.textContent).not.toContain("Newer");
    expect(container.textContent).not.toContain("Older");
  });

  it("hides pagination during error", () => {
    mockJobsResult.isError = true;
    render();
    expect(container.textContent).not.toContain("Newer");
    expect(container.textContent).not.toContain("Older");
  });

  it("shows table column headers", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("ID");
    expect(container.textContent).toContain("Topic");
    expect(container.textContent).toContain("State");
    expect(container.textContent).toContain("Safety Decision");
    expect(container.textContent).toContain("Pool");
    expect(container.textContent).toContain("Duration");
    expect(container.textContent).toContain("Updated");
  });

  it("renders safety decision badge for job with decision", () => {
    mockJobsResult.data = {
      items: [
        makeJob({
          id: "j-safety-1",
          safetyDecision: { type: "deny", reason: "Blocked by policy" },
        }),
      ],
      next_cursor: null,
    };
    render();
    const badges = container.querySelectorAll("[data-testid='badge']");
    const denyBadge = Array.from(badges).find((b) => b.textContent === "Deny");
    expect(denyBadge).toBeTruthy();
    expect(denyBadge?.getAttribute("data-variant")).toBe("danger");
  });

  it("renders refresh button via DataFreshness", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    const refreshBtn = container.querySelector("[data-testid='refresh']");
    expect(refreshBtn).toBeTruthy();
    act(() => (refreshBtn as HTMLButtonElement).click());
    expect(mockJobsResult.refetch).toHaveBeenCalledTimes(1);
  });
});
