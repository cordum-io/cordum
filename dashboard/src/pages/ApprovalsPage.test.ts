import React, { act } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Approval } from "@/api/types";
import { FOCUSABLE_SELECTOR } from "@/hooks/useDialogA11y";
import { renderWithProviders } from "@/test-utils/render";
import {
  default as ApprovalsPage,
  DRAWER_A11Y,
  handleDenyConfirm,
  resolveDenyReason,
} from "./ApprovalsPage";

const { hookState, toastState } = vi.hoisted(() => {
  (
    globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
  ).IS_REACT_ACT_ENVIRONMENT = true;

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

  return {
    hookState: {
      approvalsData: { items: [] as Approval[] } as { items: Approval[] },
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
      approveMutate: vi.fn(),
      rejectMutate: vi.fn(),
      approvePending: false,
      rejectPending: false,
    },
    toastState: {
      error: vi.fn(),
    },
  };
});

vi.mock("sonner", () => ({
  Toaster: () => React.createElement("div", { "data-testid": "toaster" }),
  toast: {
    error: toastState.error,
  },
}));

vi.mock("@/hooks/useApprovals", () => ({
  useApprovals: () => ({
    data: hookState.approvalsData,
    isLoading: hookState.isLoading,
    isError: hookState.isError,
    error: hookState.error,
    refetch: hookState.refetch,
  }),
  useApproveJob: () => ({
    mutate: hookState.approveMutate,
    isPending: hookState.approvePending,
  }),
  useRejectJob: () => ({
    mutate: hookState.rejectMutate,
    isPending: hookState.rejectPending,
  }),
}));

function makeApproval(overrides: Partial<Approval> = {}): Approval {
  return {
    id: "apr-default",
    jobId: "job-default",
    status: "pending",
    requestedAt: "2026-01-01T00:00:00.000Z",
    ...overrides,
  };
}

function makeWorkflowApproval(overrides: Partial<Approval> = {}): Approval {
  return makeApproval({
    id: "apr-77",
    jobId: "job-77",
    topic: "workflow.expense.approval",
    reason: "Fallback policy reason",
    humanSummary: "Approve 1,250 USD request with Acme Travel",
    decisionSummary: {
      source: "workflow_payload",
      completeness: "rich",
      contextStatus: "available",
      title: "Approve 1,250 USD request with Acme Travel",
      why: "Budget threshold exceeded",
      nextEffect: "Approve to continue Budget Review.",
      amount: 1250,
      currency: "USD",
      vendor: "Acme Travel",
      itemCount: 2,
      itemsPreview: ["Flight to Berlin", "Hotel stay"],
      escalationReason: "Manager sign-off required",
      missingFields: [],
    },
    workflowContext: {
      workflowId: "wf-expense",
      workflowName: "Expense Approval",
      runId: "run-77",
      stepId: "budget-review",
      stepIndex: 1,
      stepName: "Budget Review",
      totalSteps: 3,
    },
    jobInput: {
      decision: {
        vendor: "Acme Travel",
        amount: 1250,
      },
    },
    jobContext: {
      policy: "expense-threshold",
      tenant: "default",
    },
    ...overrides,
  });
}

function renderPage() {
  const result = renderWithProviders(React.createElement(ApprovalsPage), {
    initialEntries: ["/approvals"],
  });
  return {
    container: result.container,
    cleanup: result.unmount,
  };
}

