import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkflowRun } from "@/api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { routerState, workflowState, chatState, governanceState } = vi.hoisted(
  () => ({
    routerState: {
      params: { workflowId: "wf-1", runId: "run-1" },
      navigate: vi.fn(),
    },
    workflowState: {
      run: {
        data: null as WorkflowRun | null,
        isLoading: false,
        error: null as Error | null,
      },
      timeline: {
        data: [],
        isLoading: false,
      },
      cancelMutation: {
        mutate: vi.fn(),
        isPending: false,
      },
      rerunMutation: {
        mutate: vi.fn(),
        isPending: false,
      },
    },
    chatState: {
      query: {
        data: [],
        error: null,
      },
      queryClient: {
        invalidateQueries: vi.fn(),
      },
      mutation: {
        mutate: vi.fn(),
        isPending: false,
      },
    },
    governanceState: {
      render: vi.fn(),
    },
  }),
);

vi.mock("react-router-dom", () => ({
  useParams: () => routerState.params,
  useNavigate: () => routerState.navigate,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => chatState.query,
  useMutation: () => chatState.mutation,
  useQueryClient: () => chatState.queryClient,
}));

vi.mock("@/hooks/useWorkflows", () => ({
  useRun: () => workflowState.run,
  useRunTimeline: () => workflowState.timeline,
  useCancelRun: () => workflowState.cancelMutation,
  useRerunRun: () => workflowState.rerunMutation,
}));

vi.mock("framer-motion", () => {
  const passthrough = (tag: string) =>
    React.forwardRef<HTMLElement, Record<string, unknown> & { children?: React.ReactNode }>(
      ({ children, ...props }, ref) =>
        React.createElement(tag, { ...props, ref }, children),
    );

  return {
    motion: {
      div: passthrough("div"),
    },
    AnimatePresence: ({ children }: { children?: React.ReactNode }) =>
      React.createElement(React.Fragment, null, children),
    useReducedMotion: () => false,
  };
});

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

const WorkflowRunDetailPage = (await import("./RunDetailPage")).default;

function makeRun(overrides: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    id: "run-1",
    workflowId: "wf-1",
    status: "running",
    steps: [
      {
        id: "step-1",
        name: "First step",
        type: "worker",
        status: "running",
      },
    ],
    startedAt: "2026-04-20T10:00:00.000Z",
    updatedAt: "2026-04-20T10:01:00.000Z",
    ...overrides,
  };
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(<WorkflowRunDetailPage />);
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
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(),
  });
  workflowState.run = {
    data: makeRun(),
    isLoading: false,
    error: null,
  };
  workflowState.timeline = {
    data: [],
    isLoading: false,
  };
  workflowState.cancelMutation = {
    mutate: vi.fn(),
    isPending: false,
  };
  workflowState.rerunMutation = {
    mutate: vi.fn(),
    isPending: false,
  };
  chatState.query = {
    data: [],
    error: null,
  };
  chatState.mutation = {
    mutate: vi.fn(),
    isPending: false,
  };
  governanceState.render.mockReset();
  routerState.navigate.mockReset();
});

describe("WorkflowRunDetailPage governance tab integration", () => {
  it("adds the governance tab and only mounts the timeline after activation", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.textContent).toContain("Governance");
      expect(container.textContent).toContain("Run Chat");
      expect(governanceState.render).not.toHaveBeenCalled();

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
      expect(container.querySelector('[data-testid="governance-timeline"]')?.textContent).toContain('"runId":"run-1"');
      expect(container.querySelector('[data-testid="governance-timeline"]')?.textContent).toContain("This workflow run has not evaluated any policy rules yet.");
    } finally {
      cleanup();
    }
  });
});
