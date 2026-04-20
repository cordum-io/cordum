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
    expect(span.className).toContain("bg-surface2");
    expect(span.className).toContain("text-ink");
  });

  it("applies success variant styling", () => {
    renderBadge({ variant: "success" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("text-success");
  });

  it("applies warning variant styling", () => {
    renderBadge({ variant: "warning" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("text-warning");
  });

  it("applies danger variant styling", () => {
    renderBadge({ variant: "danger" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("text-danger");
  });

  it("applies info variant styling", () => {
    renderBadge({ variant: "info" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("text-accent");
  });

  it("applies enterprise variant styling", () => {
    renderBadge({ variant: "enterprise" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("text-primary");
  });

  it("merges custom className", () => {
    renderBadge({ className: "my-custom-class" });
    const span = container.querySelector("span")!;
    expect(span.className).toContain("my-custom-class");
    // Still has base classes
    expect(span.className).toContain("rounded-full");
  });

  it("has correct base styling", () => {
    renderBadge({});
    const span = container.querySelector("span")!;
    expect(span.className).toContain("inline-flex");
    expect(span.className).toContain("rounded-full");
    expect(span.className).toContain("text-xs");
    expect(span.className).toContain("font-semibold");
  });
});
