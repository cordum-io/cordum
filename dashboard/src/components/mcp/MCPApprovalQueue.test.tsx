import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MCPApprovalQueue } from "./MCPApprovalQueue";
import { ApiError } from "../../api/client";
import type { McpApproval } from "../../hooks/useMcpApprovals";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    items: [] as McpApproval[],
    isLoading: false,
    isError: false,
    error: null as unknown,
    refetchCalls: 0,
    approveMutate: vi.fn(),
    rejectMutate: vi.fn(),
    approvePending: false,
    rejectPending: false,
  },
}));

vi.mock("../../hooks/useMcp", async () => {
  const actual = await vi.importActual<typeof import("../../hooks/useMcp")>(
    "../../hooks/useMcp",
  );
  return {
    ...actual,
    useMcpPendingApprovals: () => ({
      data: hookState.items,
      isLoading: hookState.isLoading,
      isError: hookState.isError,
      error: hookState.error,
      refetch: () => {
        hookState.refetchCalls += 1;
      },
    }),
    useApproveMcp: () => ({
      mutate: hookState.approveMutate,
      isPending: hookState.approvePending,
    }),
    useRejectMcp: () => ({
      mutate: hookState.rejectMutate,
      isPending: hookState.rejectPending,
    }),
  };
});

const { configState } = vi.hoisted(() => ({
  configState: { principalId: "user-1" },
}));
vi.mock("../../state/config", () => ({
  useConfigStore: <T,>(selector: (s: typeof configState) => T) => selector(configState),
}));

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

function makeApproval(overrides: Partial<McpApproval> = {}): McpApproval {
  return {
    id: "app-1",
    tenant: "acme",
    agent_id: "agent-1",
    tool_name: "cordum.dlq.retry",
    args_hash: "abcdef0123",
    requester: "user-2",
    status: "pending",
    created_at: Math.floor(Date.now() / 1000) - 30,
    expires_at: Math.floor(Date.now() / 1000) + 600,
    ...overrides,
  };
}

beforeEach(() => {
  hookState.items = [];
  hookState.isLoading = false;
  hookState.isError = false;
  hookState.error = null;
  hookState.approvePending = false;
  hookState.rejectPending = false;
  hookState.approveMutate.mockReset();
  hookState.rejectMutate.mockReset();
  hookState.refetchCalls = 0;
  configState.principalId = "user-1";
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
});

function render(node: React.ReactElement) {
  act(() => {
    root.render(node);
  });
}

describe("MCPApprovalQueue", () => {
  it("renders an empty state when there are no items", () => {
    render(<MCPApprovalQueue status="pending" />);
    expect(container.querySelector('[data-testid="mcp-approval-queue-empty"]')).toBeTruthy();
  });

  it("renders a row per approval with the columns specified", () => {
    hookState.items = [makeApproval({ id: "app-1" }), makeApproval({ id: "app-2", tool_name: "other" })];
    render(<MCPApprovalQueue status="pending" />);
    const rows = container.querySelectorAll('tr[data-testid^="mcp-approval-row-"]');
    expect(rows.length).toBe(2);
    expect(container.querySelector('[data-testid="mcp-approval-queue-table"]')).toBeTruthy();
  });

  it("disables the approve/reject buttons for self-approval", () => {
    configState.principalId = "user-2"; // matches default requester
    hookState.items = [makeApproval()];
    render(<MCPApprovalQueue status="pending" />);
    const approve = container.querySelector(
      '[data-testid="mcp-approval-row-approve"]',
    ) as HTMLButtonElement;
    const reject = container.querySelector(
      '[data-testid="mcp-approval-row-reject"]',
    ) as HTMLButtonElement;
    expect(approve.disabled).toBe(true);
    expect(reject.disabled).toBe(true);
    expect(approve.getAttribute("aria-label")).toContain("self-approval");
  });

  it("opens a confirmation modal on approve click and submits via the hook", () => {
    hookState.items = [makeApproval()];
    render(<MCPApprovalQueue status="pending" />);
    const approve = container.querySelector(
      '[data-testid="mcp-approval-row-approve"]',
    ) as HTMLButtonElement;
    act(() => {
      approve.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(container.querySelector('[data-testid="mcp-approval-confirm-modal"]')).toBeTruthy();
    const submit = container.querySelector(
      '[data-testid="mcp-approval-confirm-submit"]',
    ) as HTMLButtonElement;
    act(() => {
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(hookState.approveMutate).toHaveBeenCalledWith(
      { id: "app-1", reason: undefined },
      expect.any(Object),
    );
  });

  it("submits the rejection reason text via the reject mutation", () => {
    hookState.items = [makeApproval()];
    render(<MCPApprovalQueue status="pending" />);
    const reject = container.querySelector(
      '[data-testid="mcp-approval-row-reject"]',
    ) as HTMLButtonElement;
    act(() => {
      reject.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const reason = container.querySelector(
      '[data-testid="mcp-approval-confirm-reason"]',
    ) as HTMLTextAreaElement;
    // React tracks the previous value via an internal setter; bypass
    // it via the native HTMLTextAreaElement value setter so the
    // change event React receives carries the new value.
    const nativeSetter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      "value",
    )?.set;
    act(() => {
      nativeSetter?.call(reason, "destructive op");
      reason.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = container.querySelector(
      '[data-testid="mcp-approval-confirm-submit"]',
    ) as HTMLButtonElement;
    act(() => {
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(hookState.rejectMutate).toHaveBeenCalledWith(
      { id: "app-1", reason: "destructive op" },
      expect.any(Object),
    );
  });

  it("shows the loading state while the query is fetching", () => {
    hookState.isLoading = true;
    render(<MCPApprovalQueue status="pending" />);
    expect(container.querySelector('[data-testid="mcp-approval-queue-loading"]')).toBeTruthy();
  });

  it("renders an error state with a retry that calls refetch", () => {
    hookState.isError = true;
    hookState.error = new Error("boom");
    render(<MCPApprovalQueue status="pending" />);
    const err = container.querySelector('[data-testid="mcp-approval-queue-error"]');
    expect(err).toBeTruthy();
    const retry = err?.querySelector("button") as HTMLButtonElement;
    act(() => {
      retry.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(hookState.refetchCalls).toBe(1);
  });

  it("renders MCP-unavailable as a disabled state instead of retryable error", () => {
    hookState.isError = true;
    hookState.error = new ApiError(503, "unavailable", {
      code: "mcp_approvals_unavailable",
      status: 503,
    });
    render(<MCPApprovalQueue status="pending" />);
    expect(container.querySelector('[data-testid="mcp-approval-queue-unavailable"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-approval-queue-error"]')).toBeFalsy();
  });

  it("renders status-only cells (no actions) for resolved approvals", () => {
    hookState.items = [
      makeApproval({ id: "app-3", status: "approved", resolved_by: "user-7" }),
    ];
    render(<MCPApprovalQueue status="approved" />);
    expect(container.querySelector('[data-testid="mcp-approval-row-approve"]')).toBeFalsy();
    expect(container.textContent).toContain("by user-7");
  });
});
