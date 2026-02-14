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
import { MemoryRouter } from "react-router-dom";
import type { PolicyBundle } from "../../api/types";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const mockBundlesData = { items: [] as PolicyBundle[] };
let mockIsLoading = false;

vi.mock("../../hooks/usePolicies", () => ({
  usePolicyBundles: () => ({
    data: mockBundlesData,
    isLoading: mockIsLoading,
    isError: false,
  }),
}));

import { PolicyBundleProvider, usePolicyBundleContext } from "./PolicyBundleContext";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  mockBundlesData.items = [];
  mockIsLoading = false;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

// Consumer component that renders context values
function ContextConsumer() {
  const { bundleId, bundles, isLoading, setBundleId } = usePolicyBundleContext();
  return React.createElement("div", null,
    React.createElement("span", { "data-testid": "bundle-id" }, bundleId),
    React.createElement("span", { "data-testid": "count" }, String(bundles.length)),
    React.createElement("span", { "data-testid": "loading" }, String(isLoading)),
    React.createElement("button", {
      type: "button",
      "data-testid": "set-btn",
      onClick: () => setBundleId("new-bundle"),
    }, "Set"),
  );
}

function renderProvider(initialRoute = "/policies") {
  act(() => {
    root.render(
      React.createElement(
        MemoryRouter,
        { initialEntries: [initialRoute] },
        React.createElement(PolicyBundleProvider, null,
          React.createElement(ContextConsumer),
        ),
      ),
    );
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("PolicyBundleContext", () => {
  it("reads bundleId from URL param 'bundle'", () => {
    renderProvider("/policies?bundle=from-url");
    const el = container.querySelector("[data-testid='bundle-id']");
    expect(el?.textContent).toBe("from-url");
  });

  it("auto-selects first bundle when no bundleId and bundles are loaded", async () => {
    mockBundlesData.items = [
      { id: "b1", name: "First", rules: [] },
      { id: "b2", name: "Second", rules: [] },
    ] as PolicyBundle[];

    renderProvider("/policies");

    // Wait for effect to run
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });

    const el = container.querySelector("[data-testid='bundle-id']");
    expect(el?.textContent).toBe("b1");
  });

  it("exposes bundles array and loading state", () => {
    mockBundlesData.items = [
      { id: "b1", name: "First", rules: [] },
    ] as PolicyBundle[];
    mockIsLoading = true;

    renderProvider("/policies");
    expect(container.querySelector("[data-testid='count']")?.textContent).toBe("1");
    expect(container.querySelector("[data-testid='loading']")?.textContent).toBe("true");
  });

  it("setBundleId updates state", async () => {
    renderProvider("/policies");

    const btn = container.querySelector("[data-testid='set-btn']") as HTMLButtonElement;
    act(() => btn.click());

    await act(async () => {
      await new Promise((r) => setTimeout(r, 10));
    });

    expect(container.querySelector("[data-testid='bundle-id']")?.textContent).toBe("new-bundle");
  });

  it("usePolicyBundleContext throws outside provider", () => {
    expect(() => {
      act(() => {
        root.render(React.createElement(ContextConsumer));
      });
    }).toThrow("usePolicyBundleContext must be used within PolicyBundleProvider");
  });
});
