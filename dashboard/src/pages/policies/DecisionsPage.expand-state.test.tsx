import { describe, expect, it } from "vitest";

import { decisionRowKey } from "./DecisionsPage";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";

function makeDecision(overrides: Partial<Decision> = {}): Decision {
  return {
    source: DecisionSource.job,
    rule_id: "rule-x",
    bundle_id: "bundle-x",
    bundle_version: "v1",
    type: DecisionType.allow,
    timestamp: "2026-05-10T12:00:00.000Z",
    audit_hash: "sha256:abcd",
    ...overrides,
  };
}

// Bug 3 (MED) lock-in. Expand-row state in DecisionsPage is keyed on
// `decisionRowKey(d, index)`. Live mode is newest-first ring buffer;
// paginated history is oldest-first cursor. The same Decision sits at
// different positions across modes, so including `index` in the key
// silently collapses the expanded-row when the user toggles live↔history.
// Fix: drop `index` from the key. This test asserts position-independence
// of the key for the same Decision identity.
describe("decisionRowKey (D8b fast-follow — Bug 3 lock-in)", () => {
  it("returns the same key for the same Decision regardless of position index", () => {
    const d = makeDecision();
    const keyAtFirstSlot = decisionRowKey(d, 0);
    const keyAtMidSlot = decisionRowKey(d, 5);
    const keyAtLastSlot = decisionRowKey(d, 199);
    expect(keyAtFirstSlot).toEqual(keyAtMidSlot);
    expect(keyAtMidSlot).toEqual(keyAtLastSlot);
  });

  it("returns distinct keys for distinct Decision identities at the same position", () => {
    const decA = makeDecision({
      rule_id: "rule-a",
      audit_hash: "sha256:0001",
    });
    const decB = makeDecision({
      rule_id: "rule-b",
      audit_hash: "sha256:0002",
    });
    expect(decisionRowKey(decA, 0)).not.toEqual(decisionRowKey(decB, 0));
  });

  it("handles missing audit_hash without collision against a sibling missing-hash row", () => {
    // Two decisions that differ ONLY in timestamp + rule_id but both lack
    // audit_hash. The empty-hash slot must not collapse them to the same
    // key.
    const earlier = makeDecision({
      timestamp: "2026-05-10T11:00:00.000Z",
      rule_id: "rule-earlier",
      audit_hash: undefined,
    });
    const later = makeDecision({
      timestamp: "2026-05-10T12:00:00.000Z",
      rule_id: "rule-later",
      audit_hash: undefined,
    });
    expect(decisionRowKey(earlier, 0)).not.toEqual(decisionRowKey(later, 0));
  });
});
