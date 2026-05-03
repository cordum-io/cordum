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

export type GlobalPolicyDefaultDecision = "allow" | "deny";
export type GlobalPolicyInputDecision =
  | "allow"
  | "deny"
  | "require_approval"
  | "allow_with_constraints"
  | "throttle";
export type GlobalPolicyOutputDecision = OutputRuleDecision;
export type GlobalPolicyOutputSeverity = OutputRuleSeverity;
export type GlobalPolicyOutputFailMode = "open" | "closed";

export interface GlobalPolicyMcpMatch {
  allowServers: string[];
  denyServers: string[];
  allowTools: string[];
  denyTools: string[];
  allowResources: string[];
  denyResources: string[];
  allowActions: string[];
  denyActions: string[];
}

export interface GlobalPolicyInputMatch {
  tenants: string[];
  topics: string[];
  capabilities: string[];
  riskTags: string[];
  requires: string[];
  packIds: string[];
  actorIds: string[];
  actorTypes: string[];
  labels: Record<string, string>;
  secretsPresent: boolean | null;
  mcp: GlobalPolicyMcpMatch;
}

export interface GlobalPolicyBudgetConstraints {
  maxRuntimeMs?: number;
  maxRetries?: number;
  maxArtifactBytes?: number;
  maxConcurrentJobs?: number;
}

export interface GlobalPolicySandboxConstraints {
  isolated?: boolean;
  networkAllowlist: string[];
  fsReadOnly: string[];
  fsReadWrite: string[];
}

export interface GlobalPolicyToolchainConstraints {
  allowedTools: string[];
  allowedCommands: string[];
}

export interface GlobalPolicyDiffConstraints {
  maxFiles?: number;
  maxLines?: number;
  denyPathGlobs: string[];
}

export interface GlobalPolicyConstraints {
  budgets: GlobalPolicyBudgetConstraints;
  sandbox: GlobalPolicySandboxConstraints;
  toolchain: GlobalPolicyToolchainConstraints;
  diff: GlobalPolicyDiffConstraints;
  redactionLevel?: string;
}

export interface GlobalPolicyRemediation {
  id: string;
  title: string;
  summary: string;
  replacementTopic: string;
  replacementCapability: string;
  addLabels: Record<string, string>;
  removeLabels: string[];
  source?: Record<string, unknown>;
}

export interface GlobalPolicyInputRule {
  id: string;
  decision: GlobalPolicyInputDecision;
  reason: string;
  match: GlobalPolicyInputMatch;
  constraints: GlobalPolicyConstraints;
  remediations: GlobalPolicyRemediation[];
  source?: Record<string, unknown>;
}

export interface GlobalPolicyOutputMatch {
  tenants: string[];
  topics: string[];
  capabilities: string[];
  riskTags: string[];
  scanners: string[];
  contentPatterns: string[];
  keywords: string[];
  contentTypes: string[];
  detectors: string[];
  outputSizeGt?: number;
  maxOutputBytes?: number;
  hasError: boolean | null;
}

export interface GlobalPolicyOutputRule {
  id: string;
  enabled: boolean;
  severity: GlobalPolicyOutputSeverity;
  description: string;
  decision: GlobalPolicyOutputDecision;
  reason: string;
  match: GlobalPolicyOutputMatch;
  source?: Record<string, unknown>;
}

export interface GlobalPolicyOutputPolicy {
  enabled: boolean;
  failMode: GlobalPolicyOutputFailMode;
  source?: Record<string, unknown>;
}

export interface GlobalPolicyDocument {
  defaultDecision: GlobalPolicyDefaultDecision;
  rules: GlobalPolicyInputRule[];
  outputPolicy: GlobalPolicyOutputPolicy;
  outputRules: GlobalPolicyOutputRule[];
  /**
   * EDGE-052 — Edge action rules (topic=job.edge.action). Optional with
   * empty-array default; legacy 2-section YAML omits this key and must
   * round-trip without errors.
   */
  edgeActionRules?: GlobalPolicyInputRule[];
  /**
   * EDGE-052 — MCP tool gate rules. Optional with empty-array default.
   * Match.MCP fields apply (allow/deny servers/tools/resources/actions).
   */
  mcpToolRules?: GlobalPolicyInputRule[];
  /**
   * EDGE-052 — Invariants (security floor). Authored by SecOps and
   * enforced uniformly across input/edge/mcp/output evaluators with
   * DENY-uncrossable precedence (see core/controlplane/safetykernel
   * applyKernelInvariants + policybundles ApplyInvariants).
   */
  invariants?: GlobalPolicyInputRule[];
  sourceRoot: Record<string, unknown>;
}

export interface GlobalPolicyParseIssue {
  path: string;
  message: string;
  severity: "error" | "warning";
  line?: number;
  column?: number;
}
