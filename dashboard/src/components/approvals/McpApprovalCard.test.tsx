import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";

// Mocked hook state shared by the module mock below. Lifted via vi.hoisted
// so the mock factories don't close over an undefined binding at module
// evaluation time — the same pattern as the existing ApprovalsPage tests.
const { hookState, toastState } = vi.hoisted(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    hookState: {
      approveMutate: vi.fn(),
      rejectMutate: vi.fn(),
      approvePending: false,
      rejectPending: false,
      approvalDetail: undefined as unknown,
      approvalDetailLoading: false,
      approvalDetailError: null as Error | null,
    },
    toastState: { addToast: vi.fn() },
  };
});

vi.mock("../../hooks/useMcpApprovals", async () => {
  const actual = await vi.importActual<typeof import("../../hooks/useMcpApprovals")>(
    "../../hooks/useMcpApprovals",
  );
  return {
    ...actual,
    useApproveMcp: () => ({
      mutate: hookState.approveMutate,
      isPending: hookState.approvePending,
    }),
    useRejectMcp: () => ({
      mutate: hookState.rejectMutate,
      isPending: hookState.rejectPending,
    }),
    useMcpApproval: () => ({
      data: hookState.approvalDetail,
      isLoading: hookState.approvalDetailLoading,
      error: hookState.approvalDetailError,
    }),
  };
});

vi.mock("../../state/toast", () => ({
  useToastStore: () => toastState,
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));

import { McpApprovalCard } from "./McpApprovalCard";
import type { McpApproval } from "../../hooks/useMcpApprovals";

const sampleApproval: McpApproval = {
  id: "abcd1234abcd1234abcd1234abcd1234",
  tenant: "default",
  agent_id: "agent-alpha",
  tool_name: "files.delete",
  args_hash: "deadbeef0123456789abcdef",
  requester: "alice@corp",
  reason: "tool 'files.delete' matches approval scope 'destructive'",
  status: "pending",
  created_at: 1_700_000_000_000_000,
  expires_at: 1_700_000_300_000_000,
};

function renderCard(approval: McpApproval) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(React.createElement(McpApprovalCard, { approval }));
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function click(el: Element | null) {
  if (!el) throw new Error("expected element before click");
  act(() => {
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

beforeEach(() => {
  document.body.innerHTML = "";
  hookState.approveMutate.mockReset();
  hookState.rejectMutate.mockReset();
  hookState.approvePending = false;
  hookState.rejectPending = false;
  hookState.approvalDetail = undefined;
  hookState.approvalDetailLoading = false;
  hookState.approvalDetailError = null;
  toastState.addToast.mockReset();
});

describe("McpApprovalCard", () => {
  it("renders the tool name, requester, tenant and trigger reason", () => {
    const { container, cleanup } = renderCard(sampleApproval);
    try {
      const text = container.textContent ?? "";
      expect(text).toContain("files.delete");
      expect(text).toContain("alice@corp");
      expect(text).toContain("default");
      expect(text).toContain("matches approval scope 'destructive'");
      // Args hash is rendered in short form so operators can visually
      // disambiguate calls without eating screen width.
      expect(text).toContain("deadbeef");
    } finally {
      cleanup();
    }
  });

  it("approve button dispatches the approve mutation with the record id", () => {
    const { container, cleanup } = renderCard(sampleApproval);
    try {
      const approveBtn = container.querySelector(
        `[data-testid="mcp-approval-${sampleApproval.id}-approve"]`,
      );
      click(approveBtn);
      expect(hookState.approveMutate).toHaveBeenCalledTimes(1);
      expect(hookState.approveMutate).toHaveBeenCalledWith({ id: sampleApproval.id });
    } finally {
      cleanup();
    }
  });

  it("reject button dispatches the reject mutation with the record id", () => {
    const { container, cleanup } = renderCard(sampleApproval);
    try {
      const rejectBtn = container.querySelector(
        `[data-testid="mcp-approval-${sampleApproval.id}-reject"]`,
      );
      click(rejectBtn);
      expect(hookState.rejectMutate).toHaveBeenCalledTimes(1);
      expect(hookState.rejectMutate).toHaveBeenCalledWith({ id: sampleApproval.id });
    } finally {
      cleanup();
    }
  });

  it("disables approve/reject on non-pending records", () => {
    const { container, cleanup } = renderCard({ ...sampleApproval, status: "approved" });
    try {
      const approveBtn = container.querySelector<HTMLButtonElement>(
        `[data-testid="mcp-approval-${sampleApproval.id}-approve"]`,
      );
      const rejectBtn = container.querySelector<HTMLButtonElement>(
        `[data-testid="mcp-approval-${sampleApproval.id}-reject"]`,
      );
      expect(approveBtn?.disabled).toBe(true);
      expect(rejectBtn?.disabled).toBe(true);
    } finally {
      cleanup();
    }
  });

  it("Review args opens the modal which renders the persisted args payload", () => {
    hookState.approvalDetail = {
      ...sampleApproval,
      args: { path: "/etc/shadow", recursive: true },
    };
    const { container, cleanup } = renderCard(sampleApproval);
    try {
      const reviewBtn = container.querySelector(
        `[data-testid="mcp-approval-${sampleApproval.id}-review"]`,
      );
      click(reviewBtn);
      // Dialog's aria-modal wrapper is appended inside the card; query
      // document so we don't miss it if the component uses a portal.
      const dialog = document.querySelector('[role="dialog"][aria-modal="true"]');
      expect(dialog).not.toBeNull();
      const payload = document.querySelector('[data-testid="mcp-args-payload"]');
      expect(payload?.textContent).toContain("/etc/shadow");
      expect(payload?.textContent).toContain("recursive");
      expect(payload?.textContent).toContain("true");
    } finally {
      cleanup();
    }
  });

  it("modal shows an explicit placeholder when args were not persisted", () => {
    hookState.approvalDetail = { ...sampleApproval, args: undefined };
    const { container, cleanup } = renderCard(sampleApproval);
    try {
      const reviewBtn = container.querySelector(
        `[data-testid="mcp-approval-${sampleApproval.id}-review"]`,
      );
      click(reviewBtn);
      const payload = document.querySelector('[data-testid="mcp-args-payload"]');
      expect(payload?.textContent).toContain("args not captured");
    } finally {
      cleanup();
    }
  });
});
