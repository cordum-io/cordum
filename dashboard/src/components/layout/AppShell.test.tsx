import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

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

const { mockUiStore, mockConfigStore, mockAuthConfig, mockApprovalsData, mockDlqData, mockSessionData } = vi.hoisted(() => ({
  mockUiStore: {
    globalSearch: "",
    setGlobalSearch: vi.fn(),
    setCommandOpen: vi.fn(),
    theme: "dark" as string,
    resolvedTheme: "dark" as string,
    toggleTheme: vi.fn(),
    syncSystemTheme: vi.fn(),
  },
  mockConfigStore: {
    apiBaseUrl: "/api/v1",
    apiKey: "test-key",
    logout: vi.fn(),
  },
  mockAuthConfig: { data: null as Record<string, unknown> | null },
  mockApprovalsData: { data: { items: [] as unknown[] } },
  mockDlqData: { data: { items: [] as unknown[] } },
  mockSessionData: { data: null as Record<string, unknown> | null },
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("react-router-dom", () => ({
  NavLink: ({ to, children, className }: { to: string; children: React.ReactNode; className: unknown }) => {
    const cls = typeof className === "function" ? className({ isActive: to === "/" }) : className;
    return React.createElement("a", { href: to, className: cls, "data-testid": "navlink" }, children);
  },
  useLocation: () => ({ pathname: "/", search: "", hash: "" }),
  useNavigate: () => vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: string[] }) => {
    if (opts.queryKey[0] === "auth-session") return mockSessionData;
    if (opts.queryKey[0] === "approvals") return mockApprovalsData;
    if (opts.queryKey[0] === "dlq") return mockDlqData;
    return { data: null };
  },
}));

vi.mock("../../state/ui", () => ({
  useUiStore: (selector: (s: typeof mockUiStore) => unknown) => selector(mockUiStore),
}));

vi.mock("../../state/config", () => ({
  useConfigStore: (selector: (s: typeof mockConfigStore) => unknown) => selector(mockConfigStore),
}));

vi.mock("../../state/events", () => ({
  usePresenceCleanup: vi.fn(),
}));

vi.mock("../../hooks/useAuthConfig", () => ({
  useAuthConfig: () => mockAuthConfig,
}));

vi.mock("../../lib/api", () => ({
  api: {
    getSession: vi.fn().mockResolvedValue(null),
    listApprovals: vi.fn().mockResolvedValue({ items: [] }),
    listDLQPage: vi.fn().mockResolvedValue({ items: [] }),
    logout: vi.fn().mockResolvedValue(undefined),
  },
}));

vi.mock("../../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock("./Breadcrumbs", () => ({
  Breadcrumbs: () => React.createElement("nav", { "aria-label": "breadcrumb", "data-testid": "breadcrumbs" }, "Breadcrumbs"),
}));

vi.mock("../ConnectionIndicator", () => ({
  ConnectionIndicator: () => React.createElement("span", { "data-testid": "conn-indicator" }, "Connected"),
}));

vi.mock("./MaintenanceBanner", () => ({
  MaintenanceBanner: () => null,
}));

