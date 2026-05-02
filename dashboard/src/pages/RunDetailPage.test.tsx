import React, { act } from "react";
import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkflowRun } from "@/api/types";
import { renderWithProviders } from "@/test-utils/render";

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

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useParams: () => routerState.params,
    useNavigate: () => routerState.navigate,
  };
});

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: () => chatState.query,
    useMutation: () => chatState.mutation,
    useQueryClient: () => chatState.queryClient,
  };
});

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

vi.mock("@/components/edge/AgentExecutionsPanel", () => ({
  AgentExecutionsPanel: (props: Record<string, unknown>) =>
    React.createElement("div", { "data-testid": "agent-executions-panel" }, String(props.workflowRunId ?? "")),
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
  const result = renderWithProviders(<WorkflowRunDetailPage />, {
    initialEntries: ["/workflows/wf-1/runs/run-1"],
  });
  return {
    container: result.container,
    cleanup: () => {
      result.unmount();
    },
  };
}

function keydown(element: Element | null, key: string) {
  if (!element)
    throw new Error("Expected element to exist before dispatching key");
  act(() => {
    element.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    );
  });
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
  it("passes the current run id into the Agent Executions panel", () => {
    const { container, cleanup } = renderPage();

    try {
      expect(container.querySelector('[data-testid="agent-executions-panel"]')?.textContent).toBe("run-1");
    } finally {
      cleanup();
    }
  });

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

describe("WorkflowRunDetailPage step list accessibility", () => {
  beforeEach(() => {
    workflowState.run = {
      data: makeRun({
        steps: [
          { id: "step-1", name: "Compile", type: "worker", status: "running" },
          { id: "step-2", name: "Approval", type: "approval", status: "waiting" },
          { id: "step-3", name: "Delay", type: "delay", status: "pending" },
        ],
      }),
      isLoading: false,
      error: null,
    };
  });

  it("renders the step list as a labeled listbox with three focusable options", () => {
    const { cleanup } = renderPage();

    try {
      const listbox = screen.getByRole("listbox", { name: "Run steps" });
      const options = screen.getAllByRole("option");

      expect(listbox).not.toBeNull();
      expect(options).toHaveLength(3);
      expect(options[0]?.getAttribute("aria-label")).toBe("Step 1: Compile, running");
      expect(options[1]?.getAttribute("aria-label")).toBe("Step 2: Approval, waiting");
      expect(options[2]?.getAttribute("aria-label")).toBe("Step 3: Delay, pending");
      expect(options.every((option) => (option as HTMLElement).tabIndex === 0)).toBe(true);
    } finally {
      cleanup();
    }
  });

  it("marks only the selected step option with aria-selected", () => {
    const { cleanup } = renderPage();

    try {
      const options = screen.getAllByRole("option");
      expect(options[0]?.getAttribute("aria-selected")).toBe("false");
      expect(options[1]?.getAttribute("aria-selected")).toBe("true");
      expect(options[2]?.getAttribute("aria-selected")).toBe("false");
    } finally {
      cleanup();
    }
  });

  it("selects a focused step when Enter is pressed", () => {
    const { cleanup } = renderPage();

    try {
      const options = screen.getAllByRole("option");
      const approvalStep = options[1] as HTMLElement;

      approvalStep.focus();
      expect(document.activeElement).toBe(approvalStep);

      keydown(approvalStep, "Enter");

      expect(options[0]?.getAttribute("aria-selected")).toBe("false");
      expect(options[1]?.getAttribute("aria-selected")).toBe("true");
    } finally {
      cleanup();
    }
  });

  it("selects a focused step when Space is pressed", () => {
    const { cleanup } = renderPage();

    try {
      const options = screen.getAllByRole("option");
      const pendingStep = options[2] as HTMLElement;

      pendingStep.focus();
      expect(document.activeElement).toBe(pendingStep);

      keydown(pendingStep, " ");

      expect(options[0]?.getAttribute("aria-selected")).toBe("false");
      expect(options[2]?.getAttribute("aria-selected")).toBe("true");
    } finally {
      cleanup();
    }
  });

  it("keeps the keyboard focus ring classes on each step option", () => {
    const { cleanup } = renderPage();

    try {
      const options = screen.getAllByRole("option");
      expect(options[0]?.className).toContain("focus-visible:ring-2");
      expect(options[0]?.className).toContain("focus-visible:ring-cordum");
      expect(options[0]?.className).toContain("focus-visible:ring-offset-2");
      expect(options[0]?.className).toContain("focus-visible:outline-none");
    } finally {
      cleanup();
    }
  });
});
