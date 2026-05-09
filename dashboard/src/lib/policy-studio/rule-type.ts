import { Cpu, FileCheck, FileSearch, Zap, type LucideIcon } from "lucide-react";
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

export function ruleTypeLabel(t: RuleType): string {
  return RULE_TYPE_LABELS[t];
}

export function ruleTypeIcon(t: RuleType): LucideIcon {
  return RULE_TYPE_ICONS[t];
}
