import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ComboboxInput, type ComboboxSuggestion } from "./ComboboxInput";

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

const sampleSuggestions: ComboboxSuggestion[] = [
  { value: "react", label: "React", description: "A JS library" },
  { value: "redux", label: "Redux", description: "State management" },
  { value: "vue", label: "Vue.js" },
  { value: "angular", label: "Angular" },
];

function renderCombo(
  overrides: Partial<React.ComponentProps<typeof ComboboxInput>> = {},
) {
  const props = {
    value: "",
    onChange: vi.fn(),
    suggestions: sampleSuggestions,
    ...overrides,
  };
  act(() => {
    root.render(React.createElement(ComboboxInput, props));
  });
  return props;
}

function getInput(): HTMLInputElement {
  return container.querySelector("input")!;
}

function fireChange(input: HTMLInputElement, value: string) {
  const nativeSet = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    "value",
  )!.set!;
  nativeSet.call(input, value);
  act(() => {
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

function fireKeyDown(input: HTMLInputElement, key: string) {
  act(() => {
    input.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true }),
    );
  });
}

describe("ComboboxInput", () => {
  it("renders input with placeholder", () => {
    renderCombo({ placeholder: "Search…" });
    const input = getInput();
    expect(input.placeholder).toBe("Search…");
  });

  it("shows suggestions dropdown on focus", () => {
    renderCombo({ value: "" });
    const input = getInput();
    act(() => input.focus());
    // All 4 suggestions match empty string
    const items = container.querySelectorAll("li");
    expect(items.length).toBe(4);
  });

  it("filters suggestions by input value", () => {
    renderCombo({ value: "re" });
    const input = getInput();
    act(() => input.focus());
    // "re" matches: React (label), redux (value) — also Angular doesn't match
    const items = container.querySelectorAll("li");
    const labels = Array.from(items).map((li) => li.querySelector(".font-medium")?.textContent);
    expect(labels).toContain("React");
    expect(labels).toContain("Redux");
    expect(labels).not.toContain("Angular");
  });

  it("calls onChange when suggestion is clicked", () => {
    const props = renderCombo({ value: "" });
    const input = getInput();
    act(() => input.focus());
    const firstItem = container.querySelector("li");
    act(() => {
      firstItem!.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    });
    expect(props.onChange).toHaveBeenCalledWith("react");
  });

  it("closes dropdown on click outside", () => {
    renderCombo({ value: "" });
    const input = getInput();
    act(() => input.focus());
    expect(container.querySelectorAll("li").length).toBeGreaterThan(0);
    // Simulate click outside
    act(() => {
      document.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    });
    expect(container.querySelectorAll("li").length).toBe(0);
  });

  it("navigates suggestions with arrow keys and selects with Enter", () => {
    const props = renderCombo({ value: "" });
    const input = getInput();
    act(() => input.focus());
    // Arrow down to first item
    fireKeyDown(input, "ArrowDown");
    // First item should be highlighted (activeIdx = 0)
    const items = container.querySelectorAll("li");
    expect(items[0].className).toContain("bg-accent");
    // Press Enter to select
    fireKeyDown(input, "Enter");
    expect(props.onChange).toHaveBeenCalledWith("react");
  });

  it("wraps around when arrowing past last suggestion", () => {
    renderCombo({ value: "re" });
    const input = getInput();
    act(() => input.focus());
    // 2 suggestions: react, redux
    fireKeyDown(input, "ArrowDown"); // idx 0
    fireKeyDown(input, "ArrowDown"); // idx 1
    fireKeyDown(input, "ArrowDown"); // wraps to 0
    const items = container.querySelectorAll("li");
    expect(items[0].className).toContain("bg-accent");
  });

  it("closes dropdown on Escape", () => {
    renderCombo({ value: "" });
    const input = getInput();
    act(() => input.focus());
    expect(container.querySelectorAll("li").length).toBeGreaterThan(0);
    fireKeyDown(input, "Escape");
    expect(container.querySelectorAll("li").length).toBe(0);
  });

  it("shows no dropdown when no suggestions match", () => {
    renderCombo({ value: "zzz" });
    const input = getInput();
    act(() => input.focus());
    expect(container.querySelectorAll("li").length).toBe(0);
  });

  it("renders suggestion descriptions when present", () => {
    renderCombo({ value: "react" });
    const input = getInput();
    act(() => input.focus());
    // React has description
    expect(container.textContent).toContain("A JS library");
  });
});
