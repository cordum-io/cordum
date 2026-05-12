import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Job, Worker } from "@/api/types";
vi.mock("@/config/flags", () => ({
  FEATURE_FLAGS: {
    delegationDashboard: true,
  },
}));
import AgentDetailPage from "./AgentDetailPage";

const { hookState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

  return {
    hookState: {
      worker: null as Worker | null,
      jobs: [] as Job[],
      bundles: [] as Array<Record<string, unknown>>,
      workerLoading: false,
      workerError: null as Error | null,
      jobsLoading: false,
      jobsError: null as Error | null,
      refetchJobs: vi.fn(),
      invalidateQueries: vi.fn(),
    },
  };
});

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQueryClient: () => ({
      invalidateQueries: hookState.invalidateQueries,
    }),
  };
});

vi.mock("@/hooks/useWorkers", () => ({
  useWorker: () => ({
    data: hookState.worker,
    isLoading: hookState.workerLoading,
    error: hookState.workerError,
  }),
  useWorkerJobs: () => ({
    data: hookState.jobs,
    isLoading: hookState.jobsLoading,
    isError: Boolean(hookState.jobsError),
    error: hookState.jobsError,
    refetch: hookState.refetchJobs,
  }),
}));

vi.mock("@/hooks/usePolicies", () => ({
  usePolicyBundles: () => ({
    data: { items: hookState.bundles },
  }),
}));

vi.mock("@/components/delegations/AgentDelegationsPanel", () => ({
  AgentDelegationsPanel: ({ agentId }: { agentId: string }) => (
    <div data-testid="agent-delegations-panel">delegations for {agentId}</div>
  ),
}));

vi.mock("@/components/agents/AgentIdentityPanel", () => ({
  default: ({ agentId }: { agentId: string }) => (
    <div data-testid="agent-identity-panel">identity for {agentId}</div>
  ),
}));

vi.mock("recharts", () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => null,
  XAxis: () => null,
  YAxis: () => null,
  Tooltip: () => null,
  CartesianGrid: () => null,
}));

function makeWorker(overrides: Partial<Worker> = {}): Worker {
  return {
    id: "worker-1",
    name: "Worker One",
    status: "idle",
    pool: "primary",
    capabilities: ["jobs.submit"],
    activeJobs: 1,
    capacity: 4,
    lastHeartbeat: "2026-04-21T09:00:00.000Z",
    cpuLoad: 12,
    memoryLoad: 24,
    version: "1.2.3",
    region: "us-east-1",
    type: "default",
    ...overrides,
  } as Worker;
}

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-1",
    type: "workflow",
    topic: "jobs.run",
    status: "succeeded",
    pool: "primary",
    capabilities: ["jobs.submit"],
    riskTags: [],
    metadata: {},
    createdAt: "2026-04-21T08:30:00.000Z",
    updatedAt: "2026-04-21T08:31:00.000Z",
    duration: 30_000,
    safetyDecision: {
      type: "allow",
      reason: "ok",
    },
    ...overrides,
  } as Job;
}

function renderPage(route = "/agents/worker-1") {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route path="/agents/:id" element={<AgentDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function click(element: Element | null) {
  if (!element) throw new Error("Expected element before clicking");
  act(() => {
    element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

describe("AgentDetailPage", () => {
  beforeEach(() => {
    hookState.worker = makeWorker();
    hookState.jobs = [makeJob()];
    hookState.bundles = [{ id: "bundle-1", name: "Default Bundle", status: "published", rule_count: 3 }];
    hookState.workerLoading = false;
    hookState.workerError = null;
    hookState.jobsLoading = false;
    hookState.jobsError = null;
    hookState.refetchJobs = vi.fn();
    hookState.invalidateQueries = vi.fn();
  });

  it("lazy-mounts the delegations tab content only when selected", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Safety Decisions");
      expect(container.querySelector('[data-testid="agent-delegations-panel"]')).toBeNull();

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Delegations"),
        ) ?? null,
      );

      expect(container.textContent).toContain("delegations for worker-1");
      expect(container.querySelector('[data-testid="agent-delegations-panel"]')).not.toBeNull();
    } finally {
      cleanup();
    }
  });

  it("renders the identity tab from the URL even when worker/job detail calls fail", () => {
    hookState.worker = null;
    hookState.workerError = new Error("worker not found");
    hookState.jobsError = new Error("jobs unavailable");

    const { container, cleanup } = renderPage("/agents/worker-1?tab=identity");

    try {
      expect(container.querySelector('[data-testid="agent-identity-panel"]')).not.toBeNull();
      expect(container.textContent).toContain("identity for worker-1");
      expect(container.textContent).not.toContain("Failed to load agent");
      expect(container.textContent).not.toContain("Failed to load agent jobs");
    } finally {
      cleanup();
    }
  });

  it("falls back to Overview for unknown tab query values", () => {
    const { container, cleanup } = renderPage("/agents/worker-1?tab=unknown");

    try {
      expect(container.textContent).toContain("Safety Decisions");
      expect(container.querySelector('[data-testid="agent-identity-panel"]')).toBeNull();
      expect(container.querySelector('[data-testid="agent-delegations-panel"]')).toBeNull();
    } finally {
      cleanup();
    }
  });
});
