/**
 * SecurityOverviewPage tests
 *
 * The Security Overview is rendered by HomePage.tsx (route: "/").
 * This test file provides coverage for the Security Overview surface:
 * KPI cards, attention sections, state handling, and dashboard widgets.
 */
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

const { mockStatus, mockWorkers } = vi.hoisted(() => ({
  mockStatus: {
    data: null as Record<string, unknown> | null,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
  mockWorkers: {
    data: null as Array<{ activeJobs?: number }> | null,
  },
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("../hooks/useStatus", () => ({
  useStatus: () => mockStatus,
}));

vi.mock("../hooks/useWorkers", () => ({
  useWorkers: () => mockWorkers,
}));

vi.mock("../hooks/usePageTitle", () => ({
  usePageTitle: vi.fn(),
}));

vi.mock("../components/MetricCard", () => ({
  MetricCard: ({ title, value }: { title: string; value: unknown }) =>
    React.createElement("div", { "data-testid": `metric-${title}` }, `${title}: ${value}`),
}));

vi.mock("../components/ui/Card", () => ({
  Card: ({ children, className }: { children: React.ReactNode; className?: string }) =>
    React.createElement("div", { "data-testid": "card", className }, children),
}));

vi.mock("../components/ui/Button", () => ({
  Button: ({ children, onClick }: { children: React.ReactNode; onClick?: () => void }) =>
    React.createElement("button", { onClick, "data-testid": "button" }, children),
}));

vi.mock("../components/home/SafetyDecisionFeed", () => ({
  SafetyDecisionFeed: () => React.createElement("div", { "data-testid": "safety-feed" }, "SafetyFeed"),
}));

vi.mock("../components/home/JobPipelineFunnel", () => ({
  JobPipelineFunnel: () => React.createElement("div", { "data-testid": "pipeline" }, "Pipeline"),
}));

vi.mock("../components/home/PoolUtilizationGrid", () => ({
  PoolUtilizationGrid: () => React.createElement("div", { "data-testid": "pools" }, "Pools"),
}));

vi.mock("../components/home/EventTimeline", () => ({
  EventTimeline: () => React.createElement("div", { "data-testid": "timeline" }, "Timeline"),
}));

vi.mock("../components/home/ActiveWorkflowCards", () => ({
  ActiveWorkflowCards: () => React.createElement("div", { "data-testid": "workflows" }, "Workflows"),
}));

vi.mock("../components/home/DLQSummary", () => ({
  DLQSummary: () => React.createElement("div", { "data-testid": "dlq" }, "DLQ"),
}));

vi.mock("../components/home/CircuitBreakerCard", () => ({
  CircuitBreakerCard: () => null,
}));

vi.mock("../components/home/RateLimiterModeBadge", () => ({
  RateLimiterModeBadge: () => null,
}));

vi.mock("../components/home/QuickActions", () => ({
  QuickActions: () => React.createElement("div", { "data-testid": "quick-actions" }, "Actions"),
}));

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let SecurityOverview: React.ComponentType;

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  mockStatus.data = null;
  mockStatus.isLoading = false;
  mockStatus.isError = false;
  mockStatus.refetch = vi.fn();
  mockWorkers.data = null;
  vi.clearAllMocks();

  // Security Overview is implemented as HomePage
  const mod = await import("./HomePage");
  SecurityOverview = mod.default;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render() {
  act(() => {
    root.render(React.createElement(SecurityOverview));
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("SecurityOverviewPage (HomePage)", () => {
  it("renders Command Center heading", () => {
    render();
    expect(container.textContent).toContain("Command Center");
  });

  it("shows skeleton grid while loading", () => {
    mockStatus.isLoading = true;
    render();
    const pulses = container.querySelectorAll(".animate-pulse");
    expect(pulses.length).toBeGreaterThan(0);
  });

  it("shows error state with retry button on failure", () => {
    mockStatus.isError = true;
    render();
    expect(container.textContent).toContain("Failed to load system status");
    const retryBtn = container.querySelector("[data-testid='button']");
    expect(retryBtn).toBeTruthy();
    expect(retryBtn?.textContent).toContain("Retry");
  });

  it("calls refetch when retry clicked", () => {
    mockStatus.isError = true;
    render();
    const retryBtn = container.querySelector("[data-testid='button']") as HTMLButtonElement;
    act(() => retryBtn.click());
    expect(mockStatus.refetch).toHaveBeenCalledTimes(1);
  });

  it("renders all KPI metric cards when loaded", () => {
    mockStatus.data = {
      nats: { connected: true },
      redis: { ok: true },
      uptime_seconds: 3600,
      workers: { count: 5 },
    };
    mockWorkers.data = [{ activeJobs: 2 }, { activeJobs: 1 }];
    render();

    expect(container.querySelector("[data-testid='metric-Workers']")).toBeTruthy();
    expect(container.querySelector("[data-testid='metric-Active Jobs']")).toBeTruthy();
    expect(container.querySelector("[data-testid='metric-NATS']")).toBeTruthy();
    expect(container.querySelector("[data-testid='metric-Redis']")).toBeTruthy();
    expect(container.querySelector("[data-testid='metric-Uptime']")).toBeTruthy();
  });

  it("shows correct worker count from workers array", () => {
    mockStatus.data = { nats: { connected: true }, redis: { ok: true }, uptime_seconds: 0, workers: { count: 0 } };
    mockWorkers.data = [{ activeJobs: 0 }, { activeJobs: 0 }, { activeJobs: 0 }];
    render();
    const card = container.querySelector("[data-testid='metric-Workers']");
    expect(card?.textContent).toContain("3");
  });

  it("shows active jobs sum from workers", () => {
    mockStatus.data = { nats: { connected: true }, redis: { ok: true }, uptime_seconds: 0, workers: { count: 2 } };
    mockWorkers.data = [{ activeJobs: 3 }, { activeJobs: 7 }];
    render();
    const card = container.querySelector("[data-testid='metric-Active Jobs']");
    expect(card?.textContent).toContain("10");
  });

  it("shows NATS Connected when connected", () => {
    mockStatus.data = { nats: { connected: true }, redis: { ok: true }, uptime_seconds: 0, workers: { count: 0 } };
    render();
    const card = container.querySelector("[data-testid='metric-NATS']");
    expect(card?.textContent).toContain("Connected");
  });

  it("shows NATS Disconnected when not connected", () => {
    mockStatus.data = { nats: { connected: false }, redis: { ok: true }, uptime_seconds: 0, workers: { count: 0 } };
    render();
    const card = container.querySelector("[data-testid='metric-NATS']");
    expect(card?.textContent).toContain("Disconnected");
  });

  it("shows Redis OK/Degraded status", () => {
    mockStatus.data = { nats: { connected: true }, redis: { ok: false }, uptime_seconds: 0, workers: { count: 0 } };
    render();
    const card = container.querySelector("[data-testid='metric-Redis']");
    expect(card?.textContent).toContain("Degraded");
  });

  it("formats uptime correctly (days + hours)", () => {
    mockStatus.data = { nats: { connected: true }, redis: { ok: true }, uptime_seconds: 90061, workers: { count: 0 } };
    render();
    const card = container.querySelector("[data-testid='metric-Uptime']");
    // 90061s = 1d 1h
    expect(card?.textContent).toContain("1d 1h");
  });

  it("renders all dashboard widget sections", () => {
    mockStatus.data = { nats: { connected: true }, redis: { ok: true }, uptime_seconds: 0, workers: { count: 0 } };
    render();
    expect(container.querySelector("[data-testid='safety-feed']")).toBeTruthy();
    expect(container.querySelector("[data-testid='pipeline']")).toBeTruthy();
    expect(container.querySelector("[data-testid='pools']")).toBeTruthy();
    expect(container.querySelector("[data-testid='dlq']")).toBeTruthy();
    expect(container.querySelector("[data-testid='timeline']")).toBeTruthy();
    expect(container.querySelector("[data-testid='workflows']")).toBeTruthy();
    expect(container.querySelector("[data-testid='quick-actions']")).toBeTruthy();
  });

  it("still renders independent widget sections during loading", () => {
    mockStatus.isLoading = true;
    render();
    // Widget sections have their own queries — they render regardless of status loading
    expect(container.querySelector("[data-testid='safety-feed']")).toBeTruthy();
  });

  it("still renders independent widget sections during error", () => {
    mockStatus.isError = true;
    render();
    // Error only affects KPI strip — widget sections are independent
    expect(container.querySelector("[data-testid='safety-feed']")).toBeTruthy();
  });
});
