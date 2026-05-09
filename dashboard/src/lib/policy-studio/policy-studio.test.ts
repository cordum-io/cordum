import { describe, it, expect } from "vitest";
import { RuleType } from "@/api/generated/model/ruleType";
import { DecisionType } from "@/api/generated/model/decisionType";
import { EdgeMode } from "@/api/generated/model/edgeMode";
import { ruleTypeLabel, ruleTypeIcon } from "./rule-type";
import { decisionTypeLabel } from "./decision-type";
import { decisionTone } from "./decision-tone";
import { edgeModeLabel } from "./edge-mode";

describe("policy-studio type adapters", () => {
  it("ruleTypeLabel covers every RuleType variant", () => {
    for (const value of Object.values(RuleType)) {
      const label = ruleTypeLabel(value);
      expect(label).toBeTruthy();
      expect(typeof label).toBe("string");
    }
    expect(ruleTypeLabel(RuleType.input)).toBe("Input");
    expect(ruleTypeLabel(RuleType.edge)).toBe("Edge");
  });

  it("ruleTypeIcon returns a LucideIcon component for every variant", () => {
    for (const value of Object.values(RuleType)) {
      const Icon = ruleTypeIcon(value);
      expect(Icon).toBeTruthy();
      expect(typeof Icon).toBe("object");
    }
  });

  it("decisionTypeLabel covers every DecisionType variant", () => {
    for (const value of Object.values(DecisionType)) {
      const label = decisionTypeLabel(value);
      expect(label).toBeTruthy();
      expect(typeof label).toBe("string");
    }
    expect(decisionTypeLabel(DecisionType.allow)).toBe("Allow");
    expect(decisionTypeLabel(DecisionType.require_human)).toBe("Require human");
    expect(decisionTypeLabel(DecisionType.allow_with_constraints)).toBe(
      "Allow with constraints",
    );
  });

  it("decisionTone maps every DecisionType to a known tone", () => {
    const validTones = new Set([
      "success",
      "warning",
      "danger",
      "info",
      "neutral",
    ]);
    for (const value of Object.values(DecisionType)) {
      const tone = decisionTone(value);
      expect(validTones.has(tone)).toBe(true);
    }
    expect(decisionTone(DecisionType.allow)).toBe("success");
    expect(decisionTone(DecisionType.deny)).toBe("danger");
    expect(decisionTone(DecisionType.require_human)).toBe("warning");
  });

  it("edgeModeLabel covers every EdgeMode variant", () => {
    for (const value of Object.values(EdgeMode)) {
      const label = edgeModeLabel(value);
      expect(label).toBeTruthy();
      expect(typeof label).toBe("string");
    }
    expect(edgeModeLabel(EdgeMode.observe)).toBe("Observe");
    expect(edgeModeLabel(EdgeMode["enterprise-strict"])).toBe("Enterprise strict");
  });
});
