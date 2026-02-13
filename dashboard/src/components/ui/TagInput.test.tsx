import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { TagInput } from "./TagInput";

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

function renderTagInput(
  overrides: Partial<React.ComponentProps<typeof TagInput>> = {},
) {
  const props = {
    value: [] as string[],
    onChange: vi.fn(),
    ...overrides,
  };
  act(() => {
    root.render(React.createElement(TagInput, props));
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

describe("TagInput", () => {
  it("renders placeholder when value is empty", () => {
    renderTagInput({ placeholder: "Add tag…" });
    const input = getInput();
    expect(input.placeholder).toBe("Add tag…");
  });

  it("hides placeholder when tags exist", () => {
    renderTagInput({ value: ["alpha"] });
    const input = getInput();
    expect(input.placeholder).toBe("");
  });

  it("adds tag via Enter key and calls onChange", () => {
    const props = renderTagInput({ value: [] });
    const input = getInput();
    fireChange(input, "newTag");
    fireKeyDown(input, "Enter");
    expect(props.onChange).toHaveBeenCalledWith(["newTag"]);
  });

  it("adds tag via comma key and calls onChange", () => {
    const props = renderTagInput({ value: ["existing"] });
    const input = getInput();
    fireChange(input, "second");
    fireKeyDown(input, ",");
    expect(props.onChange).toHaveBeenCalledWith(["existing", "second"]);
  });

  it("prevents duplicate tags", () => {
    const props = renderTagInput({ value: ["dup"] });
    const input = getInput();
    fireChange(input, "dup");
    fireKeyDown(input, "Enter");
    expect(props.onChange).not.toHaveBeenCalled();
  });

  it("enforces maxTags limit", () => {
    const props = renderTagInput({ value: ["a", "b"], maxTags: 2 });
    const input = getInput();
    fireChange(input, "c");
    fireKeyDown(input, "Enter");
    expect(props.onChange).not.toHaveBeenCalled();
  });

  it("removes tag when X button clicked", () => {
    const props = renderTagInput({ value: ["alpha", "beta", "gamma"] });
    // Each tag has an X button inside the Badge
    const xButtons = Array.from(container.querySelectorAll("button")).filter(
      (b) => b.querySelector("svg"),
    );
    expect(xButtons).toHaveLength(3);
    act(() => xButtons[1].click());
    expect(props.onChange).toHaveBeenCalledWith(["alpha", "gamma"]);
  });

  it("removes last tag on Backspace when input is empty", () => {
    const props = renderTagInput({ value: ["first", "second"] });
    const input = getInput();
    // Input is already empty by default
    fireKeyDown(input, "Backspace");
    expect(props.onChange).toHaveBeenCalledWith(["first"]);
  });

  it("does not add whitespace-only input", () => {
    const props = renderTagInput({ value: [] });
    const input = getInput();
    fireChange(input, "   ");
    fireKeyDown(input, "Enter");
    expect(props.onChange).not.toHaveBeenCalled();
  });

  it("shows filtered suggestions dropdown", () => {
    renderTagInput({
      value: [],
      suggestions: ["react", "redux", "router", "vue"],
    });
    const input = getInput();
    // Focus + type to trigger suggestions
    act(() => input.focus());
    fireChange(input, "re");
    // Suggestions matching "re": react, redux
    const listItems = container.querySelectorAll("li");
    expect(listItems.length).toBe(2);
    expect(listItems[0].textContent).toBe("react");
    expect(listItems[1].textContent).toBe("redux");
  });

  it("adds tag when suggestion clicked", () => {
    const props = renderTagInput({
      value: [],
      suggestions: ["react", "vue"],
    });
    const input = getInput();
    act(() => input.focus());
    fireChange(input, "re");
    const suggestionBtn = container.querySelector("li button");
    expect(suggestionBtn).toBeDefined();
    act(() => (suggestionBtn as HTMLButtonElement).click());
    expect(props.onChange).toHaveBeenCalledWith(["react"]);
  });

  it("excludes already-selected tags from suggestions", () => {
    renderTagInput({
      value: ["react"],
      suggestions: ["react", "redux", "vue"],
    });
    const input = getInput();
    act(() => input.focus());
    fireChange(input, "r");
    const listItems = container.querySelectorAll("li");
    // "react" already in value, only "redux" should show
    expect(listItems.length).toBe(1);
    expect(listItems[0].textContent).toBe("redux");
  });
});
