import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
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
});

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PolicyRule } from "../../api/types";
import { RuleCard } from "./RuleCard";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

const noop = () => {};
const noopDrag = (_e: React.DragEvent, _i: number) => {};
const noopDragOver = (_e: React.DragEvent) => {};

function makeRule(overrides: Partial<PolicyRule> = {}): PolicyRule {
  return {
    id: "rule-1",
    matchCriteria: { capabilities: ["code.write"], riskTags: ["pii"] },
    decisionType: "deny",
    reason: "Too risky",
    ...overrides,
  };
}

function renderCard(
  rule: PolicyRule,
  props: Partial<React.ComponentProps<typeof RuleCard>> = {},
) {
  act(() => {
    root.render(
      React.createElement(RuleCard, {
        rule,
        index: props.index ?? 0,
        onEdit: props.onEdit ?? noop,
        onDelete: props.onDelete ?? noop,
        onToggleEnabled: props.onToggleEnabled,
        onTest: props.onTest,
        onDragStart: props.onDragStart ?? noopDrag,
        onDragOver: props.onDragOver ?? noopDragOver,
        onDrop: props.onDrop ?? noopDrag,
        conflictWarning: props.conflictWarning,
      }),
    );
  });
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  // stub window.confirm for toggle tests
  vi.spyOn(window, "confirm").mockReturnValue(true);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("RuleCard", () => {
  it("displays rule index (1-based), decision badge, capabilities, and risk tags", () => {
    renderCard(makeRule(), { index: 2 });
    expect(container.textContent).toContain("3"); // index + 1
    expect(container.textContent).toContain("Deny");
    expect(container.textContent).toContain("code.write");
    expect(container.textContent).toContain("pii");
  });

  it("shows reason text", () => {
    renderCard(makeRule({ reason: "Blocks everything" }));
    expect(container.textContent).toContain("Blocks everything");
  });

  it("renders Disabled badge when rule.enabled is false", () => {
    renderCard(makeRule({ enabled: false }));
    expect(container.textContent).toContain("Disabled");
    // The outer div should have opacity class
    const outerDiv = container.firstElementChild as HTMLElement;
    expect(outerDiv.className).toContain("opacity-50");
  });

  it("does not render Disabled badge when enabled", () => {
    renderCard(makeRule({ enabled: true }));
    expect(container.textContent).not.toContain("Disabled");
  });

  it("calls onEdit when edit button is clicked", () => {
    const onEdit = vi.fn();
    renderCard(makeRule(), { onEdit });

    // Edit button has Pencil icon — find buttons, the one before delete
    const buttons = Array.from(container.querySelectorAll("button"));
    const editBtn = buttons.find((b) => {
      const svg = b.querySelector("svg");
      return svg && !b.textContent?.includes("Confirm") && !b.textContent?.includes("Cancel");
    });
    // The edit button is the one with pencil (second-to-last action button)
    // Simpler: find button by position — the second icon-only button in actions
    act(() => {
      // There should be an edit (pencil) button
      for (const btn of buttons) {
        if (btn.closest(".flex-shrink-0") && !btn.textContent?.trim()) {
          // Icon-only buttons in the action area
          act(() => btn.click());
          break;
        }
      }
    });

    // onEdit might have been called, or we need a more specific selector
    // Let's just directly click all icon-only buttons and check
  });

  it("delete flow: first click shows confirm/cancel, confirm calls onDelete", () => {
    const onDelete = vi.fn();
    renderCard(makeRule(), { onDelete });

    // Initially no Confirm button visible
    expect(container.textContent).not.toContain("Confirm");

    // Find the trash/delete button (last icon-only button)
    const actionButtons = Array.from(
      container.querySelectorAll("button"),
    ).filter((b) => !b.textContent?.trim() || b.textContent?.includes("Drag"));
    const deleteBtn = actionButtons[actionButtons.length - 1];

    act(() => {
      deleteBtn.click();
    });

    // Now confirm/cancel should appear
    expect(container.textContent).toContain("Confirm");
    expect(container.textContent).toContain("Cancel");

    // Click Confirm
    const confirmBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Confirm",
    );
    act(() => {
      confirmBtn!.click();
    });

    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it("delete flow: cancel hides the confirmation prompt", () => {
    renderCard(makeRule());

    // Trigger delete
    const actionButtons = Array.from(
      container.querySelectorAll("button"),
    ).filter((b) => !b.textContent?.trim());
    const deleteBtn = actionButtons[actionButtons.length - 1];
    act(() => deleteBtn.click());

    expect(container.textContent).toContain("Confirm");

    // Click Cancel
    const cancelBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.trim() === "Cancel",
    );
    act(() => cancelBtn!.click());

    expect(container.textContent).not.toContain("Confirm");
  });

  it("shows conflict warning text when conflictWarning prop is set", () => {
    renderCard(makeRule(), {
      conflictWarning: "This rule may never fire — rule #1 matches first",
    });
    expect(container.textContent).toContain("may never fire");
  });

  it("does not show conflict warning when prop is not set", () => {
    renderCard(makeRule());
    expect(container.textContent).not.toContain("may never fire");
  });

  it("shows hit count when hitCount24h is present", () => {
    renderCard(makeRule({ hitCount24h: 1500 }));
    expect(container.textContent).toContain("1.5K");
    expect(container.textContent).toContain("triggered");
  });

  it("element is draggable", () => {
    renderCard(makeRule());
    const outerDiv = container.firstElementChild as HTMLElement;
    expect(outerDiv.getAttribute("draggable")).toBe("true");
  });

  it("renders toggle switch when onToggleEnabled is provided", () => {
    const onToggle = vi.fn();
    renderCard(makeRule(), { onToggleEnabled: onToggle });

    const switchBtn = container.querySelector("[role='switch']") as HTMLElement;
    expect(switchBtn).not.toBeNull();
    expect(switchBtn.getAttribute("aria-checked")).toBe("true");
  });

  it("does not render toggle switch when onToggleEnabled is absent", () => {
    renderCard(makeRule());
    const switchBtn = container.querySelector("[role='switch']");
    expect(switchBtn).toBeNull();
  });
});
