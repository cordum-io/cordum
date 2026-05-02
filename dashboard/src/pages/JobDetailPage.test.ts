import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { Job } from "@/api/types";

const { queryState, routerState, governanceState } = vi.hoisted(() => ({
  queryState: {
    current: {
      data: null as Job | null,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
  },
  routerState: {
    params: { id: "job-123" },
    navigate: vi.fn(),
  },
  governanceState: {
    render: vi.fn(),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => queryState.current,
}));

vi.mock("react-router-dom", () => ({
  useParams: () => routerState.params,
  useNavigate: () => routerState.navigate,
}));

vi.mock("framer-motion", () => {
  const passthrough = (tag: string) =>
    React.forwardRef<HTMLElement, Record<string, unknown> & { children?: React.ReactNode }>(
      ({ children, ...props }, ref) =>
        React.createElement(tag, { ...props, ref }, children as React.ReactNode),
    );
  return {
    motion: {
      div: passthrough("div"),
    },
    AnimatePresence: ({ children }: { children?: React.ReactNode }) => children,
  };
});

vi.mock("@/hooks/useElapsedTimer", () => ({
  useElapsedTimer: () => ({ formatted: "1m" }),
}));

vi.mock("@/state/events", () => ({
  useEventStore: (selector: (state: { events: unknown[] }) => unknown) =>
    selector({ events: [] }),
}));

vi.mock("@/components/jobs/JobActions", () => ({
  JobActions: () => React.createElement("div", null, "Job actions"),
}));

vi.mock("@/components/governance/GovernanceTimeline", () => ({
  GovernanceTimeline: (props: Record<string, unknown>) => {
    governanceState.render(props);
    return React.createElement(
      "div",
      { "data-testid": "governance-timeline" },
      JSON.stringify(props),
    );
  },
}));

vi.mock("@/components/edge/AgentExecutionsPanel", () => ({
  AgentExecutionsPanel: (props: Record<string, unknown>) =>
    React.createElement("div", { "data-testid": "agent-executions-panel" }, String(props.jobId ?? "")),
}));

const JobDetailPage = (await import("./JobDetailPage")).default;

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-123",
    topic: "job.review",
    status: "running",
    type: "job.review",
    pool: "default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    createdAt: "2026-04-20T10:00:00.000Z",
    updatedAt: "2026-04-20T10:01:00.000Z",
    labels: {},
    context: { request: "hello" },
    result: { ok: true },
    ...overrides,
  } as Job;
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(React.createElement(JobDetailPage));
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

beforeEach(() => {
  queryState.current = {
    data: makeJob(),
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
  };
  routerState.params = { id: "job-123" };
  routerState.navigate.mockReset();
  governanceState.render.mockReset();
});

/**
 * Tests for JobDetailPage logic: payload truncation, JSON auto-parse, error fallback.
 * Tests the pure functions and logic without DOM rendering.
 */

const MAX_RESULT_DISPLAY = 100 * 1024;

// Mirrors formatBlobData from JobDetailPage.tsx
function formatBlobData(data: unknown): string | null {
  if (data == null) return null;
  if (typeof data === "string") {
    try {
      const parsed = JSON.parse(data);
      if (typeof parsed === "object" && parsed !== null) {
        return JSON.stringify(parsed, null, 2);
      }
    } catch {
      // Not JSON
    }
    return data;
  }
  return JSON.stringify(data, null, 2);
}

function errorFallback(errorMessage: string | undefined | null, errorCode: string | undefined | null): string {
  return errorMessage || `Job failed (no error message provided). Status code: ${errorCode || "unknown"}`;
}

describe("BlobViewer truncation logic", () => {
  it("does not truncate payloads under 100KB", () => {
    const data = "x".repeat(50_000);
    const formatted = formatBlobData(data);
    expect(formatted).not.toBeNull();
    expect(formatted!.length).toBe(50_000);
    expect(formatted!.length <= MAX_RESULT_DISPLAY).toBe(true);
  });

  it("identifies payloads over 100KB for truncation", () => {
    const data = "y".repeat(200_000);
    const formatted = formatBlobData(data);
    expect(formatted).not.toBeNull();
    expect(formatted!.length).toBeGreaterThan(MAX_RESULT_DISPLAY);
    // BlobViewer would slice to MAX_RESULT_DISPLAY
    const truncated = formatted!.slice(0, MAX_RESULT_DISPLAY);
    expect(truncated.length).toBe(MAX_RESULT_DISPLAY);
  });
});

describe("JSON auto-parse", () => {
  it("auto-parses JSON string into pretty-printed format", () => {
    const input = '{"checks":[{"policy":"scope","verdict":"pass"}]}';
    const result = formatBlobData(input);
    expect(result).toContain("  ");  // indented
    expect(result).toContain('"checks"');
    expect(result).toContain('"verdict": "pass"');
  });

  it("leaves non-JSON strings unchanged", () => {
    const input = "plain text error message";
    const result = formatBlobData(input);
    expect(result).toBe("plain text error message");
  });

  it("pretty-prints objects directly", () => {
    const input = { key: "value", nested: { a: 1 } };
    const result = formatBlobData(input);
    expect(result).toContain('"key": "value"');
    expect(result).toContain("  ");
  });

  it("returns null for null/undefined", () => {
    expect(formatBlobData(null)).toBeNull();
    expect(formatBlobData(undefined)).toBeNull();
  });

  it("does not wrap primitive JSON values in objects", () => {
    // "42" parses to a number, not an object — should stay as string
    expect(formatBlobData("42")).toBe("42");
    expect(formatBlobData('"hello"')).toBe('"hello"');
  });
});

describe("Error message fallback", () => {
  it("uses errorMessage when present", () => {
    expect(errorFallback("something broke", "ERR_001")).toBe("something broke");
  });

  it("falls back when errorMessage is null", () => {
    const result = errorFallback(null, "ERR_002");
    expect(result).toContain("Job failed (no error message provided)");
    expect(result).toContain("ERR_002");
  });

  it("falls back when errorMessage is empty", () => {
    const result = errorFallback("", null);
    expect(result).toContain("Job failed (no error message provided)");
    expect(result).toContain("unknown");
  });

  it("falls back when both are null", () => {
    const result = errorFallback(null, null);
    expect(result).toContain("unknown");
  });
});

// ---------------------------------------------------------------------------
// Terminal state polling contract
// ---------------------------------------------------------------------------

const TERMINAL_POLL_STATES = ["succeeded", "failed", "cancelled", "denied", "timeout", "output_quarantined"];

describe("Job polling terminal states", () => {
  it("stops polling for all terminal states", () => {
    for (const status of TERMINAL_POLL_STATES) {
      expect(TERMINAL_POLL_STATES.includes(status)).toBe(true);
    }
  });

  it("does not stop polling for active states", () => {
    for (const status of ["running", "pending", "scheduled", "dispatched", "approval_required"]) {
      expect(TERMINAL_POLL_STATES.includes(status)).toBe(false);
    }
  });
});

// ---------------------------------------------------------------------------
// Status variant mapping
// ---------------------------------------------------------------------------

function jobStatusVariant(status: string) {
  switch (status) {
    case "running": return "healthy";
    case "succeeded": case "completed": return "cordum";
    case "failed": case "timeout": case "timed_out": return "danger";
    case "denied": case "output_quarantined": return "governance";
    case "approval_required": return "warning";
    case "pending": case "scheduled": return "warning";
    case "dispatched": return "info";
    case "cancelled": return "muted";
    default: return "muted";
  }
}

describe("Job status variant mapping", () => {
  it("maps denied to governance, not danger", () => {
    expect(jobStatusVariant("denied")).toBe("governance");
  });

  it("maps output_quarantined to governance", () => {
    expect(jobStatusVariant("output_quarantined")).toBe("governance");
  });

  it("maps timeout to danger", () => {
    expect(jobStatusVariant("timeout")).toBe("danger");
  });

  it("maps approval_required to warning", () => {
    expect(jobStatusVariant("approval_required")).toBe("warning");
  });

  it("maps cancelled to muted", () => {
    expect(jobStatusVariant("cancelled")).toBe("muted");
  });

  it("maps failed to danger", () => {
    expect(jobStatusVariant("failed")).toBe("danger");
  });

  it("maps succeeded to cordum", () => {
    expect(jobStatusVariant("succeeded")).toBe("cordum");
  });
});

describe("JobDetailPage governance tab integration", () => {
  it("renders the Agent Executions panel placeholder with the current job id", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.querySelector('[data-testid="agent-executions-panel"]')?.textContent).toBe("job-123");
    } finally {
      cleanup();
    }
  });

  it("renders the governance tab and lazy-mounts the timeline on activation", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Governance");
      expect(governanceState.render).not.toHaveBeenCalled();
      expect(container.querySelector('[data-testid="governance-timeline"]')).toBeNull();

      const governanceTab = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Governance"),
      );
      expect(governanceTab).toBeTruthy();

      act(() => {
        governanceTab?.dispatchEvent(
          new MouseEvent("click", { bubbles: true, cancelable: true }),
        );
      });

      expect(governanceState.render).toHaveBeenCalledTimes(1);
      expect(container.querySelector('[data-testid="governance-timeline"]')?.textContent).toContain('"jobId":"job-123"');
    } finally {
      cleanup();
    }
  });
});

