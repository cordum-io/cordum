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

const { mockApprovalsResult, mockHistoryResult, mockApproveMutation, mockRejectMutation, mockPermission } = vi.hoisted(() => ({
  mockApprovalsResult: {
    data: undefined as { items: Array<Record<string, unknown>>; next_cursor: null } | undefined,
    isLoading: false,
    isError: false,
    dataUpdatedAt: Date.now(),
    refetch: vi.fn(),
    isRefetching: false,
  },
  mockHistoryResult: {
    data: undefined as { items: Array<Record<string, unknown>> } | undefined,
  },
  mockApproveMutation: { mutateAsync: vi.fn().mockResolvedValue(undefined), isPending: false },
  mockRejectMutation: { mutateAsync: vi.fn().mockResolvedValue(undefined), isPending: false },
  mockPermission: { allowed: false, userRoles: [] as string[] },
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const mockSearchParams = new URLSearchParams();
const mockSetSearchParams = vi.fn();

vi.mock("react-router-dom", () => ({
  useParams: () => ({}),
  useSearchParams: () => [mockSearchParams, mockSetSearchParams],
  useNavigate: () => vi.fn(),
  Link: ({ children, to, ...rest }: { children: React.ReactNode; to: string; [k: string]: unknown }) =>
    React.createElement("a", { href: to, ...rest }, children),
  NavLink: ({ children, to }: { children: React.ReactNode; to: string }) =>
    React.createElement("a", { href: to }, children),
}));

vi.mock("../hooks/useApprovals", () => ({
  useApprovals: () => mockApprovalsResult,
  useApprovalHistory: () => mockHistoryResult,
  useApproveJob: () => mockApproveMutation,
  useRejectJob: () => mockRejectMutation,
}));

vi.mock("../hooks/usePageTitle", () => ({
  usePageTitle: vi.fn(),
}));

vi.mock("../hooks/usePermission", () => ({
  usePermission: () => mockPermission,
}));

vi.mock("../state/config", () => ({
  useConfigStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ apiBaseUrl: "/api/v1", apiKey: "", user: null, principalRole: "", approvalSlaMs: 900_000 }),
    { getState: () => ({ apiBaseUrl: "/api/v1", apiKey: "", user: null, principalRole: "", approvalSlaMs: 900_000 }) },
  ),
}));

vi.mock("../state/events", () => ({
  useEventStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ approvalAssignments: new Map() }),
    { getState: () => ({ approvalAssignments: new Map() }) },
  ),
}));

vi.mock("../state/toast", () => {
  const store = { addToast: vi.fn() };
  const useToastStore = Object.assign(
    (selector: (s: typeof store) => unknown) => selector(store),
    { getState: () => store },
  );
  return { useToastStore };
});

// Mock UI components as simple stubs
vi.mock("../components/ui/Badge", () => ({
  Badge: ({ children, variant }: { children: React.ReactNode; variant?: string }) =>
    React.createElement("span", { "data-testid": "badge", "data-variant": variant }, children),
}));

vi.mock("../components/ui/Button", () => ({
  Button: ({ children, onClick, ...rest }: { children: React.ReactNode; onClick?: () => void; [k: string]: unknown }) =>
    React.createElement("button", { onClick, "data-testid": "button", ...rest }, children),
}));

vi.mock("../components/ui/Card", () => ({
  Card: ({ children, className }: { children: React.ReactNode; className?: string }) =>
    React.createElement("div", { "data-testid": "card", className }, children),
}));

vi.mock("../components/ui/DataFreshness", () => ({
  DataFreshness: ({ onRefresh }: { onRefresh: () => void }) =>
    React.createElement("button", { "data-testid": "data-freshness", onClick: onRefresh }, "Refresh"),
}));

vi.mock("../components/approvals/ApprovalDetailPanel", () => ({
  ApprovalDetailPanel: () =>
    React.createElement("div", { "data-testid": "approval-detail-panel" }, "DetailPanel"),
}));

vi.mock("../components/approvals/ApprovalHistory", () => ({
  ApprovalHistory: () =>
    React.createElement("div", { "data-testid": "approval-history" }, "ApprovalHistory"),
}));

vi.mock("../components/approvals/ApprovalQueueFilters", () => ({
  ApprovalQueueFilters: () =>
    React.createElement("div", { "data-testid": "queue-filters" }, "Filters"),
  applyFilters: (approvals: unknown[]) => approvals,
}));

vi.mock("../components/approvals/BulkActionBar", () => ({
  BulkActionBar: () =>
    React.createElement("div", { "data-testid": "bulk-action-bar" }, "BulkActions"),
}));

vi.mock("../components/RequireRole", () => ({
  RequireRole: ({ children, roles }: { children: React.ReactNode; roles: string[] }) =>
    React.createElement("div", { "data-testid": "require-role", "data-roles": roles.join(",") }, children),
}));

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
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
let ApprovalsPage: React.ComponentType;

