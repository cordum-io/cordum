import { describe, it, expect } from "vitest";
import { fireEvent, screen } from "@testing-library/dom";
import { renderWithProviders } from "@/test-utils/render";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";
import { DecisionsChartsPanel } from "./DecisionsChartsPanel";

function decision(overrides: Partial<Decision> = {}): Decision {
  return {
    source: DecisionSource.job,
    rule_id: "rule.input.secret-scan",
    bundle_id: "bundle.acme",
    bundle_version: "v1",
    type: DecisionType.deny,
    timestamp: "2026-05-10T12:00:00Z",
    audit_hash: "sha256:row",
    input_ref: "blob://x",
    ...overrides,
  };
}

const sample: Decision[] = [
  decision({ rule_id: "rule.A", type: DecisionType.allow, source: DecisionSource.job, timestamp: "2026-05-10T12:00:00Z" }),
  decision({ rule_id: "rule.A", type: DecisionType.allow, source: DecisionSource.job, timestamp: "2026-05-10T12:01:00Z" }),
  decision({ rule_id: "rule.A", type: DecisionType.deny, source: DecisionSource.edge, timestamp: "2026-05-10T12:02:00Z" }),
  decision({ rule_id: "rule.B", type: DecisionType.deny, source: DecisionSource.job, timestamp: "2026-05-10T12:03:00Z" }),
  decision({ rule_id: "rule.B", type: DecisionType.throttle, source: DecisionSource.edge, timestamp: "2026-05-10T12:04:00Z" }),
  decision({ rule_id: "rule.C", type: DecisionType.require_human, source: DecisionSource.job, timestamp: "2026-05-10T12:05:00Z" }),
];

describe("DecisionsChartsPanel (D9b — 4 Recharts charts above the DataTable)", () => {
  it("renders four chart regions when given a non-empty decisions array", () => {
    renderWithProviders(<DecisionsChartsPanel decisions={sample} />);

    expect(screen.getByTestId("decisions-chart-distribution")).not.toBeNull();
    expect(screen.getByTestId("decisions-chart-top-rules")).not.toBeNull();
    expect(screen.getByTestId("decisions-chart-per-min")).not.toBeNull();
    expect(screen.getByTestId("decisions-chart-by-scope")).not.toBeNull();
  });

  it("each chart carries an aria-label so screen readers can announce the metric", () => {
    renderWithProviders(<DecisionsChartsPanel decisions={sample} />);

    expect(
      screen.getByTestId("decisions-chart-distribution").getAttribute("aria-label"),
    ).toMatch(/distribution/i);
    expect(
      screen.getByTestId("decisions-chart-top-rules").getAttribute("aria-label"),
    ).toMatch(/top.*rule/i);
    expect(
      screen.getByTestId("decisions-chart-per-min").getAttribute("aria-label"),
    ).toMatch(/per.?min|decisions\/min/i);
    expect(
      screen.getByTestId("decisions-chart-by-scope").getAttribute("aria-label"),
    ).toMatch(/scope|source/i);
  });

  it("renders empty placeholders (not crash) when decisions is empty", () => {
    renderWithProviders(<DecisionsChartsPanel decisions={[]} />);

    const empty = screen.getAllByTestId("decisions-chart-empty");
    // One placeholder per chart, four charts total.
    expect(empty.length).toBe(4);
  });

  it("Top firing rules surfaces a clickable link to the rule editor (cross-link contract)", () => {
    renderWithProviders(<DecisionsChartsPanel decisions={sample} />);

    const topRules = screen.getByTestId("decisions-chart-top-rules");
    // The chart wraps each row's label/bar in an <a> so a click navigates
    // to /policies?rule=<id>&open=editor (spec § cross-links #2). Locate
    // by the canonical rule id from the fixture.
    const link = topRules.querySelector<HTMLAnchorElement>(
      "a[data-row-action=cross-link-decisions-rule]",
    );
    expect(link).not.toBeNull();
    expect(link!.getAttribute("href")).toMatch(/\/policies\?rule=/);
    expect(link!.getAttribute("href")).toContain("open=editor");
    fireEvent.click(link!);
  });

  it("aggregates decision counts by type for the distribution chart", () => {
    renderWithProviders(<DecisionsChartsPanel decisions={sample} />);

    // Source-of-truth signature exposed for QA: total count of decisions
    // routed into the distribution chart. Allows the test to confirm
    // arithmetic without poking Recharts internals.
    const distribution = screen.getByTestId("decisions-chart-distribution");
    expect(distribution.getAttribute("data-decision-count")).toBe(
      String(sample.length),
    );
  });
});
