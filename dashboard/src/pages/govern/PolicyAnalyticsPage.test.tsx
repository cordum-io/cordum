import { describe, expect, it } from "vitest";
import type {
  PolicyAnalyticsResponse,
  RuleAnalytics,
} from "@/api/types";
import {
  validateAnalyticsRange,
  OVERRIDE_WARNING_THRESHOLD,
  MAX_ANALYTICS_RANGE_DAYS,
  ANALYTICS_PAGE_SECTIONS,
} from "./PolicyAnalyticsPage";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeRule(overrides?: Partial<RuleAnalytics>): RuleAnalytics {
  return {
    rule_id: "rule-default",
    hit_count: 10,
    approval_count: 5,
    override_count: 2,
    override_rate: 0.4,
    avg_approval_latency_ms: 30000,
    daily_hits: [1, 2, 1, 2, 1, 2, 1],
    ...overrides,
  };
}

function makeResponse(overrides?: Partial<PolicyAnalyticsResponse>): PolicyAnalyticsResponse {
  return {
    time_range: { from: "2026-04-07T00:00:00Z", to: "2026-04-14T00:00:00Z" },
    rules: [makeRule()],
    summary: {
      total_rules: 1,
      total_hits: 10,
      total_overrides: 2,
      highest_override_rule_id: "rule-default",
    },
    ...overrides,
  };
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

describe("PolicyAnalyticsPage constants", () => {
  it("exports expected section identifiers", () => {
    expect(ANALYTICS_PAGE_SECTIONS).toContain("controls");
    expect(ANALYTICS_PAGE_SECTIONS).toContain("rule-table");
    expect(ANALYTICS_PAGE_SECTIONS).toContain("fatigue-chart");
    expect(ANALYTICS_PAGE_SECTIONS).toContain("fp-highlights");
  });

  it("has sensible thresholds", () => {
    expect(OVERRIDE_WARNING_THRESHOLD).toBe(0.5);
    expect(MAX_ANALYTICS_RANGE_DAYS).toBe(7);
  });
});

// ---------------------------------------------------------------------------
// validateAnalyticsRange
// ---------------------------------------------------------------------------

describe("validateAnalyticsRange", () => {
  it("returns null for valid 7-day range", () => {
    expect(validateAnalyticsRange("2026-04-07T00:00", "2026-04-14T00:00")).toBeNull();
  });

  it("returns null for valid 1-day range", () => {
    expect(validateAnalyticsRange("2026-04-13T00:00", "2026-04-14T00:00")).toBeNull();
  });

  it("returns error when from is empty", () => {
    expect(validateAnalyticsRange("", "2026-04-14T00:00")).toBeTruthy();
  });

  it("returns error when from >= to", () => {
    expect(validateAnalyticsRange("2026-04-14T00:00", "2026-04-14T00:00")).toBeTruthy();
    expect(validateAnalyticsRange("2026-04-15T00:00", "2026-04-14T00:00")).toBeTruthy();
  });

  it("returns error when range exceeds 7 days", () => {
    const err = validateAnalyticsRange("2026-04-01T00:00", "2026-04-09T00:00");
    expect(err).toBeTruthy();
    expect(err).toContain("7");
  });

  it("returns error for invalid dates", () => {
    expect(validateAnalyticsRange("bad", "2026-04-14T00:00")).toBeTruthy();
  });
});

// ---------------------------------------------------------------------------
// Response shape
// ---------------------------------------------------------------------------

describe("PolicyAnalyticsResponse shape", () => {
  it("produces valid defaults", () => {
    const resp = makeResponse();
    expect(resp.rules).toHaveLength(1);
    expect(resp.summary.total_rules).toBe(1);
    expect(resp.summary.total_hits).toBe(10);
  });

  it("empty response", () => {
    const resp = makeResponse({
      rules: [],
      summary: { total_rules: 0, total_hits: 0, total_overrides: 0, highest_override_rule_id: "" },
    });
    expect(resp.rules).toHaveLength(0);
    expect(resp.summary.total_overrides).toBe(0);
  });

  it("identifies high override rules", () => {
    const highOverride = makeRule({ rule_id: "rule-aggressive", override_rate: 0.75 });
    const lowOverride = makeRule({ rule_id: "rule-good", override_rate: 0.1 });

    expect(highOverride.override_rate).toBeGreaterThanOrEqual(OVERRIDE_WARNING_THRESHOLD);
    expect(lowOverride.override_rate).toBeLessThan(OVERRIDE_WARNING_THRESHOLD);
  });

  it("daily_hits has 7 elements for a 7-day range", () => {
    const rule = makeRule();
    expect(rule.daily_hits).toHaveLength(7);
    const sum = rule.daily_hits.reduce((a, b) => a + b, 0);
    expect(sum).toBe(10);
  });

  it("override rate is consistent with counts", () => {
    const rule = makeRule({ approval_count: 10, override_count: 3, override_rate: 0.3 });
    expect(rule.override_rate).toBeCloseTo(rule.override_count / rule.approval_count, 1);
  });

  it("highest override rule matches the rule with highest rate", () => {
    const rules = [
      makeRule({ rule_id: "rule-a", override_rate: 0.2 }),
      makeRule({ rule_id: "rule-b", override_rate: 0.8 }),
      makeRule({ rule_id: "rule-c", override_rate: 0.5 }),
    ];
    const highest = rules.reduce((max, r) => r.override_rate > max.override_rate ? r : max);
    expect(highest.rule_id).toBe("rule-b");
  });

  it("latency is positive for rules with overrides", () => {
    const rule = makeRule({ override_count: 5, avg_approval_latency_ms: 45000 });
    expect(rule.avg_approval_latency_ms).toBeGreaterThan(0);
  });
});
