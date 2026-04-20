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
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Job } from "../api/types";

// ---------------------------------------------------------------------------
// Mock state
// ---------------------------------------------------------------------------

const { mockJobResult, mockDecisionsResult, mockEventsResult, mockFindingsResult, mockStreamResult, mockPermission, mockApproveMutate, mockRejectMutate, mockRemediateMutate } = vi.hoisted(() => ({
  mockJobResult: {
    data: undefined as Job | undefined,
    isLoading: false,
    isError: false,
  },
  mockDecisionsResult: { data: undefined as unknown[] | undefined },
  mockEventsResult: { data: undefined as unknown[] | undefined },
  mockFindingsResult: { data: undefined as unknown[] | undefined },
  mockStreamResult: {
    progressEvents: [] as unknown[],
    latestPercent: null as number | null,
    isConnected: false,
    lastHeartbeat: null as string | null,
  },
  mockPermission: { allowed: false, userRoles: [] as string[] },
  mockApproveMutate: vi.fn(),
  mockRejectMutate: vi.fn(),
  mockRemediateMutate: vi.fn(),
}));

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

vi.mock("../hooks/useJobs", () => ({
  useJob: () => mockJobResult,
  useJobDecisions: () => mockDecisionsResult,
  useJobEvents: () => mockEventsResult,
  useRemediateJob: () => ({ mutate: mockRemediateMutate, isPending: false }),
  useCancelJob: () => ({ mutate: vi.fn(), isPending: false }),
  useRetryJob: () => ({ mutate: vi.fn(), isPending: false }),
  useSubmitJob: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("../hooks/useOutputPolicy", () => ({
  useOutputFindings: () => mockFindingsResult,
}));

vi.mock("../hooks/useApprovals", () => ({
  useApproveJob: () => ({ mutate: mockApproveMutate, isPending: false }),
  useRejectJob: () => ({ mutate: mockRejectMutate, isPending: false }),
}));

vi.mock("../hooks/useJobStream", () => ({
  useJobStream: () => mockStreamResult,
}));

vi.mock("../hooks/usePermission", () => ({
  usePermission: () => mockPermission,
}));

vi.mock("../hooks/usePageTitle", () => ({
  usePageTitle: () => {},
}));

vi.mock("../hooks/useAuthConfig", () => ({
  useAuthConfig: () => ({ data: null }),
}));

vi.mock("../state/config", () => ({
  useConfigStore: Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ apiBaseUrl: "/api/v1", apiKey: "", user: null, principalRole: "" }),
    { getState: () => ({ apiBaseUrl: "/api/v1", apiKey: "", user: null, principalRole: "" }) },
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

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock("../api/transform", () => ({
  buildUnifiedTimeline: (events: unknown[]) =>
    events.length > 0
      ? events
      : [
          { timestamp: "2026-02-13T12:00:00.000Z", kind: "submitted", title: "Job Submitted", status: "completed" },
        ],
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "j-test-12345678",
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

function renderPage(jobId = "j-test-12345678") {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[`/jobs/${jobId}`]}>
          <Routes>
            <Route path="/jobs/:id" element={<JobDetailPage />} />
            <Route path="/jobs" element={<div>Jobs List</div>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
}

// Lazy import to let mocks install first
let JobDetailPage: React.ComponentType;

beforeEach(async () => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  // Reset mock state
  mockJobResult.data = undefined;
  mockJobResult.isLoading = false;
  mockJobResult.isError = false;
  mockDecisionsResult.data = undefined;
  mockEventsResult.data = undefined;
  mockFindingsResult.data = undefined;
  mockStreamResult.progressEvents = [];
  mockStreamResult.latestPercent = null;
  mockStreamResult.isConnected = false;
  mockStreamResult.lastHeartbeat = null;
  mockPermission.allowed = false;
  mockPermission.userRoles = [];
  vi.clearAllMocks();

  const mod = await import("./JobDetailPage");
  JobDetailPage = mod.default;
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("JobDetailPage", () => {
  it("renders loading state", () => {
    mockJobResult.isLoading = true;
    renderPage();
    expect(container.textContent).toContain("Loading job...");
  });

  it("renders error state for missing job", () => {
    mockJobResult.isError = true;
    renderPage();
    expect(container.textContent).toContain("Job not found or failed to load");
    expect(container.textContent).toContain("Back to Jobs");
  });

  it("renders header with job ID and status for succeeded job", () => {
    mockJobResult.data = makeJob({ status: "succeeded" });
    renderPage();
    expect(container.textContent).toContain("j-test-1");
    expect(container.textContent).toContain("sys.job.submit");
    expect(container.textContent).toContain("default-pool");
  });

  it("renders timeline for running job", () => {
    mockJobResult.data = makeJob({ status: "running" });
    mockEventsResult.data = [
      { timestamp: "2026-02-13T12:00:00.000Z", kind: "submitted", title: "Job Submitted", status: "completed" },
      { timestamp: "2026-02-13T12:01:00.000Z", kind: "executing", title: "Executing", status: "current" },
    ];
    mockStreamResult.isConnected = true;
    mockStreamResult.latestPercent = 60;
    renderPage();

    expect(container.textContent).toContain("Job Submitted");
    expect(container.textContent).toContain("Executing");
  });

  it("renders safety decision badge when present", () => {
    mockJobResult.data = makeJob({
      status: "denied",
      safetyDecision: { type: "deny", reason: "Blocked" },
    });
    renderPage();
    expect(container.textContent).toContain("deny");
  });

  it("renders workflow context link when workflowRunId present", () => {
    mockJobResult.data = makeJob({
      workflowId: "wf-1",
      workflowRunId: "run-1",
    });
    renderPage();
    expect(container.textContent).toContain("Part of Workflow");
    expect(container.textContent).toContain("run-1");
    expect(container.querySelector("a[href*='workflows']")).toBeTruthy();
  });

  it("renders audit trail link when traceId present", () => {
    mockJobResult.data = makeJob({ traceId: "trace-abc" });
    renderPage();
    expect(container.textContent).toContain("audit trail");
    const auditLink = container.querySelector("a[href*='audit']") as HTMLAnchorElement;
    expect(auditLink).toBeTruthy();
    expect(auditLink.href).toContain("correlationId=trace-abc");
  });

  it("hides workflow and audit sections when not applicable", () => {
    mockJobResult.data = makeJob({ workflowRunId: undefined, workflowId: undefined, traceId: undefined });
    renderPage();
    expect(container.textContent).not.toContain("Part of Workflow");
    expect(container.textContent).not.toContain("audit trail");
  });

  it("renders governance trace breadcrumb", () => {
    mockJobResult.data = makeJob({ status: "running", traceId: "trace-abc" });
    renderPage();
    expect(container.textContent).toContain("Action j-test-1");
    expect(container.textContent).toContain("Audit Trail");
  });
});
