import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen } from "@testing-library/dom";
import { act } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";
import { DecisionsChartsPanel } from "./DecisionsChartsPanel";

function buildDecisions(n: number): Decision[] {
  const out: Decision[] = [];
  for (let i = 0; i < n; i += 1) {
    out.push({
      source: i % 2 === 0 ? DecisionSource.job : DecisionSource.edge,
      rule_id: `rule.${i % 5}`,
      bundle_id: "bundle.acme",
      bundle_version: "v1",
      type:
        i % 3 === 0
          ? DecisionType.deny
          : i % 3 === 1
            ? DecisionType.allow
            : DecisionType.throttle,
      timestamp: new Date(Date.UTC(2026, 4, 10, 12, 0, i % 60)).toISOString(),
      audit_hash: `sha256:${i.toString(16)}`,
      input_ref: `blob://x/${i}`,
    });
  }
  return out;
}

describe("DecisionsChartsPanel — live-mode 1Hz throttle", () => {
  beforeEach(() => {
    vi.useFakeTimers({ now: new Date("2026-05-10T12:00:00Z") });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does not recompute the displayed chart-data more than once per second under a 100/s prop-update burst", async () => {
    const initial = buildDecisions(10);
    const { rerender } = renderWithProviders(
      <DecisionsChartsPanel decisions={initial} />,
    );

    const distribution = () => screen.getByTestId("decisions-chart-distribution");
    expect(distribution().getAttribute("data-decision-count")).toBe("10");

    // Burst 100 prop updates within ~100ms (well inside the 1s throttle
    // window). The displayed chart-count MUST stay pinned to the initial
    // batch — no per-update churn.
    for (let i = 1; i <= 100; i += 1) {
      const next = buildDecisions(10 + i);
      rerender(<DecisionsChartsPanel decisions={next} />);
      // Advance 1ms per update so the burst lasts 100ms total.
      vi.advanceTimersByTime(1);
    }

    expect(distribution().getAttribute("data-decision-count")).toBe("10");

    // Cross the 1s boundary — the trailing throttle update should now
    // surface the latest prop-driven aggregate.
    act(() => {
      vi.advanceTimersByTime(1_000);
    });

    expect(distribution().getAttribute("data-decision-count")).toBe("110");
  });

  it("the throttled output ignores intermediate values and lands on the most recent", () => {
    const a = buildDecisions(5);
    const b = buildDecisions(50);
    const c = buildDecisions(200);

    const { rerender } = renderWithProviders(
      <DecisionsChartsPanel decisions={a} />,
    );
    expect(
      screen.getByTestId("decisions-chart-distribution").getAttribute("data-decision-count"),
    ).toBe("5");

    rerender(<DecisionsChartsPanel decisions={b} />);
    vi.advanceTimersByTime(100);
    rerender(<DecisionsChartsPanel decisions={c} />);
    vi.advanceTimersByTime(100);

    // Still inside the 1Hz window → no update reflected.
    expect(
      screen.getByTestId("decisions-chart-distribution").getAttribute("data-decision-count"),
    ).toBe("5");

    act(() => {
      vi.advanceTimersByTime(1_000);
    });

    // Trailing update lands on `c` (the latest), not on the intermediate
    // `b` value — confirming the throttle picks the most recent.
    expect(
      screen.getByTestId("decisions-chart-distribution").getAttribute("data-decision-count"),
    ).toBe("200");
  });
});
