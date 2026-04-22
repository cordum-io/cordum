import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { GovernanceHealth } from "../../api/types";
import { GovernanceHealthCard, GovernanceHealthIndicator } from "./GovernanceHealthIndicator";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    data: undefined as GovernanceHealth | undefined,
    isLoading: false,
    error: null as { status?: number } | null,
  },
}));

vi.mock("../../hooks/useGovernanceHealth", () => ({
  useGovernanceHealth: () => hookState,
}));

function render(ui: React.ReactElement) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(ui));
  return { container, root };
}

function cleanup(ctx: { container: HTMLElement; root: ReturnType<typeof createRoot> }) {
  act(() => ctx.root.unmount());
  ctx.container.remove();
}

function buildHealth(partial: Partial<GovernanceHealth>): GovernanceHealth {
  return {
    score: 85,
    grade: "B",
    generated_at: "2026-04-17T12:00:00Z",
    factors: {
      denial_rate: { score: 90, weight: 25 },
      approval_latency_p95: { score: 85, weight: 25 },
      policy_coverage: { score: 80, weight: 25 },
      chain_integrity: { score: 100, weight: 25 },
    },
    ...partial,
  };
}

describe("GovernanceHealthIndicator", () => {
  beforeEach(() => {
    hookState.data = undefined;
    hookState.isLoading = false;
    hookState.error = null;
  });

  it("renders a loading skeleton while fetching", () => {
    hookState.isLoading = true;
    const ctx = render(<GovernanceHealthIndicator />);
    expect(ctx.container.querySelector('[data-testid="governance-health-loading"]')).not.toBeNull();
    cleanup(ctx);
  });

  it("renders an error card when the fetch fails non-403", () => {
    hookState.error = { status: 500 };
    const ctx = render(<GovernanceHealthIndicator />);
    expect(ctx.container.querySelector('[data-testid="governance-health-error"]')).not.toBeNull();
    cleanup(ctx);
  });

  it("renders nothing for non-admin (403)", () => {
    hookState.error = { status: 403 };
    const ctx = render(<GovernanceHealthIndicator />);
    expect(ctx.container.querySelector('[data-testid="governance-health"]')).toBeNull();
    expect(ctx.container.querySelector('[data-testid="governance-health-error"]')).toBeNull();
    cleanup(ctx);
  });
});

describe("GovernanceHealthCard", () => {
  function testGrade(grade: GovernanceHealth["grade"], score: number) {
    const ctx = render(<GovernanceHealthCard health={buildHealth({ grade, score })} />);
    const el = ctx.container.querySelector('[data-testid="governance-health"]');
    expect(el).not.toBeNull();
    expect(el?.getAttribute("aria-label")).toContain(`grade ${grade}`);
    expect(el?.getAttribute("aria-label")).toContain(`${score} out of 100`);
    const scoreEl = ctx.container.querySelector('[data-testid="governance-health-score"]');
    expect(scoreEl?.textContent).toBe(String(score));
    cleanup(ctx);
  }

  it("renders grade A (score 95)", () => testGrade("A", 95));
  it("renders grade B (score 85)", () => testGrade("B", 85));
  it("renders grade C (score 75)", () => testGrade("C", 75));
  it("renders grade D (score 65)", () => testGrade("D", 65));
  it("renders grade F (score 40)", () => testGrade("F", 40));

  it("floors to F-band visuals when chain is compromised", () => {
    // The score-cap logic lives server-side; here we assert that the
    // widget honours whatever {score, grade} the API returns. A
    // compromised chain produces score≤55, grade=F.
    const ctx = render(<GovernanceHealthCard health={buildHealth({ grade: "F", score: 55 })} />);
    const el = ctx.container.querySelector('[data-testid="governance-health"]');
    expect(el?.getAttribute("aria-label")).toContain("grade F");
    cleanup(ctx);
  });

  it("marks the widget as approximate when truncated_at_max", () => {
    const ctx = render(
      <GovernanceHealthCard health={buildHealth({ truncated_at_max: true })} />,
    );
    expect(ctx.container.textContent).toContain("approx.");
    cleanup(ctx);
  });

  it("renders em-dash for factors carrying notes (unavailable)", () => {
    const ctx = render(
      <GovernanceHealthCard
        health={buildHealth({
          factors: {
            denial_rate: { score: 70, weight: 25, notes: "unavailable" },
          },
        })}
      />,
    );
    expect(ctx.container.textContent).toContain("—");
    cleanup(ctx);
  });
});
