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

describe("ComboboxInput", () => {
  it("renders input with placeholder and className", () => {
    renderCombo({ placeholder: "Search…", className: "combo-input" });
    const input = getInput();
    expect(input.placeholder).toBe("Search…");
    expect(input.className).toContain("combo-input");
  });

  it("renders controlled value", () => {
    renderCombo({ value: "react" });
    const input = getInput();
    expect(input.value).toBe("react");
  });

  it("calls onChange with typed input value", () => {
    const props = renderCombo({ value: "" });
    const input = getInput();
    fireChange(input, "redux");
    expect(props.onChange).toHaveBeenCalledWith("redux");
  });

  it("accepts suggestions prop without rendering suggestion list UI", () => {
    renderCombo({ value: "" });
    expect(container.querySelector("input")).not.toBeNull();
    expect(container.querySelectorAll("li")).toHaveLength(0);
  });
});
