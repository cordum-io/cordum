import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// matchMedia must be defined before any component import (ui.ts uses it at module scope)
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
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { PolicyBundle, PolicyRule } from "../../api/types";

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

const mockNavigate = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => mockNavigate };
});

const mockToggleMutate = vi.fn();
vi.mock("../../hooks/usePolicies", () => ({
  usePolicyBundle: vi.fn(),
  useToggleRule: () => ({ mutate: mockToggleMutate }),
  encodePolicyBundleId: (id: string) => encodeURIComponent(id),
}));

vi.mock("@monaco-editor/react", () => ({
  default: () => null,
}));

import { usePolicyBundle } from "../../hooks/usePolicies";
import { VisualRuleBuilder } from "./VisualRuleBuilder";

const mockUsePolicyBundle = vi.mocked(usePolicyBundle);

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let queryClient: QueryClient;

function render(bundleId = "bundle-1") {
  act(() => {
    root.render(
      React.createElement(
        QueryClientProvider,
        { client: queryClient },
        React.createElement(
          MemoryRouter,
          null,
          React.createElement(VisualRuleBuilder, { bundleId }),
        ),
      ),
    );
  });
}

function makeRule(overrides: Partial<PolicyRule> & { id: string }): PolicyRule {
  return {
    matchCriteria: {},
    decisionType: "allow",
    ...overrides,
  } as any as PolicyRule;
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  mockNavigate.mockClear();
  mockToggleMutate.mockClear();
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  queryClient.clear();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("VisualRuleBuilder", () => {
  it("renders loading state", () => {
    mockUsePolicyBundle.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
    } as ReturnType<typeof usePolicyBundle>);

    render();
    expect(container.textContent).toContain("Loading policy rules");
  });

  it("renders error state", () => {
    mockUsePolicyBundle.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("boom"),
    } as ReturnType<typeof usePolicyBundle>);

    render();
    expect(container.textContent).toContain("Failed to load policy bundle");
  });

  it("renders empty rules prompt", () => {
    mockUsePolicyBundle.mockReturnValue({
      data: { id: "bundle-1", name: "Default", rules: [] } as PolicyBundle,
      isLoading: false,
      error: null,
    } as ReturnType<typeof usePolicyBundle>);

    render();
    expect(container.textContent).toContain("No rules yet");
    expect(container.textContent).toContain("Create Rule");
  });

  it("renders rules with decision badges", () => {
    const rules: PolicyRule[] = [
      makeRule({
        id: "r1",
        matchCriteria: { capabilities: ["code.write"] },
        decisionType: "deny",
        reason: "Too risky",
      }),
      makeRule({
        id: "r2",
        matchCriteria: { riskTags: ["pii"] },
        decisionType: "require_approval",
      }),
    ];

    mockUsePolicyBundle.mockReturnValue({
      data: { id: "bundle-1", name: "Test", rules } as PolicyBundle,
      isLoading: false,
      error: null,
    } as ReturnType<typeof usePolicyBundle>);

    render();
    expect(container.textContent).toContain("2 rules");
    expect(container.textContent).toContain("code.write");
    expect(container.textContent).toContain("Deny");
    expect(container.textContent).toContain("pii");
    expect(container.textContent).toContain("Require Approval");
  });

  it("shows conflict warning when later rule is subset of earlier", () => {
    const rules: PolicyRule[] = [
      makeRule({
        id: "r1",
        matchCriteria: { capabilities: ["code.write", "code.exec"] },
        decisionType: "deny",
      }),
      makeRule({
        id: "r2",
        matchCriteria: { capabilities: ["code.write"] },
        decisionType: "allow",
      }),
    ];

    mockUsePolicyBundle.mockReturnValue({
      data: { id: "bundle-1", name: "Test", rules } as PolicyBundle,
      isLoading: false,
      error: null,
    } as ReturnType<typeof usePolicyBundle>);

    render();
    expect(container.textContent).toContain("may never fire");
  });

  it("does not show conflict for non-overlapping rules", () => {
    const rules: PolicyRule[] = [
      makeRule({
        id: "r1",
        matchCriteria: { capabilities: ["code.write"] },
        decisionType: "deny",
      }),
      makeRule({
        id: "r2",
        matchCriteria: { capabilities: ["code.read"] },
        decisionType: "allow",
      }),
    ];

    mockUsePolicyBundle.mockReturnValue({
      data: { id: "bundle-1", name: "Test", rules } as PolicyBundle,
      isLoading: false,
      error: null,
    } as ReturnType<typeof usePolicyBundle>);

    render();
    expect(container.textContent).not.toContain("may never fire");
  });

  it("shows Create Rule button", () => {
    mockUsePolicyBundle.mockReturnValue({
      data: { id: "bundle-1", name: "Test", rules: [] } as PolicyBundle,
      isLoading: false,
      error: null,
    } as ReturnType<typeof usePolicyBundle>);

    render();
    const buttons = Array.from(container.querySelectorAll("button"));
    const createBtn = buttons.find((b) => b.textContent?.includes("Create Rule"));
    expect(createBtn).toBeDefined();
  });
});
