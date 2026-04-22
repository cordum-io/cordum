import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";

const { mockSearchParams, setSearchParamsMock } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
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
  return {
    mockSearchParams: { tab: "" as string, mode: "" as string },
    setSearchParamsMock: vi.fn(),
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
vi.mock("@/pages/govern/InputRulesPage", () => ({ default: () => <div>Input rules content</div> }));
vi.mock("@/pages/govern/OutputRulesPage", () => ({ default: () => <div>Output rules content</div> }));
vi.mock("@/pages/govern/SimulatorPage", () => ({ default: () => <div>Simulator content</div> }));
vi.mock("@/pages/govern/BundlesPage", () => ({ default: () => <div>Bundles content</div> }));
vi.mock("@/pages/govern/VelocityRulesPage", () => ({ default: () => <div>Velocity content</div> }));
vi.mock("@/pages/govern/ReplayPage", () => ({ default: () => <div>Replay content</div> }));
vi.mock("@/pages/govern/PolicyAnalyticsPage", () => ({ default: () => <div>Analytics content</div> }));
vi.mock("@/pages/govern/TenantsPage", () => ({ default: () => <div>Scope content</div> }));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useSearchParams: () => {
      const params = new URLSearchParams();
      if (mockSearchParams.tab) params.set("tab", mockSearchParams.tab);
      if (mockSearchParams.mode) params.set("mode", mockSearchParams.mode);
      return [params, setSearchParamsMock] as const;
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
  mockSearchParams.mode = "";
  setSearchParamsMock.mockReset();
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

async function renderPage(tab = "", mode = "") {
  mockSearchParams.tab = tab;
  mockSearchParams.mode = mode;
  await act(async () => {
    root.render(
      <MemoryRouter>
        <React.Suspense fallback={<div>Loading...</div>}>
          <PolicyOverviewPage />
        </React.Suspense>
      </MemoryRouter>,
    );
    await Promise.resolve();
  });
}

function findTabButton(label: string): HTMLButtonElement | null {
  return container.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
}

function getActiveLabels(): string[] {
  return Array.from(
    container.querySelectorAll<HTMLButtonElement>('button[aria-selected="true"]'),
  ).map((button) => button.getAttribute("aria-label") ?? "");
}

describe("PolicyOverviewPage tab rendering", () => {
  it("renders the merged Policy Studio primary tabs", async () => {
    await renderPage();
    expect(findTabButton("Overview")).not.toBeNull();
    expect(findTabButton("Input Rules")).not.toBeNull();
    expect(findTabButton("Output Rules")).not.toBeNull();
    expect(findTabButton("Velocity")).not.toBeNull();
    expect(findTabButton("Evaluation")).not.toBeNull();
    expect(findTabButton("Bundles")).not.toBeNull();
    expect(findTabButton("Scope")).not.toBeNull();
  });

  it("activates Overview by default", async () => {
    await renderPage();
    expect(getActiveLabels()).toContain("Overview");
  });

  it("activates Velocity from the tab query param", async () => {
    await renderPage("velocity");
    expect(getActiveLabels()).toContain("Velocity");
    expect(container.textContent).toContain("Velocity content");
  });

  it("activates Evaluation + Replay from tab and mode params", async () => {
    await renderPage("evaluation", "replay");
    expect(getActiveLabels()).toEqual(expect.arrayContaining(["Evaluation", "Replay"]));
    expect(container.textContent).toContain("Replay content");
  });

  it("normalizes the legacy simulator tab into evaluation mode", async () => {
    await renderPage("simulator");
    expect(getActiveLabels()).toEqual(expect.arrayContaining(["Evaluation", "Simulator"]));
    expect(container.textContent).toContain("Simulator content");
  });

  it("falls back to overview for invalid tab params", async () => {
    await renderPage("nonexistent");
    expect(getActiveLabels()).toContain("Overview");
  });

  it("mounts the ChainIntegrityWidget inside the Overview tab content", async () => {
    await renderPage("overview");
    const widget = container.querySelector(
      "[data-testid=chain-integrity-widget]",
    );
    expect(widget).not.toBeNull();
    // With the default (data: undefined, not loading, not error) mock
    // the widget lands in its not_checked state.
    expect(widget?.getAttribute("data-state")).toBe("not_checked");
  });

  it("does NOT mount the GapAlertBanner when verify data is absent or ok", async () => {
    await renderPage("overview");
    expect(container.querySelector("[data-testid=gap-alert-banner]")).toBeNull();
  });
});