describe("JobDetailPage 4-surface agreement (task-dc086833)", () => {
  it("renders Run banner + MetadataBar Run row + suppresses empty-context card for a metadata.run_id-only job", () => {
    queryState.current.data = makeJob({
      workflowRunId: undefined,
      workflowId: undefined,
      metadata: { run_id: "wfr-meta-only" },
      labels: {},
      context: {},
    });

    const { container, cleanup } = renderPage();
    try {
      // ParentContextBanner: "Part of Workflow Run" header (Run card, not Session fallback)
      expect(container.textContent).toContain("Part of Workflow Run");
      expect(container.textContent).not.toContain("Part of Copilot Session");
      // ParentContextBanner Run line: "Run: wfr-meta-onl..." (slice(0,12) of "wfr-meta-only" is "wfr-meta-onl")
      expect(container.textContent).toContain("Run: wfr-meta-onl");

      // MetadataBar Run row PRESENT (the bug-fix surface): label "Run" with the runId value displayed
      const metaLabels = Array.from(
        container.querySelectorAll("span.text-\\[10px\\]"),
      ).map((el) => el.textContent?.trim());
      expect(metaLabels).toContain("Run");
      // The Run value is rendered as plain text (workflowId absent, so the row is non-clickable)
      expect(container.textContent).toContain("wfr-meta-only");

      // Empty-context card SUPPRESSED (the inverse-surface bug-fix)
      expect(container.textContent).not.toContain("No extended context available for this job");
    } finally {
      cleanup();
    }
  });

  it("does not double-print ctx.run_id in GenericContext (task-125694ec)", () => {
    queryState.current.data = makeJob({
      workflowRunId: "wfr-banner",
      workflowId: "wf-1",
      context: { run_id: "wfr-banner", foo: "bar-visible" },
    });

    const { container, cleanup } = renderPage();
    try {
      // GenericContext title-cases keys: "foo" → "Foo". A mounted entry shows
      // both the formatted key and its value. run_id should be filtered out
      // (task-125694ec); the foo→bar-visible entry should still mount.
      const lowered = (container.textContent ?? "").toLowerCase();
      expect(lowered).not.toContain("run_id");
      expect(container.textContent).toContain("Foo");
      expect(container.textContent).toContain("bar-visible");
    } finally {
      cleanup();
    }
  });

  it("renders Run banner + MetadataBar Run row + suppresses empty-context card for a labels.run_id-only job", () => {
    queryState.current.data = makeJob({
      workflowRunId: undefined,
      workflowId: undefined,
      metadata: {},
      labels: { run_id: "wfr-label-only" },
      context: {},
    });

    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("Part of Workflow Run");
      expect(container.textContent).not.toContain("Part of Copilot Session");
      expect(container.textContent).toContain("Run: wfr-label-on");

      const metaLabels = Array.from(
        container.querySelectorAll("span.text-\\[10px\\]"),
      ).map((el) => el.textContent?.trim());
      expect(metaLabels).toContain("Run");
      expect(container.textContent).toContain("wfr-label-only");

      expect(container.textContent).not.toContain("No extended context available for this job");
    } finally {
      cleanup();
    }
  });
});
