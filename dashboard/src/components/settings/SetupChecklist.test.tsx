import { describe, expect, it, vi } from "vitest";

// matchMedia must be defined before any component import (ui.ts uses it at module scope)
vi.hoisted(() => {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
});

import React from "react";
import { createRoot } from "react-dom/client";
import { act } from "react";
import { MemoryRouter } from "react-router-dom";
import { SetupChecklist } from "./SetupChecklist";
import type { ChecklistItem } from "../../hooks/useSetupStatus";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeItems(overrides: Partial<ChecklistItem>[] = []): ChecklistItem[] {
  const defaults: ChecklistItem[] = [
    { id: "api-key", label: "Create API key", route: "/settings/api-keys", completed: true, optional: false },
    { id: "workers", label: "Connect workers", route: "/agents", completed: false, optional: false },
    { id: "policy", label: "Configure policies", route: "/policies", completed: true, optional: false },
    { id: "nats", label: "Set up NATS", route: "/settings/system", completed: false, optional: false },
    { id: "dark-mode", label: "Try dark mode", route: "/settings", completed: false, optional: true },
  ];
  return defaults.map((d, i) => ({ ...d, ...(overrides[i] ?? {}) }));
}

function renderChecklist(props: {
  items?: ChecklistItem[];
  completedCount?: number;
  totalRequired?: number;
  onDismissForever?: () => void;
  onClose?: () => void;
}) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter>
        <SetupChecklist
          open={true}
          onClose={props.onClose ?? (() => {})}
          items={props.items ?? makeItems()}
          completedCount={props.completedCount ?? 2}
          totalRequired={props.totalRequired ?? 4}
          onDismissForever={props.onDismissForever ?? (() => {})}
        />
      </MemoryRouter>,
    );
  });

  return { container, root };
}

function cleanup(container: HTMLElement, root: ReturnType<typeof createRoot>) {
  act(() => root.unmount());
  container.remove();
}

// ---------------------------------------------------------------------------
// Percentage computation
// ---------------------------------------------------------------------------

describe("SetupChecklist percentage", () => {
  it("displays correct percentage (2/4 = 50%)", () => {
    const { container, root } = renderChecklist({
      completedCount: 2,
      totalRequired: 4,
    });
    expect(container.textContent).toContain("50%");
    expect(container.textContent).toContain("2 of 4 complete");
    cleanup(container, root);
  });

  it("displays 0% when nothing completed", () => {
    const { container, root } = renderChecklist({
      completedCount: 0,
      totalRequired: 5,
    });
    expect(container.textContent).toContain("0%");
    expect(container.textContent).toContain("0 of 5 complete");
    cleanup(container, root);
  });

  it("displays 100% when all completed", () => {
    const { container, root } = renderChecklist({
      completedCount: 4,
      totalRequired: 4,
    });
    expect(container.textContent).toContain("100%");
    cleanup(container, root);
  });

  it("handles 0 totalRequired without crashing (0%)", () => {
    const { container, root } = renderChecklist({
      completedCount: 0,
      totalRequired: 0,
      items: [],
    });
    expect(container.textContent).toContain("0%");
    cleanup(container, root);
  });

  it("rounds percentage (3/5 = 60%)", () => {
    const { container, root } = renderChecklist({
      completedCount: 3,
      totalRequired: 5,
    });
    expect(container.textContent).toContain("60%");
    cleanup(container, root);
  });
});

// ---------------------------------------------------------------------------
// Required vs optional item separation
// ---------------------------------------------------------------------------

describe("SetupChecklist item separation", () => {
  it("shows required items before optional section", () => {
    const { container, root } = renderChecklist({});
    const text = container.textContent ?? "";
    const reqIdx = text.indexOf("Create API key");
    const optIdx = text.indexOf("Optional");
    expect(reqIdx).toBeLessThan(optIdx);
    cleanup(container, root);
  });

  it("shows 'Optional' header when optional items exist", () => {
    const { container, root } = renderChecklist({});
    expect(container.textContent).toContain("Optional");
    cleanup(container, root);
  });

  it("hides 'Optional' header when no optional items", () => {
    const items = makeItems().filter((i) => !i.optional);
    const { container, root } = renderChecklist({ items });
    // Count occurrences — "Optional" badge text should not appear as a section header
    const optionalHeaders = container.querySelectorAll("p");
    const hasOptionalSection = Array.from(optionalHeaders).some(
      (p) => p.textContent?.trim() === "Optional",
    );
    expect(hasOptionalSection).toBe(false);
    cleanup(container, root);
  });
});

// ---------------------------------------------------------------------------
// Completed items vs incomplete items
// ---------------------------------------------------------------------------

describe("SetupChecklist item icons", () => {
  it("renders all items", () => {
    const items = makeItems();
    const { container, root } = renderChecklist({ items });
    for (const item of items) {
      expect(container.textContent).toContain(item.label);
    }
    cleanup(container, root);
  });

  it("completed items have line-through class", () => {
    const items = makeItems();
    const { container, root } = renderChecklist({ items });
    const links = container.querySelectorAll("a");
    for (const link of links) {
      const label = link.querySelector("span");
      const itemData = items.find((i) => label?.textContent === i.label);
      if (itemData?.completed) {
        expect(label?.className).toContain("line-through");
      } else {
        expect(label?.className).not.toContain("line-through");
      }
    }
    cleanup(container, root);
  });
});

// ---------------------------------------------------------------------------
// Items link to correct routes
// ---------------------------------------------------------------------------

describe("SetupChecklist routing", () => {
  it("each item links to its route", () => {
    const items = makeItems();
    const { container, root } = renderChecklist({ items });
    const links = container.querySelectorAll("a");
    const hrefs = Array.from(links).map((a) => a.getAttribute("href"));
    for (const item of items) {
      expect(hrefs).toContain(item.route);
    }
    cleanup(container, root);
  });
});

// ---------------------------------------------------------------------------
// Dismiss button
// ---------------------------------------------------------------------------

describe("SetupChecklist dismiss", () => {
  it("calls onDismissForever and onClose when 'Dismiss forever' is clicked", () => {
    const onDismiss = vi.fn();
    const onClose = vi.fn();
    const { container, root } = renderChecklist({
      onDismissForever: onDismiss,
      onClose,
    });
    const buttons = container.querySelectorAll("button");
    const dismissBtn = Array.from(buttons).find(
      (b) => b.textContent?.trim() === "Dismiss forever",
    );
    expect(dismissBtn).toBeDefined();
    act(() => {
      dismissBtn?.click();
    });
    expect(onDismiss).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
    cleanup(container, root);
  });
});
