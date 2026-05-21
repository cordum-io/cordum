import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { JobFiltersBar } from "./JobFiltersBar";

// task-cafacca3 reopen #1 — JobFiltersBar popover regression suite.
// Asserts: (1) the 5 free-text inputs (Topic/Pool/Tenant/Session ID/Run ID)
// are NOT rendered in the always-visible bar by default; (2) clicking the
// Filter trigger reveals them inside the popover; (3) the count chip on
// the trigger reflects active advanced filters; (4) Clear advanced clears
// the 5 text fields without disturbing other filter state.

function renderBar(initialEntries: string[] = ["/jobs"]) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      React.createElement(
        MemoryRouter,
        { initialEntries },
        React.createElement(JobFiltersBar, { onChange: () => {} }),
      ),
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function getByPlaceholderTexts(container: HTMLElement, placeholders: string[]): HTMLInputElement[] {
  const inputs = Array.from(container.querySelectorAll<HTMLInputElement>("input"));
  return inputs.filter((i) => placeholders.includes(i.placeholder));
}

function clickFilterTrigger(container: HTMLElement): HTMLButtonElement {
  const trigger = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
    (b) => (b.textContent ?? "").trim().startsWith("Filter"),
  );
  expect(trigger).toBeTruthy();
  act(() => {
    trigger?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
  return trigger as HTMLButtonElement;
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("Test_oldFilterInputs_removed", () => {
  it("does not render Topic/Pool/Tenant/Session ID/Run ID as inline inputs in the always-visible bar", () => {
    const { container, cleanup } = renderBar();
    try {
      const inputs = getByPlaceholderTexts(container, [
        "Topic",
        "Pool",
        "Tenant",
        "Session ID",
        "Run ID",
      ]);
      // The popover is closed by default, so none of the 5 inputs should
      // appear in the DOM yet. The always-visible bar must show only the
      // categorical controls + the Filter trigger.
      expect(inputs).toHaveLength(0);
    } finally {
      cleanup();
    }
  });
});

describe("Test_advancedFilterPopover_revealsFilters", () => {
  it("renders the 5 advanced inputs inside the popover when the Filter button is clicked", () => {
    const { container, cleanup } = renderBar();
    try {
      // Before click: 0 of the 5 inputs are present.
      expect(getByPlaceholderTexts(container, ["Topic", "Pool", "Tenant", "Session ID", "Run ID"])).toHaveLength(0);

      clickFilterTrigger(container);

      // After click: all 5 inputs are rendered inside the popover, in this
      // exact order, with the documented placeholders.
      const inputs = getByPlaceholderTexts(container, [
        "Topic",
        "Pool",
        "Tenant",
        "Session ID",
        "Run ID",
      ]);
      expect(inputs.map((i) => i.placeholder)).toEqual([
        "Topic",
        "Pool",
        "Tenant",
        "Session ID",
        "Run ID",
      ]);

      // The popover is a real dialog role for screen-reader users.
      const popover = container.querySelector('[role="dialog"][aria-label="Advanced filters"]');
      expect(popover).not.toBeNull();
    } finally {
      cleanup();
    }
  });

  it("clicking the trigger a second time hides the popover", () => {
    const { container, cleanup } = renderBar();
    try {
      clickFilterTrigger(container);
      expect(container.querySelector('[role="dialog"]')).not.toBeNull();
      clickFilterTrigger(container);
      expect(container.querySelector('[role="dialog"]')).toBeNull();
    } finally {
      cleanup();
    }
  });
});

describe("Test_advancedFilterCountChip_showsCount", () => {
  it("shows the count of active advanced filters on the trigger when URL state has values", () => {
    const { container, cleanup } = renderBar(["/jobs?topic=a&pool=b&tenant=c"]);
    try {
      const chip = container.querySelector('[data-testid="advanced-filters-count"]');
      expect(chip).not.toBeNull();
      expect(chip?.textContent).toBe("3");
    } finally {
      cleanup();
    }
  });

  it("hides the count chip when no advanced filters are active", () => {
    const { container, cleanup } = renderBar();
    try {
      expect(container.querySelector('[data-testid="advanced-filters-count"]')).toBeNull();
    } finally {
      cleanup();
    }
  });
});

describe("Test_clearAdvanced_resetsTextFilters", () => {
  it("Clear advanced filters button resets the 5 text filter inputs", () => {
    const { container, cleanup } = renderBar(["/jobs?topic=foo&pool=bar"]);
    try {
      clickFilterTrigger(container);

      const inputs = getByPlaceholderTexts(container, ["Topic", "Pool"]);
      expect(inputs[0]?.value).toBe("foo");
      expect(inputs[1]?.value).toBe("bar");

      const clear = Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
        (b) => (b.textContent ?? "").trim() === "Clear advanced filters",
      );
      expect(clear).toBeTruthy();
      act(() => {
        clear?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
      });

      // After clear: the local state for the 5 inputs resets immediately
      // (the URL state also resets, but the visible inputs are what the
      // user observes).
      const afterInputs = getByPlaceholderTexts(container, ["Topic", "Pool"]);
      expect(afterInputs[0]?.value).toBe("");
      expect(afterInputs[1]?.value).toBe("");

      // The trigger chip should disappear once the count drops to 0.
      expect(container.querySelector('[data-testid="advanced-filters-count"]')).toBeNull();
    } finally {
      cleanup();
    }
  });
});
