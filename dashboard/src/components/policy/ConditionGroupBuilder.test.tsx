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
import type { ConditionGroup } from "./conditionTypes";
import { createCondition, createConditionGroup } from "./conditionTypes";

// Mock ConditionRow to a simple stub
vi.mock("./ConditionRow", () => ({
  ConditionRow: ({ condition, onRemove }: { condition: { id: string; field: string }; onRemove: () => void }) =>
    React.createElement("div", { "data-testid": `row-${condition.id}` },
      React.createElement("span", null, condition.field),
      React.createElement("button", { type: "button", onClick: onRemove, "data-action": "remove-condition" }, "X"),
    ),
}));

import { ConditionGroupBuilder } from "./ConditionGroupBuilder";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

function renderGroup(
  group: ConditionGroup,
  onChange: (updated: ConditionGroup) => void,
  opts?: { onRemove?: () => void; depth?: number },
) {
  act(() => {
    root.render(
      React.createElement(ConditionGroupBuilder, {
        group,
        onChange,
        onRemove: opts?.onRemove,
        depth: opts?.depth ?? 0,
      }),
    );
  });
}

function findButtonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll("button")).find((b) =>
    b.textContent?.includes(text),
  );
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("ConditionGroupBuilder", () => {
  it("displays logic toggle showing AND", () => {
    const group = createConditionGroup("AND", []);
    renderGroup(group, vi.fn());
    expect(container.textContent).toContain("AND");
    expect(container.textContent).toContain("All conditions must match");
  });

  it("toggles logic from AND to OR on click", () => {
    const onChange = vi.fn();
    const group = createConditionGroup("AND", []);
    renderGroup(group, onChange);

    const logicBtn = findButtonByText("AND");
    act(() => logicBtn!.click());

    expect(onChange).toHaveBeenCalledTimes(1);
    const updated = onChange.mock.calls[0][0] as ConditionGroup;
    expect(updated.logic).toBe("OR");
  });

  it("toggles logic from OR to AND on click", () => {
    const onChange = vi.fn();
    const group = createConditionGroup("OR", []);
    renderGroup(group, onChange);

    const logicBtn = findButtonByText("OR");
    act(() => logicBtn!.click());

    const updated = onChange.mock.calls[0][0] as ConditionGroup;
    expect(updated.logic).toBe("AND");
  });

  it("adds a new condition when Condition button is clicked", () => {
    const onChange = vi.fn();
    const group = createConditionGroup("AND", []);
    renderGroup(group, onChange);

    const addBtn = findButtonByText("Condition");
    act(() => addBtn!.click());

    expect(onChange).toHaveBeenCalledTimes(1);
    const updated = onChange.mock.calls[0][0] as ConditionGroup;
    expect(updated.conditions).toHaveLength(1);
    expect("field" in updated.conditions[0]).toBe(true);
  });

  it("adds a nested group when Group button is clicked", () => {
    const onChange = vi.fn();
    const group = createConditionGroup("AND", []);
    renderGroup(group, onChange, { depth: 0 });

    const groupBtn = findButtonByText("Group");
    expect(groupBtn).toBeDefined();
    act(() => groupBtn!.click());

    expect(onChange).toHaveBeenCalledTimes(1);
    const updated = onChange.mock.calls[0][0] as ConditionGroup;
    expect(updated.conditions).toHaveLength(1);
    expect("logic" in updated.conditions[0]).toBe(true);
    // Nested group has one default condition
    const nested = updated.conditions[0] as ConditionGroup;
    expect(nested.conditions).toHaveLength(1);
  });

  it("hides Group button at depth >= MAX_DEPTH (3)", () => {
    const group = createConditionGroup("AND", []);
    renderGroup(group, vi.fn(), { depth: 3 });

    const groupBtn = findButtonByText("Group");
    expect(groupBtn).toBeUndefined();
    // Condition button should still exist
    expect(findButtonByText("Condition")).toBeDefined();
  });

  it("shows Group button at depth < MAX_DEPTH", () => {
    const group = createConditionGroup("AND", []);
    renderGroup(group, vi.fn(), { depth: 2 });

    expect(findButtonByText("Group")).toBeDefined();
  });

  it("removes a condition when ConditionRow remove button is clicked", () => {
    const cond1 = createCondition("capability", "in", ["a"]);
    const cond2 = createCondition("riskTag", "in", ["b"]);
    const group = createConditionGroup("AND", [cond1, cond2]);
    const onChange = vi.fn();
    renderGroup(group, onChange);

    // Find the remove button for the first condition
    const removeBtn = container.querySelector(
      `[data-testid="row-${cond1.id}"] [data-action="remove-condition"]`,
    ) as HTMLButtonElement;
    expect(removeBtn).not.toBeNull();

    act(() => removeBtn.click());

    expect(onChange).toHaveBeenCalledTimes(1);
    const updated = onChange.mock.calls[0][0] as ConditionGroup;
    expect(updated.conditions).toHaveLength(1);
    expect((updated.conditions[0] as { id: string }).id).toBe(cond2.id);
  });

  it("shows remove button only when onRemove prop is provided", () => {
    const group = createConditionGroup("AND", []);

    // Without onRemove
    renderGroup(group, vi.fn());
    const trashButtons = Array.from(container.querySelectorAll("button")).filter(
      (b) => b.className.includes("danger"),
    );
    expect(trashButtons).toHaveLength(0);

    // With onRemove
    const onRemove = vi.fn();
    renderGroup(group, vi.fn(), { onRemove });
    const trashBtns = Array.from(container.querySelectorAll("button")).filter(
      (b) => b.className.includes("danger"),
    );
    expect(trashBtns.length).toBeGreaterThan(0);
  });

  it("renders existing conditions via ConditionRow", () => {
    const cond = createCondition("capability", "in", ["code.write"]);
    const group = createConditionGroup("AND", [cond]);
    renderGroup(group, vi.fn());

    const row = container.querySelector(`[data-testid="row-${cond.id}"]`);
    expect(row).not.toBeNull();
    expect(row!.textContent).toContain("capability");
  });
});
