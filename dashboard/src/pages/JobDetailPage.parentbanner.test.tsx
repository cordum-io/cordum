import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { Job } from "@/api/types";

const { navigateMock } = vi.hoisted(() => ({
  navigateMock: vi.fn(),
}));

vi.mock("react-router-dom", () => ({
  useNavigate: () => navigateMock,
}));

vi.mock("framer-motion", () => {
  const passthrough = (tag: string) =>
    React.forwardRef<HTMLElement, Record<string, unknown> & { children?: React.ReactNode }>(
      ({ children, ...props }, ref) =>
        React.createElement(tag, { ...props, ref }, children as React.ReactNode),
    );
  return {
    motion: { div: passthrough("div") },
  };
});

const { ParentContextBanner } = await import("./JobDetailPage");

function makeJob(overrides: Partial<Job> = {}): Job {
  return {
    id: "job-test",
    topic: "topic.test",
    status: "succeeded",
    type: "topic.test",
    pool: "default",
    capabilities: [],
    riskTags: [],
    metadata: {},
    labels: {},
    createdAt: "2026-04-25T00:00:00.000Z",
    updatedAt: "2026-04-25T00:00:01.000Z",
    ...overrides,
  } as Job;
}

function renderBanner(job: Job): { container: HTMLDivElement; cleanup: () => void } {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(React.createElement(ParentContextBanner, { job }));
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function findViewParentButton(container: HTMLElement): HTMLButtonElement | null {
  return Array.from(container.querySelectorAll("button")).find((b) =>
    b.textContent?.includes("View Parent"),
  ) as HTMLButtonElement | null;
}

describe("ParentContextBanner — workflowId guard against /workflows/all/runs/X (task-22a85a34)", () => {
  beforeEach(() => {
    navigateMock.mockReset();
  });

  it("returns null when neither runId nor sessionId present", () => {
    const { container, cleanup } = renderBanner(makeJob());
    try {
      // motion.div root would render a div if banner is present
      expect(container.querySelector("button")).toBeNull();
    } finally {
      cleanup();
    }
  });

  it("renders Run banner and View Parent navigates to /workflows/<workflowId>/runs/<runId> when both present", () => {
    const { container, cleanup } = renderBanner(
      makeJob({ workflowRunId: "wfr-abc123xy", workflowId: "wf-1" }),
    );
    try {
      const btn = findViewParentButton(container);
      expect(btn).not.toBeNull();
      expect(btn?.disabled).toBe(false);
      act(() => {
        btn?.click();
      });
      expect(navigateMock).toHaveBeenCalledWith("/workflows/wf-1/runs/wfr-abc123xy");
    } finally {
      cleanup();
    }
  });

  it("renders Run informational card but does NOT navigate to /workflows/all/... when workflowId absent and no session", () => {
    const { container, cleanup } = renderBanner(
      makeJob({ metadata: { run_id: "wfr-abc123xy" } }),
    );
    try {
      const btn = findViewParentButton(container);
      expect(btn).not.toBeNull();
      // Button is disabled when neither (run+workflow) nor session can be navigated to
      expect(btn?.disabled).toBe(true);
      act(() => {
        btn?.click();
      });
      expect(navigateMock).not.toHaveBeenCalled();
    } finally {
      cleanup();
    }
  });

  it("falls back to Session navigate when runId is present but workflowId is absent and session_id is present", () => {
    const { container, cleanup } = renderBanner(
      makeJob({
        metadata: { run_id: "wfr-abc123xy", session_id: "sess-abc123xy" },
      }),
    );
    try {
      const btn = findViewParentButton(container);
      expect(btn).not.toBeNull();
      expect(btn?.disabled).toBe(false);
      act(() => {
        btn?.click();
      });
      expect(navigateMock).toHaveBeenCalledTimes(1);
      const arg = String(navigateMock.mock.calls[0][0]);
      expect(arg).not.toMatch(/\/workflows\/all\//);
      expect(arg).toBe("/copilot/sessions/sess-abc123xy");
    } finally {
      cleanup();
    }
  });
});
