import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";

const { navigateMock, mockSearchParams } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false, media: "", onchange: null,
      addListener: () => {}, removeListener: () => {},
      addEventListener: () => {}, removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
  return {
    navigateMock: vi.fn(),
    mockSearchParams: { tab: "" as string },
  };
});

vi.mock("@/hooks/usePolicies", () => ({
  usePolicyBundles: () => ({ data: { items: [] }, isLoading: false }),
  usePolicyRules: () => ({ data: { items: [] }, isLoading: false }),
}));
vi.mock("@/hooks/useAuditChainVerify", () => ({
  useAuditChainVerify: () => ({
    data: undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    dataUpdatedAt: 0,
  }),
}));
vi.mock("@/hooks/useAuditVerify", () => ({
  useAuditVerify: () => ({
    data: undefined,
    isLoading: false,
    isFetching: false,
    isError: false,
    dataUpdatedAt: 0,
  }),
  useTriggerAuditVerify: () => () => {},
}));
vi.mock("@/hooks/usePermission", () => ({
  usePermission: () => ({ allowed: true, userRoles: ["admin"] }),
  useIsAdmin: () => true,
}));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ tenantId: "default" }),
}));
vi.mock("@/hooks/usePageTitle", () => ({ usePageTitle: () => {} }));
vi.mock("@/hooks/useStatus", () => ({ useStatus: () => ({ data: null, isLoading: false }) }));
vi.mock("@/hooks/usePolicyAccess", () => ({
  usePolicyAccess: () => ({
    canEdit: true, canPublish: true, canRelease: true, isReadOnly: false,
    canManageOutputRules: true, canManageTenants: true,
  }),
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
    useSearchParams: () => {
      const params = new URLSearchParams();
      if (mockSearchParams.tab) params.set("tab", mockSearchParams.tab);
      return [params, vi.fn()] as const;
    },
  };
});

import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import PolicyOverviewPage from "./PolicyOverviewPage";

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  mockSearchParams.tab = "";
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function renderPage(tab = "") {
  mockSearchParams.tab = tab;
  act(() => {
    root.render(
      <MemoryRouter>
        <React.Suspense fallback={<div>Loading...</div>}>
          <PolicyOverviewPage />
        </React.Suspense>
      </MemoryRouter>,
    );
  });
}

function findTabButton(label: string): HTMLButtonElement | null {
  const buttons = container.querySelectorAll<HTMLButtonElement>("button[type='button']");
  for (const btn of buttons) {
    if (btn.textContent?.trim().startsWith(label)) return btn;
  }
  return null;
}

function getActiveTabLabel(): string | null {
  // The active tab has shadow-soft class
  const buttons = container.querySelectorAll<HTMLButtonElement>("button[type='button']");
  for (const btn of buttons) {
    if (btn.className.includes("shadow-soft")) return btn.textContent?.trim() ?? null;
  }
  return null;
}

describe("PolicyOverviewPage tab rendering", () => {
  it("renders all 5 tab buttons", () => {
    renderPage();
    expect(findTabButton("Overview")).not.toBeNull();
    expect(findTabButton("Input Rules")).not.toBeNull();
    expect(findTabButton("Output Rules")).not.toBeNull();
    expect(findTabButton("Simulator")).not.toBeNull();
    expect(findTabButton("Bundles")).not.toBeNull();
  });

  it("activates Overview tab by default", () => {
    renderPage();
    expect(getActiveTabLabel()).toContain("Overview");
  });

  it("activates Input Rules tab from URL param", () => {
    renderPage("input-rules");
    expect(getActiveTabLabel()).toContain("Input Rules");
  });

  it("activates Output Rules tab from URL param", () => {
    renderPage("output-rules");
    expect(getActiveTabLabel()).toContain("Output Rules");
  });

  it("activates Simulator tab from URL param", () => {
    renderPage("simulator");
    expect(getActiveTabLabel()).toContain("Simulator");
  });

  it("activates Bundles tab from URL param", () => {
    renderPage("bundles");
    expect(getActiveTabLabel()).toContain("Bundles");
  });

  it("falls back to overview for invalid tab param", () => {
    renderPage("nonexistent");
    expect(getActiveTabLabel()).toContain("Overview");
  });

  it("mounts the ChainIntegrityWidget inside the Overview tab content", () => {
    renderPage("overview");
    const widget = container.querySelector(
      "[data-testid=chain-integrity-widget]",
    );
    expect(widget).not.toBeNull();
    // With the default (data: undefined, not loading, not error) mock
    // the widget lands in its not_checked state.
    expect(widget?.getAttribute("data-state")).toBe("not_checked");
  });

  it("does NOT mount the GapAlertBanner when verify data is absent or ok", () => {
    renderPage("overview");
    expect(container.querySelector("[data-testid=gap-alert-banner]")).toBeNull();
  });
});
