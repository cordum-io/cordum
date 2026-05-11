import { DecisionType } from "@/api/generated/model/decisionType";

const DECISION_TYPE_LABELS: Record<DecisionType, string> = {
  [DecisionType.allow]: "Allow",
  [DecisionType.deny]: "Deny",
  [DecisionType.require_human]: "Require human",
  [DecisionType.throttle]: "Throttle",
  [DecisionType.allow_with_constraints]: "Allow with constraints",
  [DecisionType.quarantine]: "Quarantine",
  [DecisionType.redact]: "Redact",
};

export function decisionTypeLabel(t: DecisionType): string {
  return DECISION_TYPE_LABELS[t];
}
