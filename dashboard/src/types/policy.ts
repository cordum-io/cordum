export type OutputRuleDecision = "allow" | "deny" | "quarantine" | "redact";
export type OutputRuleSeverity = "critical" | "high" | "medium" | "low";

export interface OutputRule {
  id: string;
  description?: string;
  topics: string[];
  scanners: string[];
  patterns: string[];
  patternPreview?: string;
  decision: OutputRuleDecision | string;
  severity: OutputRuleSeverity | string;
  enabled: boolean;
  reason?: string;
  match?: Record<string, unknown>;
  source?: Record<string, unknown>;
  lastTriggered?: string;
  triggerCount24h?: number;
}

export interface OutputRuleFinding {
  type: string;
  severity: string;
  detail: string;
  scanner?: string;
  confidence?: number;
  matchedPattern?: string;
}

export interface OutputRuleAuditEntry {
  id: string;
  jobId: string;
  ruleId: string;
  timestamp: string;
  decision?: string;
  reason?: string;
  phase?: string;
  findings: OutputRuleFinding[];
  originalPtr?: string;
  redactedPtr?: string;
}
