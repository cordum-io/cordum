import { describe, expect, it } from "vitest";
import { getSafetyBadge, isJobType } from "./nodeRegistry";
import type { SafetyDecisionType } from "@/api/types";

// All five canonical SafetyDecisionType values must be covered by getSafetyBadge
// so the WorkflowStudio + RunDetailPage governance overlay never silently drops
// a decision tier. Driven by canonical type union from api/types.ts.
const ALL_SAFETY_DECISIONS: readonly SafetyDecisionType[] = [
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
];

describe("getSafetyBadge", () => {
  it.each(ALL_SAFETY_DECISIONS)("returns a non-null badge config for %s", (decision) => {
    const badge = getSafetyBadge(decision);
    expect(badge).not.toBeNull();
    expect(badge?.label).toBeTruthy();
    expect(badge?.className).toMatch(/^bg-\[var\(--color-/);
    expect(badge?.glyph).toBeTruthy();
  });

  it("maps allow → success token + check glyph", () => {
    const badge = getSafetyBadge("allow");
    expect(badge?.label).toBe("Allowed");
    expect(badge?.className).toContain("var(--color-success)");
    expect(badge?.glyph).toBe("✓");
  });

  it("maps deny → governance token + cross glyph", () => {
    const badge = getSafetyBadge("deny");
    expect(badge?.label).toBe("Denied");
    expect(badge?.className).toContain("var(--color-governance)");
    expect(badge?.glyph).toBe("✗");
  });

  it("maps require_approval → warning token + raised-hand glyph", () => {
    const badge = getSafetyBadge("require_approval");
    expect(badge?.label).toBe("Approval required");
    expect(badge?.className).toContain("var(--color-warning)");
    expect(badge?.glyph).toBe("✋");
  });

  it("maps allow_with_constraints → warning token (regression: previously absent from SAFETY_BADGES)", () => {
    const badge = getSafetyBadge("allow_with_constraints");
    expect(badge).not.toBeNull();
    expect(badge?.label).toBe("Allowed with constraints");
    expect(badge?.className).toContain("var(--color-warning)");
    expect(badge?.glyph).toBe("⚠");
  });

  it("maps throttle → info token + hourglass glyph", () => {
    const badge = getSafetyBadge("throttle");
    expect(badge?.label).toBe("Throttled");
    expect(badge?.className).toContain("var(--color-info)");
    expect(badge?.glyph).toBe("⏳");
  });

  it("returns null for undefined input", () => {
    expect(getSafetyBadge(undefined)).toBeNull();
  });

  it("returns null for an unknown decision string (fail-closed for future tiers)", () => {
    expect(getSafetyBadge("escalate")).toBeNull();
    expect(getSafetyBadge("quarantine")).toBeNull();
  });
});

describe("isJobType", () => {
  it.each(["job", "agent-task", "pack-action", "tool-call"])("returns true for %s", (stepType) => {
    expect(isJobType(stepType)).toBe(true);
  });

  it("returns false for non-job step types", () => {
    expect(isJobType("condition")).toBe(false);
    expect(isJobType("delay")).toBe(false);
    expect(isJobType("for-each")).toBe(false);
    expect(isJobType("")).toBe(false);
  });
});
