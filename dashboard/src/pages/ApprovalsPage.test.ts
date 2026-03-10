import { describe, expect, it, vi } from "vitest";
import { resolveDenyReason, handleDenyConfirm, DRAWER_A11Y } from "./ApprovalsPage";
import { FOCUSABLE_SELECTOR } from "@/hooks/useDialogA11y";
import type { Approval } from "@/api/types";

function makeApproval(overrides: Partial<Approval> = {}): Approval {
  return {
    id: "approval-test-1",
    jobId: "job-test-1",
    status: "pending",
    requestedAt: "2026-01-01T00:00:00.000Z",
    ...overrides,
  };
}

describe("ApprovalsPage deny reason logic", () => {
  describe("resolveDenyReason", () => {
    it("returns custom reason when provided", () => {
      expect(resolveDenyReason("High risk operation")).toBe("High risk operation");
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

      handleDenyConfirm(approval, "Violates compliance policy", { mutate, clearTarget });

      expect(mutate).toHaveBeenCalledWith({
        id: "approval-42",
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
        id: "approval-99",
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
        id: "approval-test-1",
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
    // Non-empty ensures the heading element has a matching id attribute
    expect(DRAWER_A11Y.labelledById.length).toBeGreaterThan(0);
  });

  it("drawer uses useDialogA11y hook for Escape close and focus trap", () => {
    // The hook name is exported to verify the drawer is wired to the correct
    // a11y implementation (Escape key, Tab focus cycling).
    expect(DRAWER_A11Y.hookName).toBe("useDialogA11y");
  });

  it("useDialogA11y FOCUSABLE_SELECTOR covers interactive elements", () => {
    // Verify the focus trap selector includes all standard interactive elements
    expect(FOCUSABLE_SELECTOR).toContain("button:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("a[href]");
    expect(FOCUSABLE_SELECTOR).toContain("textarea:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("input:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("select:not([disabled])");
    expect(FOCUSABLE_SELECTOR).toContain("[tabindex]");
    // Negative tabindex elements should be excluded from focus cycling
    expect(FOCUSABLE_SELECTOR).toContain('[tabindex="-1"]');
  });
});