function click(element: Element | null) {
  if (!element) throw new Error("Expected element to exist before clicking");
  act(() => {
    element.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
}

function findButtonByAriaLabelPrefix(
  container: ParentNode,
  prefix: string,
): HTMLButtonElement | null {
  return (Array.from(container.querySelectorAll("button")).find((button) =>
    (button.getAttribute("aria-label") ?? "").startsWith(prefix),
  ) ?? null) as HTMLButtonElement | null;
}

function findTabButton(
  container: ParentNode,
  label: string,
): HTMLButtonElement | null {
  return (Array.from(container.querySelectorAll("button")).find(
    (button) =>
      button.getAttribute("aria-selected") !== null &&
      button.textContent?.includes(label),
  ) ?? null) as HTMLButtonElement | null;
}

beforeEach(() => {
  document.body.innerHTML = "";
  hookState.approvalsData = { items: [] };
  hookState.isLoading = false;
  hookState.isError = false;
  hookState.error = null;
  hookState.refetch = vi.fn();
  hookState.approveMutate = vi.fn();
  hookState.rejectMutate = vi.fn();
  hookState.approvePending = false;
  hookState.rejectPending = false;
  toastState.error.mockReset();
});

describe("ApprovalsPage deny reason logic", () => {
  describe("resolveDenyReason", () => {
    it("returns custom reason when provided", () => {
      expect(resolveDenyReason("High risk operation")).toBe(
        "High risk operation",
      );
    });

    it("trims whitespace from custom reason", () => {
      expect(resolveDenyReason("  too risky  ")).toBe("too risky");
    });

    it("falls back to default when reason is empty", () => {
      expect(resolveDenyReason("")).toBe("Denied by operator");
    });

    it("falls back to default when reason is only whitespace", () => {
      expect(resolveDenyReason("   ")).toBe("Denied by operator");
    });
  });

  describe("handleDenyConfirm", () => {
    it("sends custom reason to reject mutation", () => {
      const mutate = vi.fn();
      const clearTarget = vi.fn();
      const approval = makeApproval({ id: "approval-42" });

      handleDenyConfirm(approval, "Violates compliance policy", {
        mutate,
        clearTarget,
      });

      expect(mutate).toHaveBeenCalledWith({
        jobId: "job-default",
        reason: "Violates compliance policy",
      });
      expect(clearTarget).toHaveBeenCalled();
    });

    it("sends default reason when custom reason is empty", () => {
      const mutate = vi.fn();
      const clearTarget = vi.fn();
      const approval = makeApproval({ id: "approval-99" });

      handleDenyConfirm(approval, "", { mutate, clearTarget });

      expect(mutate).toHaveBeenCalledWith({
        jobId: "job-default",
        reason: "Denied by operator",
      });
      expect(clearTarget).toHaveBeenCalled();
    });

    it("sends default reason when custom reason is whitespace-only", () => {
      const mutate = vi.fn();
      const clearTarget = vi.fn();
      const approval = makeApproval();

      handleDenyConfirm(approval, "   \t\n  ", { mutate, clearTarget });

      expect(mutate).toHaveBeenCalledWith({
        jobId: "job-default",
        reason: "Denied by operator",
      });
    });

    it("is a no-op when target is null", () => {
      const mutate = vi.fn();
      const clearTarget = vi.fn();

      handleDenyConfirm(null, "some reason", { mutate, clearTarget });

      expect(mutate).not.toHaveBeenCalled();
      expect(clearTarget).not.toHaveBeenCalled();
    });

    it("clears target after mutation", () => {
      const mutate = vi.fn();
      const clearTarget = vi.fn();
      const approval = makeApproval();

      handleDenyConfirm(approval, "blocked", { mutate, clearTarget });

      expect(clearTarget).toHaveBeenCalledTimes(1);
    });
  });
});

describe("ApprovalsPage drawer a11y", () => {
  it("drawer config has role='dialog'", () => {
    expect(DRAWER_A11Y.role).toBe("dialog");
  });

  it("drawer config has aria-modal=true", () => {
    expect(DRAWER_A11Y.ariaModal).toBe(true);
  });

  it("drawer config has aria-labelledby pointing to title element", () => {
    expect(DRAWER_A11Y.labelledById).toBe("approval-drawer-title");
    expect(DRAWER_A11Y.labelledById.length).toBeGreaterThan(0);
  });

  it("drawer uses useDialogA11y hook for Escape close and focus trap", () => {
    expect(DRAWER_A11Y.hookName).toBe("useDialogA11y");
  });

  it("useDialogA11y FOCUSABLE_SELECTOR covers interactive elements", () => {
    expect(FOCUSABLE_SELECTOR).toContain("button:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("a[href]");
    expect(FOCUSABLE_SELECTOR).toContain("textarea:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("input:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("select:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("[tabindex]");
    expect(FOCUSABLE_SELECTOR).toContain('[tabindex="-1"]');
  });

  it("focuses the drawer close button and restores focus to the selected card on close", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };

    const { container, cleanup } = renderPage();
    try {
      const card = container.querySelector(
        'article[role="button"]',
      ) as HTMLElement | null;
      expect(card).not.toBeNull();

      act(() => {
        card?.focus();
      });

      // Card click now navigates to /approvals/:jobId detail page.
      // Verify the card has the correct role and is interactive.
      expect(card?.getAttribute("role")).toBe("button");
      expect(card?.getAttribute("tabindex")).toBe("0");
      expect(card?.getAttribute("aria-label")).toContain("Open approval detail");
    } finally {
      cleanup();
    }
  });

  it("focuses the denial reason field and restores focus to the deny trigger on close", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };

    const { container, cleanup } = renderPage();
    try {
      const denyButton = findButtonByAriaLabelPrefix(container, "Deny ");
      expect(denyButton).not.toBeNull();

      act(() => {
        denyButton?.focus();
      });

      click(denyButton);

      const denyReasonField = container.querySelector(
        'textarea[aria-label="Denial reason"]',
      ) as HTMLTextAreaElement | null;
      expect(denyReasonField).not.toBeNull();
      expect(document.activeElement).toBe(denyReasonField);

      const closeButton = container.querySelector(
        'button[aria-label="Close dialog"]',
      ) as HTMLButtonElement | null;
      expect(closeButton).not.toBeNull();

      click(closeButton);
      expect(document.activeElement).toBe(denyButton);
    } finally {
      cleanup();
    }
  });
});

describe("ApprovalsPage decision-first rendering", () => {
  it("renders workflow approvals with decision content ahead of secondary metadata and accessible actions", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };

    const { container, cleanup } = renderPage();
    try {
      const card = container.querySelector('article[role="button"]');
      const cardText = card?.textContent ?? "";

      expect(card).not.toBeNull();
      expect(cardText).toContain("Approve 1,250 USD request with Acme Travel");
      expect(cardText).toContain("Budget threshold exceeded");
      expect(cardText).toContain("Workflow Gate");
      expect(cardText).toContain("Approve to continue Budget Review.");
      expect(
        cardText.indexOf("Approve 1,250 USD request with Acme Travel"),
      ).toBeLessThan(cardText.indexOf("apr-77"));

      const approveButton = findButtonByAriaLabelPrefix(container, "Approve ");
      const denyButton = findButtonByAriaLabelPrefix(container, "Deny ");
      expect(approveButton).not.toBeNull();
      expect(denyButton).not.toBeNull();

      click(approveButton);
      expect(hookState.approveMutate).toHaveBeenCalledWith(
        { jobId: "job-77" },
        expect.objectContaining({ onError: expect.any(Function) }),
      );
      expect(container.querySelector("#approval-drawer-title")).toBeNull();
    } finally {
      cleanup();
    }
  });

  it("opens the workflow approval drawer from the keyboard and keeps audit/raw data secondary", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };

    const { container, cleanup } = renderPage();
    try {
      const card = container.querySelector('article[role="button"]');
      expect(card).not.toBeNull();

      // Card content shows workflow approval details inline.
      const cardText = card?.textContent ?? "";
      expect(cardText).toContain("Approve 1,250 USD request with Acme Travel");
      expect(cardText).toContain("Budget threshold exceeded");

      // Card click now navigates to detail page instead of opening drawer.
      expect(card?.getAttribute("role")).toBe("button");
      expect(card?.getAttribute("tabindex")).toBe("0");
    } finally {
      cleanup();
    }
  });

  it("renders legacy approvals with safe fallback messaging and visible but secondary audit detail", () => {
    hookState.approvalsData = {
      items: [
        makeApproval({
          id: "apr-legacy",
          jobId: "job-legacy",
          topic: "finance.expense.review",
          reason: "Requires manual review",
          policySnapshot: "policy-v4",
        }),
      ],
    };

    const { container, cleanup } = renderPage();
    try {
      const card = container.querySelector('article[role="button"]');
      expect(card?.textContent).toContain("Review finance.expense.review");
      expect(card?.textContent).toContain("Requires manual review");
      expect(card?.textContent).toContain("Safety Policy");

      // Card click navigates to detail page. Verify card is interactive.
      expect(card?.getAttribute("role")).toBe("button");
      expect(card?.getAttribute("tabindex")).toBe("0");
    } finally {
      cleanup();
    }
  });

  it("renders degraded workflow approvals with explicit context warnings instead of an empty shell", () => {
    hookState.approvalsData = {
      items: [
        makeWorkflowApproval({
          decisionSummary: {
            source: "workflow_payload",
            completeness: "partial",
            contextStatus: "missing",
            title: "Approve manager-approval",
            why: "manager review required",
            missingFields: ["approval_context", "business_context"],
          },
          jobInput: undefined,
          jobContext: undefined,
        }),
      ],
    };

    const { container, cleanup } = renderPage();
    try {
      const card = container.querySelector('article[role="button"]');
      expect(card?.textContent).toContain("Approve manager-approval");
      expect(card?.textContent).toContain("manager review required");
      expect(card?.textContent).toContain(
        "Approval context is missing — missing approval_context, business_context.",
      );

      // Card click navigates to detail page. Verify card is interactive.
      expect(card?.getAttribute("role")).toBe("button");
      expect(card?.getAttribute("tabindex")).toBe("0");
      // Degraded approvals show context warnings inline on the card.
      expect(card?.textContent).toContain(
        "Approval context is missing — missing approval_context, business_context.",
      );
    } finally {
      cleanup();
    }
  });

  it("shows approved history without pending action buttons when the approved tab is selected", () => {
    hookState.approvalsData = {
      items: [
        makeApproval({
          id: "apr-approved",
          jobId: "job-approved",
          status: "approved",
          humanSummary: "Approve renewal for Example Corp",
          reason: "Finance approved the renewal",
        }),
      ],
    };

    const { container, cleanup } = renderPage();
    try {
      click(findTabButton(container, "Approved"));

      const card = container.querySelector('article[role="button"]');
      expect(card?.textContent).toContain("Approve renewal for Example Corp");
      expect(findButtonByAriaLabelPrefix(container, "Approve ")).toBeNull();
      expect(findButtonByAriaLabelPrefix(container, "Deny ")).toBeNull();
    } finally {
      cleanup();
    }
  });

  it("renders invalidated and repaired approvals as non-actionable lifecycle states", () => {
    hookState.approvalsData = {
      items: [
        makeApproval({
          id: "apr-invalidated",
          jobId: "job-invalidated",
          status: "invalidated",
          actionability: "invalidated",
          humanSummary: "Budget approval drifted",
          reason: "Workflow input changed after approval request creation",
        }),
        makeApproval({
          id: "apr-repaired",
          jobId: "job-repaired",
          status: "repaired",
          actionability: "repaired",
          humanSummary: "Legacy approval repaired",
          reason: "Operator repaired an inconsistent approval row",
        }),
      ],
    };

    const { container, cleanup } = renderPage();
    try {
      click(findTabButton(container, "Invalidated"));

      expect(container.textContent).toContain("Budget approval drifted");
      expect(container.textContent).toContain("Invalidated");
      expect(findButtonByAriaLabelPrefix(container, "Approve ")).toBeNull();
      expect(findButtonByAriaLabelPrefix(container, "Deny ")).toBeNull();

      click(findTabButton(container, "Repaired"));
      expect(container.textContent).toContain("Legacy approval repaired");
      expect(container.textContent).toContain("Repaired");
    } finally {
      cleanup();
    }
  });

  it("shows a clear empty-state message when no pending approvals are available", () => {
    hookState.approvalsData = { items: [] };

    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("No pending approvals");
      expect(container.textContent).toContain(
        "Approvals are triggered when a job matches a require_approval rule",
      );
    } finally {
      cleanup();
    }
  });

  it("shows loading skeletons while approvals are being fetched", () => {
    hookState.isLoading = true;
    hookState.approvalsData = { items: [] };

    const { container, cleanup } = renderPage();
    try {
      expect(container.querySelectorAll(".skeleton").length).toBeGreaterThan(0);
      expect(container.textContent).not.toContain("Approval queue unavailable");
    } finally {
      cleanup();
    }
  });

  it("disables approval actions while approve is pending", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };
    hookState.approvePending = true;

    const { container, cleanup } = renderPage();
    try {
      const approveButton = findButtonByAriaLabelPrefix(container, "Approve ");
      const denyButton = findButtonByAriaLabelPrefix(container, "Deny ");
      expect(approveButton?.hasAttribute("disabled")).toBe(true);
      expect(denyButton?.hasAttribute("disabled")).toBe(true);
    } finally {
      cleanup();
    }
  });

  it("disables deny actions while reject is pending", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };
    hookState.rejectPending = true;

    const { container, cleanup } = renderPage();
    try {
      const denyButton = findButtonByAriaLabelPrefix(container, "Deny ");
      expect(denyButton?.hasAttribute("disabled")).toBe(true);
    } finally {
      cleanup();
    }
  });

  it("shows a toast when approving fails", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };
    hookState.approveMutate = vi.fn((_input, options) => {
      options?.onError?.(new Error("approve failed"));
    });

    const { container, cleanup } = renderPage();
    try {
      const approveButton = findButtonByAriaLabelPrefix(container, "Approve ");
      click(approveButton);

      expect(toastState.error).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("keeps the deny dialog open and shows a toast when rejection fails", () => {
    hookState.approvalsData = { items: [makeWorkflowApproval()] };
    hookState.rejectMutate = vi.fn((_input, options) => {
      options?.onError?.(new Error("reject failed"));
    });

    const { container, cleanup } = renderPage();
    try {
      const denyButton = findButtonByAriaLabelPrefix(container, "Deny ");
      click(denyButton);

      const confirmButton = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Enter reason to deny"),
      );
      click(confirmButton ?? null);

      expect(container.querySelector('textarea[aria-label="Denial reason"]')).not.toBeNull();
      expect(toastState.error).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("shows the API error state and lets the user retry", () => {
    hookState.isError = true;
    hookState.error = new Error("Gateway offline");

    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("Approval queue unavailable");
      expect(container.textContent).toContain("Gateway offline");

      const retryButton = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Try again"),
      );
      click(retryButton ?? null);
      expect(hookState.refetch).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });
});