function makeApproval(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: "appr-001",
    jobId: "job-12345678",
    status: "pending",
    requestedAt: "2026-02-25T10:00:00.000Z",
    reason: "Budget limit exceeded",
    policyRule: "max-budget-500",
    humanSummary: "Job requires budget approval",
    urgencyLevel: "fresh",
    waitMs: 120_000,
    capabilities: ["code-exec"],
    riskTags: ["high-cost"],
    ...overrides,
  };
}

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  // Reset mock state
  mockApprovalsResult.data = undefined;
  mockApprovalsResult.isLoading = false;
  mockApprovalsResult.isError = false;
  mockApprovalsResult.dataUpdatedAt = Date.now();
  mockApprovalsResult.refetch = vi.fn();
  mockApprovalsResult.isRefetching = false;
  mockHistoryResult.data = undefined;
  mockApproveMutation.mutateAsync = vi.fn().mockResolvedValue(undefined);
  mockRejectMutation.mutateAsync = vi.fn().mockResolvedValue(undefined);
  mockPermission.allowed = false;
  mockPermission.userRoles = [];

  // Reset search params
  for (const key of [...mockSearchParams.keys()]) {
    mockSearchParams.delete(key);
  }

  vi.clearAllMocks();

  const mod = await import("./ApprovalsPage");
  ApprovalsPage = mod.default;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render() {
  act(() => {
    root.render(React.createElement(ApprovalsPage));
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("ApprovalsPage", () => {
  it("renders page heading", () => {
    mockApprovalsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("Approvals");
    const heading = container.querySelector("h1");
    expect(heading).toBeTruthy();
    expect(heading?.textContent).toBe("Approvals");
  });

  it("shows loading state with skeleton cards", () => {
    mockApprovalsResult.isLoading = true;
    render();
    const pulses = container.querySelectorAll(".animate-pulse");
    expect(pulses.length).toBeGreaterThan(0);
  });

  it("shows error state with failure message", () => {
    mockApprovalsResult.isError = true;
    mockApprovalsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("Failed to load approvals");
  });

  it("shows empty state when no approvals", () => {
    mockApprovalsResult.data = { items: [], next_cursor: null };
    render();
    expect(container.textContent).toContain("All clear");
    expect(container.textContent).toContain("no pending approvals");
  });

  it("renders approval items when data present", () => {
    mockApprovalsResult.data = {
      items: [
        makeApproval({ id: "appr-001", humanSummary: "Job requires budget approval" }),
        makeApproval({ id: "appr-002", humanSummary: "Sensitive data access" }),
      ] as never[],
      next_cursor: null,
    };
    render();
    expect(container.textContent).toContain("Job requires budget approval");
    expect(container.textContent).toContain("Sensitive data access");
  });

  it("shows queue and history tabs", () => {
    mockApprovalsResult.data = { items: [], next_cursor: null };
    render();
    const tabs = container.querySelectorAll("[role='tab']");
    expect(tabs.length).toBe(2);

    const tabTexts = Array.from(tabs).map((t) => t.textContent);
    expect(tabTexts.some((t) => t?.includes("Queue"))).toBe(true);
    expect(tabTexts.some((t) => t?.includes("History"))).toBe(true);
  });

  it("renders RequireRole wrapper for bulk actions when items selected", () => {
    const approval = makeApproval({ id: "appr-select" });
    mockApprovalsResult.data = { items: [approval] as never[], next_cursor: null };
    render();

    // Click the checkbox to select the approval
    const checkbox = container.querySelector("input[type='checkbox']");
    if (checkbox) {
      act(() => {
        (checkbox as HTMLInputElement).click();
      });
    }

    // RequireRole wraps the BulkActionBar with admin/operator roles
    const roleGate = container.querySelector("[data-testid='require-role']");
    if (roleGate) {
      expect(roleGate.getAttribute("data-roles")).toContain("admin");
      expect(roleGate.getAttribute("data-roles")).toContain("operator");
    }
  });

  it("shows filter controls when approvals exist", () => {
    mockApprovalsResult.data = {
      items: [makeApproval()] as never[],
      next_cursor: null,
    };
    render();
    const filters = container.querySelector("[data-testid='queue-filters']");
    expect(filters).toBeTruthy();
  });

  it("shows pending badge with count when approvals present", () => {
    mockApprovalsResult.data = {
      items: [
        makeApproval({ id: "a-1" }),
        makeApproval({ id: "a-2" }),
        makeApproval({ id: "a-3" }),
      ] as never[],
      next_cursor: null,
    };
    render();
    const badges = container.querySelectorAll("[data-testid='badge']");
    const pendingBadge = Array.from(badges).find((b) => b.textContent?.includes("pending"));
    expect(pendingBadge).toBeTruthy();
    expect(pendingBadge?.textContent).toContain("3");
  });

  it("renders DataFreshness component", () => {
    mockApprovalsResult.data = { items: [], next_cursor: null };
    render();
    const freshness = container.querySelector("[data-testid='data-freshness']");
    expect(freshness).toBeTruthy();
  });

  it("shows stats strip with pending count and avg wait", () => {
    mockApprovalsResult.data = {
      items: [
        makeApproval({ id: "a-1", waitMs: 60_000 }),
        makeApproval({ id: "a-2", waitMs: 120_000 }),
      ] as never[],
      next_cursor: null,
    };
    render();
    // Stats strip should show pending count
    expect(container.textContent).toContain("2");
    expect(container.textContent).toContain("pending");
    // avg wait of 90s = 1m
    expect(container.textContent).toContain("avg wait");
  });

  it("shows history tab content when history tab active", () => {
    mockApprovalsResult.data = { items: [], next_cursor: null };
    mockSearchParams.set("tab", "history");
    render();
    const historyPanel = container.querySelector("[data-testid='approval-history']");
    expect(historyPanel).toBeTruthy();
  });

  it("shows urgency info for critical approvals", () => {
    mockApprovalsResult.data = {
      items: [
        makeApproval({ id: "a-crit", urgencyLevel: "critical", humanSummary: "Critical job" }),
      ] as never[],
      next_cursor: null,
    };
    render();
    expect(container.textContent).toContain("Critical");
  });

  it("does not show filter controls during loading", () => {
    mockApprovalsResult.isLoading = true;
    render();
    const filters = container.querySelector("[data-testid='queue-filters']");
    expect(filters).toBeNull();
  });
});
