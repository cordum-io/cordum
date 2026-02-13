import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ConfirmDialog } from "./ConfirmDialog";

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

const defaultProps = {
  open: true,
  title: "Delete Item",
  message: "Are you sure?",
  onConfirm: vi.fn(),
  onCancel: vi.fn(),
};

function renderDialog(overrides: Partial<React.ComponentProps<typeof ConfirmDialog>> = {}) {
  act(() => {
    root.render(React.createElement(ConfirmDialog, { ...defaultProps, ...overrides }));
  });
}

describe("ConfirmDialog", () => {
  it("renders nothing when open is false", () => {
    renderDialog({ open: false });
    expect(container.innerHTML).toBe("");
  });

  it("renders overlay with title and message when open", () => {
    renderDialog();
    expect(container.textContent).toContain("Delete Item");
    expect(container.textContent).toContain("Are you sure?");
  });

  it("shows default confirmLabel 'Confirm'", () => {
    renderDialog();
    const buttons = Array.from(container.querySelectorAll("button"));
    const confirmBtn = buttons.find((b) => b.textContent?.includes("Confirm"));
    expect(confirmBtn).toBeDefined();
  });

  it("shows custom confirmLabel", () => {
    renderDialog({ confirmLabel: "Delete Forever" });
    const buttons = Array.from(container.querySelectorAll("button"));
    const btn = buttons.find((b) => b.textContent?.includes("Delete Forever"));
    expect(btn).toBeDefined();
  });

  it("calls onConfirm when confirm button clicked", () => {
    const onConfirm = vi.fn();
    renderDialog({ onConfirm });
    const buttons = Array.from(container.querySelectorAll("button"));
    const confirmBtn = buttons.find((b) => b.textContent?.includes("Confirm"));
    act(() => confirmBtn!.click());
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it("calls onCancel when Cancel button clicked", () => {
    const onCancel = vi.fn();
    renderDialog({ onCancel });
    const buttons = Array.from(container.querySelectorAll("button"));
    const cancelBtn = buttons.find((b) => b.textContent?.trim() === "Cancel");
    act(() => cancelBtn!.click());
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("calls onCancel when X button clicked", () => {
    const onCancel = vi.fn();
    renderDialog({ onCancel });
    // X button has an SVG inside and no text
    const buttons = Array.from(container.querySelectorAll("button"));
    const xBtn = buttons.find((b) => b.querySelector("svg") && !b.textContent?.trim());
    expect(xBtn).toBeDefined();
    act(() => xBtn!.click());
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("shows 'Working...' and disables buttons when isPending", () => {
    renderDialog({ isPending: true });
    expect(container.textContent).toContain("Working...");
    const buttons = Array.from(container.querySelectorAll("button"));
    const disabledBtns = buttons.filter((b) => b.disabled);
    expect(disabledBtns.length).toBeGreaterThanOrEqual(2);
  });
});
