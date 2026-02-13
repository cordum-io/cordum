import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Drawer } from "./Drawer";

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

function renderDrawer(
  overrides: Partial<Omit<React.ComponentProps<typeof Drawer>, "children">> = {},
) {
  const props = {
    open: true,
    onClose: vi.fn(),
    children: React.createElement("p", null, "Drawer content"),
    ...overrides,
  };
  act(() => {
    root.render(React.createElement(Drawer, props));
  });
  return props;
}

describe("Drawer", () => {
  it("renders nothing when open is false", () => {
    renderDrawer({ open: false });
    expect(container.innerHTML).toBe("");
  });

  it("renders children when open", () => {
    renderDrawer({ open: true });
    expect(container.textContent).toContain("Drawer content");
  });

  it("renders backdrop overlay", () => {
    renderDrawer({ open: true });
    // Backdrop is a button with aria-label Close
    const backdrop = container.querySelector('button[aria-label="Close"]');
    expect(backdrop).not.toBeNull();
  });

  it("calls onClose when backdrop clicked", () => {
    const props = renderDrawer({ open: true });
    const backdrop = container.querySelector('button[aria-label="Close"]')!;
    act(() => (backdrop as HTMLElement).click());
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it("applies default lg size class", () => {
    renderDrawer({ open: true });
    const panel = container.querySelector(".max-w-lg");
    expect(panel).not.toBeNull();
  });

  it("applies sm size class", () => {
    renderDrawer({ open: true, size: "sm" });
    const panel = container.querySelector(".max-w-sm");
    expect(panel).not.toBeNull();
  });

  it("applies xl size class", () => {
    renderDrawer({ open: true, size: "xl" });
    const panel = container.querySelector(".max-w-xl");
    expect(panel).not.toBeNull();
  });

  it("applies full size class", () => {
    renderDrawer({ open: true, size: "full" });
    const panel = container.querySelector(".max-w-full");
    expect(panel).not.toBeNull();
  });
});
