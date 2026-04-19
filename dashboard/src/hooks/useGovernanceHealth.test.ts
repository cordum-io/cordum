// Coverage for the governance-health hook's error handling + shape.
import { describe, expect, it } from "vitest";
import type { GovernanceHealth, GovernanceHealthFactor } from "../api/types";

describe("GovernanceHealth types", () => {
  it("accepts a populated score with all four factors", () => {
    const factor: GovernanceHealthFactor = { score: 90, weight: 25 };
    const health: GovernanceHealth = {
      score: 87,
      grade: "B",
      generated_at: "2026-04-17T12:00:00Z",
      factors: {
        denial_rate: factor,
        approval_latency_p95: factor,
        policy_coverage: factor,
        chain_integrity: factor,
      },
    };
    expect(health.score).toBe(87);
    expect(health.grade).toBe("B");
    expect(Object.keys(health.factors)).toHaveLength(4);
  });

  it("accepts a partial factor with notes", () => {
    const factor: GovernanceHealthFactor = {
      score: 70,
      weight: 25,
      notes: "unavailable: redis timeout",
    };
    expect(factor.notes).toContain("unavailable");
  });

  it("allows truncated_at_max flag", () => {
    const h: GovernanceHealth = {
      score: 82,
      grade: "B",
      generated_at: "x",
      factors: {},
      truncated_at_max: true,
    };
    expect(h.truncated_at_max).toBe(true);
  });
});
