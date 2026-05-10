import { describe, expect, it } from "vitest";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import { fixturePolicyDecisions } from "./decisions";

// The fixture file imports `Decision` from the generated client and types
// the exported array as `Decision[]`. If the schema drifts, tsc (and these
// tests via it) fail to compile — the type contract is verified statically.
// These runtime checks pin coverage and stability so D8/D9 can rely on the
// fixture having every bucket populated.
describe("fixturePolicyDecisions", () => {
  it("covers every DecisionType bucket", () => {
    const seen = new Set(fixturePolicyDecisions.map((d) => d.type));
    expect(seen).toContain(DecisionType.allow);
    expect(seen).toContain(DecisionType.deny);
    expect(seen).toContain(DecisionType.require_human);
    expect(seen).toContain(DecisionType.throttle);
    expect(seen).toContain(DecisionType.allow_with_constraints);
    expect(seen).toContain(DecisionType.quarantine);
    expect(seen).toContain(DecisionType.redact);
  });

  it("covers both DecisionSource values", () => {
    const seen = new Set(fixturePolicyDecisions.map((d) => d.source));
    expect(seen).toContain(DecisionSource.job);
    expect(seen).toContain(DecisionSource.edge);
  });

  it("populates trace[] with at least one TraceStep on every row", () => {
    for (const d of fixturePolicyDecisions) {
      expect(d.trace?.length).toBeGreaterThan(0);
      expect(d.trace?.[0].decision_type).toBeTruthy();
    }
  });

  it("populates audit_hash on every row", () => {
    for (const d of fixturePolicyDecisions) {
      expect(d.audit_hash).toMatch(/^sha256:/);
    }
  });

  it("populates bundle_id + bundle_version on most rows (>=80%)", () => {
    const withBundle = fixturePolicyDecisions.filter(
      (d) => d.bundle_id && d.bundle_version,
    );
    expect(withBundle.length).toBeGreaterThanOrEqual(
      Math.ceil(0.8 * fixturePolicyDecisions.length),
    );
  });
});
