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

// ---------------------------------------------------------------------------
// Mock state
// ---------------------------------------------------------------------------

const { mockJobsResult, mockNavigate } = vi.hoisted(() => ({
  mockJobsResult: {
    data: undefined as { items: unknown[]; next_cursor?: number | null } | undefined,
    isLoading: false,
    isError: false,
    dataUpdatedAt: Date.now(),
    refetch: vi.fn(),
    isRefetching: false,
  },
  mockNavigate: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("react-router-dom", () => ({
  useNavigate: () => mockNavigate,
  useSearchParams: () => [new URLSearchParams(), vi.fn()],
  Link: ({ to, children }: { to: string; children: React.ReactNode }) =>
    React.createElement("a", { href: to }, children),
  useParams: () => ({}),
}));

vi.mock("../hooks/useJobs", () => ({
  useJobs: () => mockJobsResult,
  useSubmitJob: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("../hooks/usePageTitle", () => ({
  usePageTitle: vi.fn(),
}));

vi.mock("../state/toast", () => {
  const store = { addToast: vi.fn() };
  const useToastStore = Object.assign(
    (selector: (s: typeof store) => unknown) => selector(store),
    { getState: () => store },
  );
  return { useToastStore };
});

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// Stub child components
vi.mock("../components/StatusBadge", () => ({
  JobStatusBadge: ({ state }: { state: string }) =>
    React.createElement("span", { "data-testid": "job-status-badge" }, state),
  RunStatusBadge: ({ status }: { status: string }) =>
    React.createElement("span", { "data-testid": "run-status-badge" }, status),
}));

vi.mock("../components/jobs/JobFiltersBar", () => ({
  JobFiltersBar: ({ onChange }: { onChange: (v: Record<string, unknown>) => void }) =>
    React.createElement(
      "div",
      { "data-testid": "job-filters-bar" },
      React.createElement("button", {
        "data-testid": "apply-filter",
        onClick: () => onChange({ state: ["running"] }),
      }, "Apply"),
    ),
}));

vi.mock("../components/jobs/JobSubmitDrawer", () => ({
  JobSubmitDrawer: ({ open }: { open: boolean }) =>
    open
      ? React.createElement("div", { "data-testid": "submit-drawer" }, "Submit Drawer")
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
  EmptyState: ({ title }: { title: string }) =>
    React.createElement("div", { "data-testid": "empty-state" }, title),
  TableEmptyState: ({ title, colSpan }: { title: string; colSpan: number }) =>
    React.createElement(
      "tr",
      null,
      React.createElement(
        "td",
        { colSpan },
        React.createElement("div", { "data-testid": "table-empty-state" }, title),
      ),
    ),
}));

vi.mock("../components/ui/Skeleton", () => ({
  SkeletonRow: ({ columns }: { columns: number }) =>
    React.createElement(
      "tr",
      { "data-testid": "skeleton-row" },
      React.createElement("td", { colSpan: columns }, "Loading..."),
    ),
}));

vi.mock("../components/ui/DataFreshness", () => ({
  DataFreshness: () =>
    React.createElement("div", { "data-testid": "data-freshness" }, "Freshness"),
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let JobsPage: React.ComponentType;

function makeJob(overrides: Record<string, unknown> = {}) {
  return {
    id: "job-12345678-abcd",
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
  mockNavigate.mockReset();
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

describe("JobsPage (Runs)", () => {
  it("renders page heading", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("Jobs");
    const heading = container.querySelector("h1");
    expect(heading).toBeTruthy();
    expect(heading!.textContent).toContain("Jobs");
  });

  it("shows loading state with skeleton rows", () => {
    mockJobsResult.isLoading = true;
    render();
    const skeletons = container.querySelectorAll("[data-testid='skeleton-row']");
    expect(skeletons.length).toBeGreaterThan(0);
  });

  it("shows error state when fetch fails", () => {
    mockJobsResult.isError = true;
    render();
    expect(container.textContent).toContain("Failed to load jobs");
  });

  it("shows empty state when no jobs match", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    const emptyState = container.querySelector("[data-testid='table-empty-state']");
    expect(emptyState).toBeTruthy();
    expect(emptyState!.textContent).toContain("No actions match current filters");
  });

  it("renders job rows when data is present", () => {
    mockJobsResult.data = {
      items: [
        makeJob({ id: "job-aaaaaaaa-1111", status: "running", topic: "agent.task" }),
        makeJob({ id: "job-bbbbbbbb-2222", status: "succeeded", topic: "sys.job.submit" }),
      ],
      next_cursor: null,
    };
    render();
    // Both job ID prefixes should appear in the table
    expect(container.textContent).toContain("job-aaaa");
    expect(container.textContent).toContain("job-bbbb");
    // Topics should appear
    expect(container.textContent).toContain("agent.task");
    expect(container.textContent).toContain("sys.job.submit");
  });

  it("shows status badges for each job", () => {
    mockJobsResult.data = {
      items: [
        makeJob({ id: "job-11111111-aaaa", status: "running", updatedAt: "2026-02-13T12:10:00.000Z" }),
        makeJob({ id: "job-22222222-bbbb", status: "failed", updatedAt: "2026-02-13T12:05:00.000Z" }),
      ],
      next_cursor: null,
    };
    render();
    const badges = container.querySelectorAll("[data-testid='job-status-badge']");
    expect(badges.length).toBe(2);
    const texts = Array.from(badges).map((b) => b.textContent);
    expect(texts).toContain("running");
    expect(texts).toContain("failed");
  });

  it("renders filter controls", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    const filtersBar = container.querySelector("[data-testid='job-filters-bar']");
    expect(filtersBar).toBeTruthy();
  });

  it("navigates to job detail on row click", () => {
    mockJobsResult.data = {
      items: [makeJob({ id: "job-nav-test-1234" })],
      next_cursor: null,
    };
    render();
    // Find the table row and click it
    const rows = container.querySelectorAll("tbody tr");
    expect(rows.length).toBeGreaterThan(0);
    act(() => {
      rows[0].dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(mockNavigate).toHaveBeenCalledWith("/jobs/job-nav-test-1234");
  });

  it("renders New Job button", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("New Job");
  });

  it("renders table column headers", () => {
    mockJobsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("ID");
    expect(container.textContent).toContain("Topic");
    expect(container.textContent).toContain("State");
    expect(container.textContent).toContain("Pool");
    expect(container.textContent).toContain("Duration");
    expect(container.textContent).toContain("Updated");
  });

  it("shows pagination controls when data loaded", () => {
    mockJobsResult.data = { items: [makeJob()], next_cursor: null };
    render();
    expect(container.textContent).toContain("result");
    expect(container.textContent).toContain("Page 1");
  });

  it("shows safety decision badge column", () => {
    mockJobsResult.data = {
      items: [
        makeJob({
          id: "job-safety-11111111",
          safetyDecision: { type: "deny", reason: "Blocked" },
        }),
      ],
      next_cursor: null,
    };
    render();
    expect(container.textContent).toContain("Safety Decision");
    // The SafetyBadge inside JobsPage renders the decision
    expect(container.textContent).toContain("Deny");
  });
});
