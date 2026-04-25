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

const { OriginPill } = await import("./JobsPage");

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

function renderPill(job: Job): { container: HTMLDivElement; cleanup: () => void } {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(React.createElement(OriginPill, { job }));
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("OriginPill — workflowId guard against /workflows/all/runs/X (task-22a85a34)", () => {
  beforeEach(() => {
    navigateMock.mockReset();
  });

  it("renders Run pill and navigates to /workflows/<workflowId>/runs/<runId> when both ids present", () => {
    const { container, cleanup } = renderPill(
      makeJob({ workflowRunId: "wfr-abc123xy", workflowId: "wf-1" }),
    );
    try {
      const button = container.querySelector("button");
      expect(button).not.toBeNull();
      expect(button?.textContent).toContain("Run:");
      expect(button?.textContent).toContain("wfr-abc1"); // slice(0,8) of "wfr-abc123xy" = "wfr-abc1"
      act(() => {
        button?.click();
      });
      expect(navigateMock).toHaveBeenCalledTimes(1);
      expect(navigateMock).toHaveBeenCalledWith("/workflows/wf-1/runs/wfr-abc123xy");
    } finally {
      cleanup();
    }
  });

  it("falls through to Direct pill when runId is set but workflowId is absent (no session_id)", () => {
    const { container, cleanup } = renderPill(
      makeJob({ metadata: { run_id: "wfr-abc123xy" } }),
    );
    try {
      // Direct pill is a <span>, not a <button>
      expect(container.querySelector("button")).toBeNull();
      const span = container.querySelector("span");
      expect(span?.textContent).toContain("Direct");
    } finally {
      cleanup();
    }
  });

  it("falls through to Session pill when runId is set but workflowId is absent (and session_id is present)", () => {
    const { container, cleanup } = renderPill(
      makeJob({
        metadata: { run_id: "wfr-abc123xy" },
        labels: { session_id: "sess-abc123xy" },
      }),
    );
    try {
      const button = container.querySelector("button");
      expect(button).not.toBeNull();
      expect(button?.textContent).toContain("Session:");
      expect(button?.textContent).toContain("sess-abc");
      act(() => {
        button?.click();
      });
      expect(navigateMock).toHaveBeenCalledTimes(1);
      expect(navigateMock).toHaveBeenCalledWith("/copilot/sessions/sess-abc123xy");
    } finally {
      cleanup();
    }
  });

  it("never produces /workflows/all/runs/X for any combination of metadata/labels run_id without workflowId", () => {
    const cases: Partial<Job>[] = [
      { metadata: { run_id: "wfr-x" } },
      { labels: { run_id: "wfr-x" } },
      { metadata: { run_id: "wfr-x" }, labels: { run_id: "wfr-y" } },
    ];
    for (const overrides of cases) {
      const { container, cleanup } = renderPill(makeJob(overrides));
      try {
        const button = container.querySelector("button");
        if (button) {
          act(() => {
            button.click();
          });
        }
        const callArgs = navigateMock.mock.calls.map((c) => c[0]);
        for (const arg of callArgs) {
          expect(String(arg)).not.toMatch(/\/workflows\/all\//);
        }
      } finally {
        navigateMock.mockReset();
        cleanup();
      }
    }
  });
});
