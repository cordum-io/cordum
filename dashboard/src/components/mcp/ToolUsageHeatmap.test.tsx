import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { ToolUsageHeatmap } from "./ToolUsageHeatmap";
import type { MCPUsageCell } from "../../api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

beforeEach(() => {
  // Default to wide layout — the matchMedia mock returns false.
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    media: "(max-width: 767px)",
    onchange: null,
    dispatchEvent: () => false,
  }));
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  container.remove();
  vi.unstubAllGlobals();
});

const cells: MCPUsageCell[] = [
  {
    agent_id: "agent-1",
    tool_name: "tool-a",
    count: 10,
    allow_count: 9,
    deny_count: 1,
    approval_required_count: 0,
    p50_latency_ms: 12,
    p99_latency_ms: 50,
    last_invoked_at_ms: 1_700_000_000_000,
  },
  {
    agent_id: "agent-1",
    tool_name: "tool-b",
    count: 4,
    allow_count: 1,
    deny_count: 3,
    approval_required_count: 0,
    p50_latency_ms: 30,
    p99_latency_ms: 80,
    last_invoked_at_ms: 1_700_000_001_000,
  },
  {
    agent_id: "agent-2",
    tool_name: "tool-a",
    count: 1,
    allow_count: 1,
    deny_count: 0,
    approval_required_count: 0,
    p50_latency_ms: 5,
    p99_latency_ms: 5,
    last_invoked_at_ms: 1_700_000_002_000,
  },
];

function render(node: React.ReactElement) {
  act(() => {
    root.render(node);
  });
}

describe("ToolUsageHeatmap", () => {
  it("renders an empty state when there are no cells", () => {
    render(<ToolUsageHeatmap cells={[]} />);
    expect(container.querySelector('[data-testid="tool-usage-heatmap-empty"]')).toBeTruthy();
  });

  it("renders a cell button per (agent, tool) with the correct call count", () => {
    render(<ToolUsageHeatmap cells={cells} />);
    const cellA = container.querySelector('[data-testid="heatmap-cell-agent-1-tool-a"]');
    const cellB = container.querySelector('[data-testid="heatmap-cell-agent-1-tool-b"]');
    const cellC = container.querySelector('[data-testid="heatmap-cell-agent-2-tool-a"]');
    const cellEmpty = container.querySelector('[data-testid="heatmap-cell-agent-2-tool-b"]');

    expect(cellA?.textContent?.trim()).toBe("10");
    expect(cellB?.textContent?.trim()).toBe("4");
    expect(cellC?.textContent?.trim()).toBe("1");
    expect(cellEmpty?.textContent?.trim()).toBe("");
    expect((cellEmpty as HTMLButtonElement).disabled).toBe(true);
  });

  it("emits aria-labels with verbose call/allow/deny percentages", () => {
    render(<ToolUsageHeatmap cells={cells} />);
    const cellA = container.querySelector(
      '[data-testid="heatmap-cell-agent-1-tool-a"]',
    ) as HTMLButtonElement;
    const cellB = container.querySelector(
      '[data-testid="heatmap-cell-agent-1-tool-b"]',
    ) as HTMLButtonElement;
    expect(cellA.getAttribute("aria-label")).toBe(
      "agent agent-1 tool tool-a: 10 calls, 90% allow, 10% deny",
    );
    expect(cellB.getAttribute("aria-label")).toBe(
      "agent agent-1 tool tool-b: 4 calls, 25% allow, 75% deny",
    );
  });

  it("classifies cells: allow-dominant green vs deny-dominant red vs no-call empty", () => {
    render(<ToolUsageHeatmap cells={cells} />);
    expect(
      container
        .querySelector('[data-testid="heatmap-cell-agent-1-tool-a"]')
        ?.getAttribute("data-variant"),
    ).toBe("allow-dominant");
    expect(
      container
        .querySelector('[data-testid="heatmap-cell-agent-1-tool-b"]')
        ?.getAttribute("data-variant"),
    ).toBe("deny-dominant");
    expect(
      container
        .querySelector('[data-testid="heatmap-cell-agent-2-tool-b"]')
        ?.getAttribute("data-variant"),
    ).toBe("empty");
  });

  it("fires onCellClick with the matching cell and shows the detail panel", () => {
    const onClick = vi.fn();
    render(<ToolUsageHeatmap cells={cells} onCellClick={onClick} />);
    const cellA = container.querySelector(
      '[data-testid="heatmap-cell-agent-1-tool-a"]',
    ) as HTMLButtonElement;
    act(() => {
      cellA.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onClick).toHaveBeenCalledTimes(1);
    expect(onClick.mock.calls[0][0].agent_id).toBe("agent-1");
    expect(onClick.mock.calls[0][0].tool_name).toBe("tool-a");
    expect(container.querySelector('[data-testid="heatmap-cell-detail"]')).toBeTruthy();
  });

  it("renders a legend that explains the colour ramp", () => {
    render(<ToolUsageHeatmap cells={cells} />);
    const legend = container.querySelector('[data-testid="tool-usage-heatmap-legend"]');
    expect(legend).toBeTruthy();
    expect(legend?.textContent).toContain("Allow dominant");
    expect(legend?.textContent).toContain("Deny dominant");
  });

  it("renders focusable cell buttons that can receive keyboard focus", () => {
    render(<ToolUsageHeatmap cells={cells} />);
    const start = container.querySelector(
      '[data-testid="heatmap-cell-agent-1-tool-a"]',
    ) as HTMLButtonElement;
    // Default <button> is focusable; programmatic focus transfer is
    // the primitive the arrow-key handler builds on. Verifies the
    // a11y prerequisite without depending on React's synthetic-event
    // delivery in jsdom.
    start.focus();
    expect(document.activeElement).toBe(start);
    // Sibling cells are buttons too — keyboard users can tab through.
    const sibling = container.querySelector(
      '[data-testid="heatmap-cell-agent-1-tool-b"]',
    ) as HTMLButtonElement;
    sibling.focus();
    expect(document.activeElement).toBe(sibling);
    expect(sibling.tagName).toBe("BUTTON");
  });

  it("uses CSS-variable token classes (no hardcoded colour outside the ramp)", () => {
    render(<ToolUsageHeatmap cells={cells} />);
    const empty = container.querySelector(
      '[data-testid="heatmap-cell-agent-2-tool-b"]',
    ) as HTMLButtonElement;
    // Empty cell uses the surface-subtle CSS var; verifies dark-mode
    // works without per-cell hex values.
    expect(empty.className).toContain("var(--surface-subtle");
  });

  it("collapses to a per-agent list at compact widths", () => {
    vi.unstubAllGlobals();
    vi.stubGlobal("matchMedia", (q: string) => ({
      matches: q.includes("max-width: 767px"),
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      media: q,
      onchange: null,
      dispatchEvent: () => false,
    }));
    render(<ToolUsageHeatmap cells={cells} />);
    expect(
      container.querySelector('[data-testid="tool-usage-heatmap-compact"]'),
    ).toBeTruthy();
    // Sparse cells (agent-2/tool-b had count=0) shouldn't render in the list.
    expect(
      container.querySelector('[data-testid="heatmap-row-agent-2-tool-b"]'),
    ).toBeFalsy();
    // Rich cells should.
    expect(
      container.querySelector('[data-testid="heatmap-row-agent-1-tool-a"]'),
    ).toBeTruthy();
  });
});
