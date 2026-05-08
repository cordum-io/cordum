import { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
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

  it("encodes the policy-gate as a data attribute when supplied", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay policyGate="require_approval" runtime />,
      );
    });

    const slot = container.querySelector('[data-slot="policy-gate"]');
    expect(slot?.getAttribute("data-policy-gate")).toBe("require_approval");
  });

  it("renders the audit hash chip with the truncated 8-char prefix", () => {
    act(() => {
      root.render(
        <WorkflowNodeGovernanceOverlay auditHash="abcdef0123456789deadbeef" runtime />,
      );
    });

    const chip = container.querySelector<HTMLButtonElement>('button[data-slot="audit-hash"]');
    expect(chip).not.toBeNull();
    expect(chip?.textContent).toBe("abcdef01");
    expect(chip?.getAttribute("aria-label")).toBe(
      "Copy audit hash abcdef0123456789deadbeef",
    );
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
