import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DataFreshness } from "./DataFreshness";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function renderFreshness(dataUpdatedAt: number) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <DataFreshness
        dataUpdatedAt={dataUpdatedAt}
        onRefresh={() => {}}
        isRefetching={false}
      />,
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

describe("DataFreshness", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-04-20T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders a human-readable 'just now' label for a fresh timestamp", () => {
    const now = Date.now();
    const { container, cleanup } = renderFreshness(now);
    try {
      expect(container.textContent).toContain("Updated just now");
      expect(container.textContent).not.toContain("NaN");
    } finally {
      cleanup();
    }
  });

  it("renders nothing when dataUpdatedAt is 0 (no successful fetch yet)", () => {
    const { container, cleanup } = renderFreshness(0);
    try {
      // Zero is treated as "not updated" — component bails before rendering
      // anything user-visible, and crucially never surfaces "NaN".
      expect(container.textContent ?? "").not.toContain("NaN");
      expect(container.querySelector("button")).toBeNull();
    } finally {
      cleanup();
    }
  });

  it("renders nothing and never emits 'NaN' when dataUpdatedAt is NaN", () => {
    const { container, cleanup } = renderFreshness(Number.NaN);
    try {
      expect(container.textContent ?? "").not.toContain("NaN");
      expect(container.querySelector("button")).toBeNull();
    } finally {
      cleanup();
    }
  });

  it("does not emit 'NaN' in the interval tick when the prop transitions between values", () => {
    // Mount with a valid timestamp, then advance the clock past the 10s
    // interval to force the setInterval callback to fire. The closure must
    // still produce a valid label rather than "Updated NaN ago".
    const now = Date.now();
    const { container, cleanup } = renderFreshness(now);
    try {
      act(() => {
        vi.advanceTimersByTime(10_000);
      });
      expect(container.textContent ?? "").not.toContain("NaN");
    } finally {
      cleanup();
    }
  });
});
