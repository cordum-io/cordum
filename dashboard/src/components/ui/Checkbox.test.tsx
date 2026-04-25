import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { Checkbox } from "./Checkbox";

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

describe("Checkbox", () => {
  it("renders the checkbox with label content", () => {
    act(() => {
      root.render(<Checkbox label="Retry item" aria-label="Retry item" />);
    });

    expect(container.querySelector('input[type="checkbox"]')).not.toBeNull();
    expect(container.textContent).toContain("Retry item");
  });

  it("propagates change events", () => {
    const onChange = vi.fn();
    act(() => {
      root.render(
        <Checkbox
          checked={false}
          onChange={onChange}
          aria-label="Select row"
        />,
      );
    });

    const checkbox = container.querySelector(
      'input[type="checkbox"]',
    ) as HTMLInputElement | null;
    act(() => checkbox?.click());
    expect(onChange).toHaveBeenCalled();
  });
});
