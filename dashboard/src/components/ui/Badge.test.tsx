import { afterEach, beforeEach, describe, expect, it } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Badge } from "./Badge";

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

function renderBadge(
  overrides: Partial<React.ComponentProps<typeof Badge>> = {},
  children: string = "Label",
) {
  act(() => {
    root.render(React.createElement(Badge, overrides, children));
  });
}

describe("Badge", () => {
  it("renders children text", () => {
    renderBadge({}, "Active");
    expect(container.textContent).toBe("Active");
  });

  it("applies default variant styling", () => {
    renderBadge({});
    const span = container.querySelector("span")!;
    expect(span.className).toContain("bg-surface-2");
    expect(span.className).toContain("text-foreground");
  });

  it("applies success variant opacity model", () => {
    renderBadge({ variant: "success" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("bg-status-success-bg");
    expect(span.className).toContain("text-success");
    expect(span.className).toContain("border-status-success-border");
  });

  it("applies warning variant opacity model", () => {
    renderBadge({ variant: "warning" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("bg-status-warning-bg");
    expect(span.className).toContain("text-warning");
  });

  it("applies danger variant opacity model", () => {
    renderBadge({ variant: "danger" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("bg-status-danger-bg");
    expect(span.className).toContain("text-danger");
  });

  it("applies info variant opacity model", () => {
    renderBadge({ variant: "info" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("bg-status-info-bg");
    expect(span.className).toContain("text-info");
  });

  it("renders with icon", () => {
    act(() => {
      root.render(<Badge icon={<span id="test-icon" />}>Label</Badge>);
    });
    expect(container.querySelector("#test-icon")).toBeTruthy();
  });

  it("merges custom className", () => {
    renderBadge({ className: "my-custom-class" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("my-custom-class");
    // Still has base classes
    expect(span.className).toContain("rounded-sm");
  });

  it("has correct base styling", () => {
    renderBadge({});
    const span = container.querySelector("span")!;
    expect(span.className).toContain("inline-flex");
    expect(span.className).toContain("rounded-sm");
    expect(span.className).toContain("text-[10px]");
    expect(span.className).toContain("font-semibold");
    expect(span.className).toContain("font-mono");
  });
});
