import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { McpApproval } from "../../hooks/useMcpApprovals";
import { McpApprovalsSection } from "./McpApprovalsSection";

// Enable React's concurrent test mode warnings.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// Hoist hook mocks so vi.mock below can reference them.
const { hookState, approveMutation, rejectMutation } = vi.hoisted(() => {
  type State = {
    data?: McpApproval[];
    isLoading: boolean;
    error: Error | null;
  };
  const state: State = { data: undefined, isLoading: false, error: null };
  const approveMutation = { mutate: vi.fn(), isPending: false };
  const rejectMutation = { mutate: vi.fn(), isPending: false };
  return { hookState: state, approveMutation, rejectMutation };
});

vi.mock("../../hooks/useMcpApprovals", async () => {
  const actual = await vi.importActual<typeof import("../../hooks/useMcpApprovals")>(
    "../../hooks/useMcpApprovals",
  );
  return {
    ...actual,
    useMcpApprovals: () => ({
      data: hookState.data,
      isLoading: hookState.isLoading,
      error: hookState.error,
    }),
    useMcpApproval: () => ({
      data: hookState.data?.[0],
      isLoading: false,
      error: null,
    }),
    useApproveMcp: () => approveMutation,
    useRejectMcp: () => rejectMutation,
  };
});

function render(ui: React.ReactElement) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRoot(container);
  act(() => {
    root.render(
      <QueryClientProvider client={client}>
        {ui}
      </QueryClientProvider>,
    );
  });
  return { container, root };
}

function cleanup({ container, root }: { container: HTMLElement; root: ReturnType<typeof createRoot> }) {
  act(() => root.unmount());
  container.remove();
}

const samplePending: McpApproval = {
  id: "app-42",
  tenant: "default",
  agent_id: "agent-alpha",
  tool_name: "files.delete",
  args_hash: "deadbeefcafebabe",
  requester: "alice@corp",
  reason: "dangerous tool requires approval",
  status: "pending",
  created_at: 1,
  expires_at: 2,
};

describe("McpApprovalsSection", () => {
  beforeEach(() => {
    hookState.data = undefined;
    hookState.isLoading = false;
    hookState.error = null;
    approveMutation.mutate.mockClear();
    rejectMutation.mutate.mockClear();
    approveMutation.isPending = false;
    rejectMutation.isPending = false;
  });

  it("renders the loading state while the query is in flight", () => {
    hookState.isLoading = true;
    const { container, root } = render(<McpApprovalsSection />);
    expect(container.querySelector('[data-testid="mcp-approvals-loading"]')).not.toBeNull();
    cleanup({ container, root });
  });

  it("renders the error state when the query fails", () => {
    hookState.error = new Error("boom");
    const { container, root } = render(<McpApprovalsSection />);
    expect(container.querySelector('[data-testid="mcp-approvals-error"]')).not.toBeNull();
    cleanup({ container, root });
  });

  it("renders an explicit empty hint when no approvals exist", () => {
    hookState.data = [];
    const { container, root } = render(<McpApprovalsSection />);
    expect(container.querySelector('[data-testid="mcp-approvals-empty"]')).not.toBeNull();
    cleanup({ container, root });
  });

  it("renders a card per approval and fires approve mutation on click", () => {
    hookState.data = [samplePending];
    const { container, root } = render(<McpApprovalsSection />);
    // Find the Approve button in the rendered card.
    const approveBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => (b.textContent ?? "").toLowerCase().includes("approve"),
    );
    expect(approveBtn).toBeDefined();
    act(() => {
      approveBtn!.click();
    });
    expect(approveMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ id: samplePending.id }),
    );
    cleanup({ container, root });
  });

  it("renders a card per approval and fires reject mutation on click", () => {
    hookState.data = [samplePending];
    const { container, root } = render(<McpApprovalsSection />);
    const rejectBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => (b.textContent ?? "").toLowerCase().includes("reject"),
    );
    expect(rejectBtn).toBeDefined();
    act(() => {
      rejectBtn!.click();
    });
    expect(rejectMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ id: samplePending.id }),
    );
    cleanup({ container, root });
  });
});