vi.mock("./EnvironmentBanner", () => ({
  EnvironmentBorder: () => null,
  EnvironmentBadge: () => null,
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

let AppShell: React.ComponentType<{ children: React.ReactNode }>;

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  // Reset state
  mockUiStore.globalSearch = "";
  mockUiStore.theme = "dark";
  mockUiStore.resolvedTheme = "dark";
  mockConfigStore.apiKey = "test-key";
  mockConfigStore.apiBaseUrl = "/api/v1";
  mockAuthConfig.data = null;
  mockApprovalsData.data = { items: [] };
  mockDlqData.data = { items: [] };
  mockSessionData.data = null;
  vi.clearAllMocks();

  const mod = await import("./AppShell");
  AppShell = mod.AppShell;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(children?: React.ReactNode) {
  act(() => {
    root.render(
      React.createElement(AppShell, null, children ?? React.createElement("div", { "data-testid": "page-content" }, "Page")),
    );
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("AppShell", () => {
  it("renders all 10 navigation items", () => {
    render();
    const navLinks = container.querySelectorAll("a[data-testid='navlink']");
    // Desktop sidebar + mobile nav = 2 sets of 10
    expect(navLinks.length).toBeGreaterThanOrEqual(10);
    const labels = Array.from(navLinks).map((a) => a.textContent);
    expect(labels).toEqual(expect.arrayContaining([
      expect.stringContaining("Overview"),
      expect.stringContaining("Jobs"),
      expect.stringContaining("Workflows"),
      expect.stringContaining("Agent Fleet"),
      expect.stringContaining("Approvals"),
      expect.stringContaining("Policy Studio"),
      expect.stringContaining("Packs"),
      expect.stringContaining("Dead Letters"),
      expect.stringContaining("Audit Log"),
      expect.stringContaining("Settings"),
    ]));
  });

  it("renders children in main content area", () => {
    render(React.createElement("div", { "data-testid": "test-child" }, "Child content"));
    expect(container.querySelector("[data-testid='test-child']")).toBeTruthy();
    expect(container.textContent).toContain("Child content");
  });

  it("renders breadcrumbs in header", () => {
    render();
    const breadcrumbs = container.querySelector("[data-testid='breadcrumbs']");
    expect(breadcrumbs).toBeTruthy();
  });

  it("renders connection indicator in sidebar", () => {
    render();
    const indicator = container.querySelector("[data-testid='conn-indicator']");
    expect(indicator).toBeTruthy();
  });

  it("renders search input", () => {
    render();
    const input = container.querySelector("input");
    expect(input).toBeTruthy();
    expect(input?.getAttribute("placeholder")).toContain("Search");
  });

  it("renders theme toggle button", () => {
    render();
    const buttons = Array.from(container.querySelectorAll("button"));
    const themeBtn = buttons.find((b) => b.textContent?.includes("Dark"));
    expect(themeBtn).toBeTruthy();
  });

  it("renders command button", () => {
    render();
    const buttons = Array.from(container.querySelectorAll("button"));
    const cmdBtn = buttons.find((b) => b.textContent?.includes("Command"));
    expect(cmdBtn).toBeTruthy();
  });

  it("calls toggleTheme when theme button clicked", () => {
    render();
    const buttons = Array.from(container.querySelectorAll("button"));
    const themeBtn = buttons.find((b) => b.textContent?.includes("Dark"));
    act(() => themeBtn?.click());
    expect(mockUiStore.toggleTheme).toHaveBeenCalledTimes(1);
  });

  it("calls setCommandOpen when command button clicked", () => {
    render();
    const buttons = Array.from(container.querySelectorAll("button"));
    const cmdBtn = buttons.find((b) => b.textContent?.includes("Command"));
    act(() => cmdBtn?.click());
    expect(mockUiStore.setCommandOpen).toHaveBeenCalledWith(true);
  });

  it("shows nav badge counts when approvals/dlq have items", () => {
    mockApprovalsData.data = { items: [{ id: "a1" }, { id: "a2" }, { id: "a3" }] };
    mockDlqData.data = { items: [{ id: "d1" }] };
    render();
    // Badges show formatted counts
    expect(container.textContent).toContain("3");
    expect(container.textContent).toContain("1");
  });

  it("does not show badges when counts are zero", () => {
    mockApprovalsData.data = { items: [] };
    mockDlqData.data = { items: [] };
    render();
    // No badge spans for zero counts — check that there are no badge-like elements
    const navLinks = container.querySelectorAll("a[data-testid='navlink']");
    const approvalsLink = Array.from(navLinks).find((a) => a.textContent?.includes("Approvals"));
    // The link text should be exactly "Approvals" with no trailing badge number
    expect(approvalsLink?.textContent?.trim()).toBe("Approvals");
  });

  it("hides auth controls when auth not required", () => {
    mockAuthConfig.data = null;
    render();
    expect(container.textContent).not.toContain("Logout");
  });

  it("shows user identity and logout when auth is required", () => {
    mockAuthConfig.data = { password_enabled: true, user_auth_enabled: false, saml_enabled: false };
    mockSessionData.data = {
      user: { display_name: "Alice Admin", email: "alice@test.com", roles: ["admin"], tenant: "acme" },
    };
    render();
    expect(container.textContent).toContain("Alice Admin");
    expect(container.textContent).toContain("acme");
    expect(container.textContent).toContain("admin");
    expect(container.textContent).toContain("Logout");
  });

  it("shows api base url in sidebar", () => {
    mockConfigStore.apiBaseUrl = "https://api.cordum.io";
    render();
    expect(container.textContent).toContain("https://api.cordum.io");
  });

  it("shows 'same origin' when apiBaseUrl is empty", () => {
    mockConfigStore.apiBaseUrl = "";
    render();
    expect(container.textContent).toContain("same origin");
  });
});
