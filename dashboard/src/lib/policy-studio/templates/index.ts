import { RuleType } from "@/api/generated/model/ruleType";
import piiRedactYaml from "./pii-redact.yaml?raw";
import secretScanYaml from "./secret-scan.yaml?raw";
import rateLimitYaml from "./rate-limit.yaml?raw";
import approvalGateYaml from "./approval-gate.yaml?raw";
import edgeToolAllowlistYaml from "./edge-tool-allowlist.yaml?raw";
import edgeFileAccessYaml from "./edge-file-access.yaml?raw";
import edgePromptClassifierYaml from "./edge-prompt-classifier.yaml?raw";

export interface RuleTemplate {
  id: string;
  label: string;
  description: string;
  ruleType: RuleType;
  yaml: string;
}

// Plain YAML files double as documentation (task rail). Order is curated:
// most-common job-side patterns first, then the edge-side guardrails.
export const RULE_TEMPLATES: ReadonlyArray<RuleTemplate> = [
  {
    id: "pii-redact",
    label: "PII redact",
    description: "Redact PII (emails, phone, SSN) detected in agent input.",
    ruleType: RuleType.input,
    yaml: piiRedactYaml,
  },
  {
    id: "secret-scan",
    label: "Secret scan",
    description: "Hard-deny input carrying API keys, tokens, or private keys.",
    ruleType: RuleType.input,
    yaml: secretScanYaml,
  },
  {
    id: "rate-limit",
    label: "Rate limit",
    description: "Throttle a single agent identity to a sustainable request rate.",
    ruleType: RuleType.velocity,
    yaml: rateLimitYaml,
  },
  {
    id: "approval-gate",
    label: "Approval gate",
    description: "Require human approval before a sensitive action proceeds.",
    ruleType: RuleType.input,
    yaml: approvalGateYaml,
  },
  {
    id: "edge-tool-allowlist",
    label: "Edge tool allowlist",
    description: "Deny edge tool invocations not in an explicit allowlist.",
    ruleType: RuleType.edge,
    yaml: edgeToolAllowlistYaml,
  },
  {
    id: "edge-file-access",
    label: "Edge file access guard",
    description: "Block file reads/writes outside the agent's workspace.",
    ruleType: RuleType.edge,
    yaml: edgeFileAccessYaml,
  },
  {
    id: "edge-prompt-classifier",
    label: "Edge prompt classifier",
    description: "Deny prompts classified as injection, jailbreak, or exfiltration.",
    ruleType: RuleType.edge,
    yaml: edgePromptClassifierYaml,
  },
];
