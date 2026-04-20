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

const { mockDecisions, mockWsStatus } = vi.hoisted(() => ({
  mockDecisions: {
    decisions: [] as Array<{
      id: string;
      timestamp: string;
      topic: string;
      decision: string;
      matchedRule?: string;
      evalTimeMs?: number;
    }>,
    isLoading: false,
    isError: false,
    isFetching: false,
  },
  mockWsStatus: { value: "connected" },
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("../../hooks/useSafetyDecisions", () => ({
  useSafetyDecisions: () => mockDecisions,
}));

vi.mock("../../state/events", () => ({
  useEventStore: (selector: (s: { status: string }) => unknown) =>
    selector({ status: mockWsStatus.value }),
}));

vi.mock("../../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let SafetyDecisionFeed: React.ComponentType;

function makeDecision(overrides: Partial<typeof mockDecisions.decisions[0]> = {}) {
  return {
    id: `d-${Math.random().toString(36).slice(2, 8)}`,
    timestamp: "2026-02-13T12:00:00.000Z",
    topic: "sys.job.submit",
    decision: "allow",
    ...overrides,
  };
}

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  mockDecisions.decisions = [];
  mockDecisions.isLoading = false;
  mockDecisions.isError = false;
  mockDecisions.isFetching = false;
  mockWsStatus.value = "connected";
  vi.clearAllMocks();

  const mod = await import("./SafetyDecisionFeed");
  SafetyDecisionFeed = mod.SafetyDecisionFeed;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render() {
  act(() => {
    root.render(React.createElement(SafetyDecisionFeed));
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("SafetyDecisionFeed", () => {
  it("renders title and stream status", () => {
    render();
    expect(container.textContent).toContain("Live Safety Decisions");
    expect(container.textContent).toContain("Stream Live");
  });

  it("shows loading state", () => {
    mockDecisions.isLoading = true;
    render();
    const pulses = container.querySelectorAll(".animate-pulse");
    expect(pulses.length).toBeGreaterThan(0);
  });

  it("shows empty state when no decisions", () => {
    mockDecisions.decisions = [];
    render();
    expect(container.textContent).toContain("No safety decisions yet");
  });

  it("shows error state when error and no decisions", () => {
    mockDecisions.isError = true;
    mockDecisions.decisions = [];
    render();
    expect(container.textContent).toContain("Unable to load safety decisions");
  });

  it("renders decision rows with topic and badge", () => {
    mockDecisions.decisions = [
      makeDecision({ id: "d1", topic: "sys.job.submit", decision: "allow" }),
      makeDecision({ id: "d2", topic: "deploy.prod", decision: "deny" }),
    ];
    render();
    expect(container.textContent).toContain("sys.job.submit");
    expect(container.textContent).toContain("deploy.prod");
    expect(container.textContent).toContain("Allow");
    expect(container.textContent).toContain("Deny");
  });

  it("shows summary counts grid when decisions exist", () => {
    mockDecisions.decisions = [
      makeDecision({ id: "d1", decision: "allow" }),
      makeDecision({ id: "d2", decision: "allow" }),
      makeDecision({ id: "d3", decision: "deny" }),
      makeDecision({ id: "d4", decision: "require_approval" }),
    ];
    render();
    // Summary grid labels
    expect(container.textContent).toContain("Allow");
    expect(container.textContent).toContain("Deny");
    expect(container.textContent).toContain("Approval");
    expect(container.textContent).toContain("Throttle");
  });

  it("shows matched rule when present", () => {
    mockDecisions.decisions = [
      makeDecision({ id: "d1", matchedRule: "prod-write-block" }),
    ];
    render();
    expect(container.textContent).toContain("prod-write-block");
  });

  it("shows eval time when present", () => {
    mockDecisions.decisions = [
      makeDecision({ id: "d1", evalTimeMs: 42 }),
    ];
    render();
    expect(container.textContent).toContain("42ms");
  });

  it("shows stream offline status", () => {
    mockWsStatus.value = "disconnected";
    render();
    expect(container.textContent).toContain("Stream Offline");
  });

  it("shows reconnecting status", () => {
    mockWsStatus.value = "reconnecting";
    render();
    expect(container.textContent).toContain("Reconnecting");
  });

  it("shows refreshing message when fetching", () => {
    mockDecisions.decisions = [makeDecision({ id: "d1" })];
    mockDecisions.isFetching = true;
    render();
    expect(container.textContent).toContain("Refreshing safety decisions");
  });

  it("shows decision count badge", () => {
    mockDecisions.decisions = [
      makeDecision({ id: "d1" }),
      makeDecision({ id: "d2" }),
      makeDecision({ id: "d3" }),
    ];
    render();
    // The count badge shows total decisions
    expect(container.textContent).toContain("3");
  });
});
