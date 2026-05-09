import { Cpu, FileCheck, FileSearch, Shield, Zap, type LucideIcon } from "lucide-react";
import { RuleType } from "@/api/generated/model/ruleType";

const RULE_TYPE_LABELS: Record<RuleType, string> = {
  [RuleType.input]: "Input",
  [RuleType.output]: "Output",
  [RuleType.velocity]: "Velocity",
  [RuleType.edge]: "Edge",
};

const RULE_TYPE_ICONS: Record<RuleType, LucideIcon> = {
  [RuleType.input]: FileSearch,
  [RuleType.output]: FileCheck,
  [RuleType.velocity]: Zap,
  [RuleType.edge]: Cpu,
};

// `Shield` is the safe fallback for legacy/unknown rule types so neither
// `ruleTypeLabel` nor `ruleTypeIcon` ever returns undefined — preserves the
// task-fd25f310 comments-beeedc8e/-58bb8361 safeguards (no rendering of
// undefined icons for unknown/missing rule types).
export function ruleTypeLabel(t: RuleType | string | undefined | null): string {
  return (t && RULE_TYPE_LABELS[t as RuleType]) || "Unknown";
}

export function ruleTypeIcon(t: RuleType | string | undefined | null): LucideIcon {
  return (t && RULE_TYPE_ICONS[t as RuleType]) || Shield;
}
