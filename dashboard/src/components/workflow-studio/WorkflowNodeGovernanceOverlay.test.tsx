import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { WorkflowNodeGovernanceOverlay } from "./WorkflowNodeGovernanceOverlay";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe("WorkflowNodeGovernanceOverlay", () => {
  it("renders all three indicator slots even with no data (DoD #2 contract)", () => {
    act(() => {
      root.render(<WorkflowNodeGovernanceOverlay />);
    });

    const policySlot = container.querySelector('[data-slot="policy-gate"]');
    const safetySlot = container.querySelector('[data-slot="safety-decision"]');
    const auditSlot = container.querySelector('[data-slot="audit-hash"]');

    expect(policySlot).not.toBeNull();
    expect(safetySlot).not.toBeNull();
    expect(auditSlot).not.toBeNull();
  });

  it("marks policy-gate + audit-hash as data-pending-api='task-913b6c6c' when source data is missing", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay safetyDecision="allow" runtime />,
      );
    });

    expect(
      container.querySelector('[data-slot="policy-gate"][data-pending-api="task-913b6c6c"]'),
    ).not.toBeNull();
    expect(
      container.querySelector('[data-slot="audit-hash"][data-pending-api="task-913b6c6c"]'),
    ).not.toBeNull();
  });

  it("renders the saturated SafetyDecisionBadge when safetyDecision is provided", () => {
    act(() => {
      root.render(<WorkflowNodeGovernanceOverlay safetyDecision="deny" runtime />);
    });

    const badge = container.querySelector('[data-slot="safety-decision"]');
    expect(badge).not.toBeNull();
    expect(badge?.getAttribute("aria-label")).toBe("Safety decision: deny");
  });

  it("encodes the policy-gate=require_approval as a data attribute", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay policyGate="require_approval" runtime />,
      );
    });

    const slot = container.querySelector('[data-slot="policy-gate"]');
    expect(slot?.getAttribute("data-policy-gate")).toBe("require_approval");
    // Visual icon: ShieldQuestion-style (Shield) + warning tone class.
    expect(slot?.querySelector("svg")).not.toBeNull();
    expect(slot?.className).toMatch(/text-\[var\(--color-warning\)\]/);
  });

  it("encodes the policy-gate=allow with success-tone Shield icon (DoD #3 — task-6fccc637 reopen)", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay policyGate="allow" runtime />,
      );
    });

    const slot = container.querySelector('[data-slot="policy-gate"]');
    expect(slot?.getAttribute("data-policy-gate")).toBe("allow");
    expect(slot?.className).toMatch(/text-\[var\(--color-success\)\]/);
    expect(slot?.querySelector("svg")).not.toBeNull();
  });

  it("encodes the policy-gate=deny with governance-tone Shield icon (DoD #3 — task-6fccc637 reopen)", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay policyGate="deny" runtime />,
      );
    });

    const slot = container.querySelector('[data-slot="policy-gate"]');
    expect(slot?.getAttribute("data-policy-gate")).toBe("deny");
    expect(slot?.className).toMatch(/text-\[var\(--color-governance\)\]/);
    expect(slot?.querySelector("svg")).not.toBeNull();
  });

  it("renders the audit hash chip with the truncated 8-char prefix via the shared CodeBlock primitive (DoD #1)", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay auditHash="abcdef0123456789deadbeef" runtime />,
      );
    });

    const slot = container.querySelector('[data-slot="audit-hash"]');
    expect(slot).not.toBeNull();
    const chip = slot?.querySelector<HTMLButtonElement>("button");
    expect(chip).not.toBeNull();
    expect(chip?.textContent).toBe("abcdef01");
    expect(chip?.getAttribute("aria-label")).toBe(
      "Copy audit hash abcdef0123456789deadbeef",
    );
  });

  it("copies the FULL audit hash to clipboard on chip click (DoD #3 — task-6fccc637 reopen)", () => {
    const fullHash = "abcdef0123456789deadbeefcafebabe";
    const writeText = vi.fn().mockResolvedValue(undefined);
    const originalClipboard = navigator.clipboard;
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });

    try {
      act(() => {
        root.render(
          <WorkflowNodeGovernanceOverlay auditHash={fullHash} runtime />,
        );
      });

      const chip = container.querySelector<HTMLButtonElement>(
        '[data-slot="audit-hash"] button',
      );
      expect(chip).not.toBeNull();
      // Copy preview is truncated, the writeText payload is the full hash.
      expect(chip?.textContent).toBe(fullHash.slice(0, 8));

      act(() => {
        chip?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
      });

      expect(writeText).toHaveBeenCalledTimes(1);
      expect(writeText).toHaveBeenCalledWith(fullHash);
    } finally {
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: originalClipboard,
      });
    }
  });

  it("flags design-time (runtime=false) vs run-time via data-runtime attribute", () => {
    act(() => {
      root.render(<WorkflowNodeGovernanceOverlay safetyDecision="allow" />);
    });
    const designOverlay = container.querySelector("[data-governance-overlay]");
    expect(designOverlay?.getAttribute("data-runtime")).toBe("false");

    act(() => {
      root.unmount();
    });
    container.remove();
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);

    act(() => {
      root.render(<WorkflowNodeGovernanceOverlay safetyDecision="allow" runtime />);
    });
    const runtimeOverlay = container.querySelector("[data-governance-overlay]");
    expect(runtimeOverlay?.getAttribute("data-runtime")).toBe("true");
  });
});
