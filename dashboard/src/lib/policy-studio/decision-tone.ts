import { DecisionType } from "@/api/generated/model/decisionType";

export type DecisionTone = "success" | "warning" | "danger" | "info" | "neutral";

const DECISION_TONES: Record<DecisionType, DecisionTone> = {
  [DecisionType.allow]: "success",
  [DecisionType.allow_with_constraints]: "info",
  [DecisionType.deny]: "danger",
  [DecisionType.require_human]: "warning",
  [DecisionType.throttle]: "warning",
  [DecisionType.quarantine]: "warning",
  [DecisionType.redact]: "info",
};

export function decisionTone(t: DecisionType): DecisionTone {
  return DECISION_TONES[t];
}
