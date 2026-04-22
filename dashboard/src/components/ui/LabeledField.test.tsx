import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { LabeledField } from "./LabeledField";

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

describe("LabeledField", () => {
  it("renders label, description, action, and child content", () => {
    act(() => {
      root.render(
        <LabeledField
          label="Search"
          description="Filter the current list"
          action={<button type="button">Clear</button>}
        >
          <input aria-label="search field" />
        </LabeledField>,
      );
    });

    expect(container.textContent).toContain("Search");
    expect(container.textContent).toContain("Filter the current list");
    expect(container.textContent).toContain("Clear");
    expect(container.querySelector('input[aria-label="search field"]')).not.toBeNull();
  });
});
