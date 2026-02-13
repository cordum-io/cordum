import { afterEach, beforeEach, describe, expect, it } from "vitest";

import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { CollapsibleSection } from "./CollapsibleSection";

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

function renderSection(
  overrides: Partial<Omit<React.ComponentProps<typeof CollapsibleSection>, "children">> = {},
) {
  const props = {
    title: "Section Title",
    children: React.createElement("p", null, "Section body"),
    ...overrides,
  };
  act(() => {
    root.render(React.createElement(CollapsibleSection, props));
  });
}

describe("CollapsibleSection", () => {
  it("renders title text", () => {
    renderSection({ title: "Details" });
    expect(container.textContent).toContain("Details");
  });

  it("shows children by default (defaultOpen=true)", () => {
    renderSection();
    expect(container.textContent).toContain("Section body");
  });

  it("hides children when defaultOpen is false", () => {
    renderSection({ defaultOpen: false });
    expect(container.textContent).not.toContain("Section body");
  });

  it("toggles children on click", () => {
    renderSection();
    // Initially open
    expect(container.textContent).toContain("Section body");
    // Click to collapse
    const button = container.querySelector("button")!;
    act(() => button.click());
    expect(container.textContent).not.toContain("Section body");
    // Click to expand
    act(() => button.click());
    expect(container.textContent).toContain("Section body");
  });

  it("renders badge when provided", () => {
    renderSection({
      badge: React.createElement("span", { "data-testid": "badge" }, "3"),
    });
    expect(container.textContent).toContain("3");
  });

  it("applies custom className", () => {
    renderSection({ className: "my-section" });
    const outerDiv = container.firstElementChild as HTMLElement;
    expect(outerDiv.className).toContain("my-section");
  });

  it("rotates chevron icon when open", () => {
    renderSection({ defaultOpen: true });
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("class")).toContain("rotate-180");
  });

  it("does not rotate chevron when collapsed", () => {
    renderSection({ defaultOpen: false });
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("class")).not.toContain("rotate-180");
  });
});
