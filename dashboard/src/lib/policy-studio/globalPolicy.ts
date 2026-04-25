import { parsePolicyYaml, stringifyPolicyYaml } from "@/lib/policy-yaml";
import type {
  GlobalPolicyConstraints,
  GlobalPolicyDefaultDecision,
  GlobalPolicyDocument,
  GlobalPolicyInputDecision,
  GlobalPolicyInputMatch,
  GlobalPolicyInputRule,
  GlobalPolicyOutputFailMode,
  GlobalPolicyOutputMatch,
  GlobalPolicyOutputPolicy,
  GlobalPolicyOutputRule,
  GlobalPolicyOutputDecision,
  GlobalPolicyOutputSeverity,
  GlobalPolicyParseIssue,
  GlobalPolicyRemediation,
} from "@/types/policy";

export interface ParseGlobalPolicyResult {
  policy: GlobalPolicyDocument;
  issues: GlobalPolicyParseIssue[];
  valid: boolean;
}

const INPUT_DECISIONS = new Set<GlobalPolicyInputDecision>([
  "allow",
  "deny",
  "require_approval",
  "allow_with_constraints",
  "throttle",
]);

const OUTPUT_DECISIONS = new Set<GlobalPolicyOutputDecision>([
  "allow",
  "deny",
  "quarantine",
  "redact",
]);

const OUTPUT_SEVERITIES = new Set<GlobalPolicyOutputSeverity>([
  "low",
  "medium",
  "high",
  "critical",
]);

const OUTPUT_FAIL_MODES = new Set<GlobalPolicyOutputFailMode>(["open", "closed"]);
const DEFAULT_OUTPUT_POLICY: GlobalPolicyOutputPolicy = {
  enabled: false,
  failMode: "closed",
};
const GLOBAL_RULE_ID_SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export function isValidGlobalRuleIdSlug(value: string): boolean {
  return GLOBAL_RULE_ID_SLUG_PATTERN.test(value.trim());
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function cloneRecord(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) return {};
  if (typeof structuredClone === "function") {
    return structuredClone(value);
  }
  return JSON.parse(JSON.stringify(value)) as Record<string, unknown>;
}

function pushIssue(
  issues: GlobalPolicyParseIssue[],
  issue: Omit<GlobalPolicyParseIssue, "severity"> & { severity?: "error" | "warning" },
) {
  issues.push({
    severity: issue.severity ?? "warning",
    path: issue.path,
    message: issue.message,
    line: issue.line,
    column: issue.column,
  });
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((entry) => (typeof entry === "string" ? entry.trim() : ""))
    .filter(Boolean);
}

function asStringMap(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {};
  const out: Record<string, string> = {};
  for (const [key, raw] of Object.entries(value)) {
    if (typeof raw === "string" && raw.trim()) {
      out[key] = raw.trim();
    }
  }
  return out;
}

function asBoolean(value: unknown): boolean | null {
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    const normalized = value.trim().toLowerCase();
    if (["1", "true", "yes", "on"].includes(normalized)) return true;
    if (["0", "false", "no", "off"].includes(normalized)) return false;
  }
  return null;
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return undefined;
}

function normalizeDefaultDecision(raw: unknown): GlobalPolicyDefaultDecision {
  return asString(raw).trim().toLowerCase() === "allow" ? "allow" : "deny";
}

function normalizeInputDecision(raw: unknown): GlobalPolicyInputDecision {
  const normalized = asString(raw).trim().toLowerCase();
  return INPUT_DECISIONS.has(normalized as GlobalPolicyInputDecision)
    ? (normalized as GlobalPolicyInputDecision)
    : "deny";
}

function normalizeOutputDecision(raw: unknown): GlobalPolicyOutputDecision {
  const normalized = asString(raw).trim().toLowerCase();
  return OUTPUT_DECISIONS.has(normalized as GlobalPolicyOutputDecision)
    ? (normalized as GlobalPolicyOutputDecision)
    : "deny";
}

function normalizeOutputSeverity(raw: unknown): GlobalPolicyOutputSeverity {
  const normalized = asString(raw).trim().toLowerCase();
  return OUTPUT_SEVERITIES.has(normalized as GlobalPolicyOutputSeverity)
    ? (normalized as GlobalPolicyOutputSeverity)
    : "medium";
}

function normalizeOutputFailMode(raw: unknown): GlobalPolicyOutputFailMode {
  const normalized = asString(raw).trim().toLowerCase();
  return OUTPUT_FAIL_MODES.has(normalized as GlobalPolicyOutputFailMode)
    ? (normalized as GlobalPolicyOutputFailMode)
    : "closed";
}

function parseInputMatch(raw: unknown): GlobalPolicyInputMatch {
  const source = isRecord(raw) ? raw : {};
  const mcpRaw = isRecord(source.mcp) ? source.mcp : {};
  return {
    tenants: asStringArray(source.tenants),
    topics: asStringArray(source.topics),
    capabilities: asStringArray(source.capabilities),
    riskTags: asStringArray(source.risk_tags),
    requires: asStringArray(source.requires),
    packIds: asStringArray(source.pack_ids),
    actorIds: asStringArray(source.actor_ids),
    actorTypes: asStringArray(source.actor_types),
    labels: asStringMap(source.labels),
    secretsPresent: asBoolean(source.secrets_present),
    mcp: {
      allowServers: asStringArray(mcpRaw.allow_servers),
      denyServers: asStringArray(mcpRaw.deny_servers),
      allowTools: asStringArray(mcpRaw.allow_tools),
      denyTools: asStringArray(mcpRaw.deny_tools),
      allowResources: asStringArray(mcpRaw.allow_resources),
      denyResources: asStringArray(mcpRaw.deny_resources),
      allowActions: asStringArray(mcpRaw.allow_actions),
      denyActions: asStringArray(mcpRaw.deny_actions),
    },
  };
}

function parseConstraints(raw: unknown): GlobalPolicyConstraints {
  const source = isRecord(raw) ? raw : {};
  const budgetsRaw = isRecord(source.budgets) ? source.budgets : {};
  const sandboxRaw = isRecord(source.sandbox) ? source.sandbox : {};
  const toolchainRaw = isRecord(source.toolchain) ? source.toolchain : {};
  const diffRaw = isRecord(source.diff) ? source.diff : {};

  return {
    budgets: {
      maxRuntimeMs: asNumber(budgetsRaw.max_runtime_ms),
      maxRetries: asNumber(budgetsRaw.max_retries),
      maxArtifactBytes: asNumber(budgetsRaw.max_artifact_bytes),
      maxConcurrentJobs: asNumber(budgetsRaw.max_concurrent_jobs),
    },
    sandbox: {
      isolated: asBoolean(sandboxRaw.isolated) ?? undefined,
      networkAllowlist: asStringArray(sandboxRaw.network_allowlist),
      fsReadOnly: asStringArray(sandboxRaw.fs_read_only),
      fsReadWrite: asStringArray(sandboxRaw.fs_read_write),
    },
    toolchain: {
      allowedTools: asStringArray(toolchainRaw.allowed_tools),
      allowedCommands: asStringArray(toolchainRaw.allowed_commands),
    },
    diff: {
      maxFiles: asNumber(diffRaw.max_files),
      maxLines: asNumber(diffRaw.max_lines),
      denyPathGlobs: asStringArray(diffRaw.deny_path_globs),
    },
    redactionLevel: asString(source.redaction_level).trim() || undefined,
  };
}

function parseRemediation(raw: unknown): GlobalPolicyRemediation | null {
  if (!isRecord(raw)) return null;
  return {
    id: asString(raw.id).trim(),
    title: asString(raw.title).trim(),
    summary: asString(raw.summary).trim(),
    replacementTopic: asString(raw.replacement_topic).trim(),
    replacementCapability: asString(raw.replacement_capability).trim(),
    addLabels: asStringMap(raw.add_labels),
    removeLabels: asStringArray(raw.remove_labels),
    source: cloneRecord(raw),
  };
}

function parseInputRule(raw: unknown, index: number): GlobalPolicyInputRule {
  const source = cloneRecord(raw);
  const remediationsRaw = Array.isArray(source.remediations)
    ? source.remediations
    : [];
  const remediations = remediationsRaw
    .map(parseRemediation)
    .filter((entry): entry is GlobalPolicyRemediation => entry !== null);

  const ruleId = asString(source.id).trim() || `rule-${index + 1}`;
  return {
    id: ruleId,
    decision: normalizeInputDecision(source.decision),
    reason: asString(source.reason).trim(),
    match: parseInputMatch(source.match),
    constraints: parseConstraints(source.constraints),
    remediations,
    source,
  };
}

function parseOutputMatch(raw: unknown): GlobalPolicyOutputMatch {
  const source = isRecord(raw) ? raw : {};
  return {
    tenants: asStringArray(source.tenants),
    topics: asStringArray(source.topics),
    capabilities: asStringArray(source.capabilities),
    riskTags: asStringArray(source.risk_tags),
    scanners: asStringArray(source.scanners),
    contentPatterns: asStringArray(source.content_patterns),
    keywords: asStringArray(source.keywords),
    contentTypes: asStringArray(source.content_types),
    detectors: asStringArray(source.detectors),
    outputSizeGt: asNumber(source.output_size_gt),
    maxOutputBytes: asNumber(source.max_output_bytes),
    hasError: asBoolean(source.has_error),
  };
}

function parseOutputRule(raw: unknown, index: number): GlobalPolicyOutputRule {
  const source = cloneRecord(raw);
  const ruleId = asString(source.id).trim() || `output-rule-${index + 1}`;
  return {
    id: ruleId,
    enabled: asBoolean(source.enabled) ?? true,
    severity: normalizeOutputSeverity(source.severity),
    description: asString(source.description).trim(),
    decision: normalizeOutputDecision(source.decision),
    reason: asString(source.reason).trim(),
    match: parseOutputMatch(source.match),
    source,
  };
}

function parseOutputPolicy(raw: unknown): GlobalPolicyOutputPolicy {
  const source = cloneRecord(raw);
  return {
    enabled: asBoolean(source.enabled) ?? DEFAULT_OUTPUT_POLICY.enabled,
    failMode: normalizeOutputFailMode(source.fail_mode),
    source,
  };
}

function setStringArray(
  target: Record<string, unknown>,
  key: string,
  value: string[],
) {
  if (value.length > 0) {
    target[key] = [...value];
  } else {
    delete target[key];
  }
}

function setNumber(target: Record<string, unknown>, key: string, value?: number) {
  if (typeof value === "number" && Number.isFinite(value)) {
    target[key] = value;
  } else {
    delete target[key];
  }
}

function setTrimmedString(
  target: Record<string, unknown>,
  key: string,
  value: string,
) {
  const trimmed = value.trim();
  if (trimmed) {
    target[key] = trimmed;
  } else {
    delete target[key];
  }
}

function serializeInputMatch(match: GlobalPolicyInputMatch, base: unknown): Record<string, unknown> {
  const out = cloneRecord(base);
  setStringArray(out, "tenants", match.tenants);
  setStringArray(out, "topics", match.topics);
  setStringArray(out, "capabilities", match.capabilities);
  setStringArray(out, "risk_tags", match.riskTags);
  setStringArray(out, "requires", match.requires);
  setStringArray(out, "pack_ids", match.packIds);
  setStringArray(out, "actor_ids", match.actorIds);
  setStringArray(out, "actor_types", match.actorTypes);

  if (Object.keys(match.labels).length > 0) {
    out.labels = { ...match.labels };
  } else {
    delete out.labels;
  }

  if (match.secretsPresent === null) {
    delete out.secrets_present;
  } else {
    out.secrets_present = match.secretsPresent;
  }

  const mcp = cloneRecord(out.mcp);
  setStringArray(mcp, "allow_servers", match.mcp.allowServers);
  setStringArray(mcp, "deny_servers", match.mcp.denyServers);
  setStringArray(mcp, "allow_tools", match.mcp.allowTools);
  setStringArray(mcp, "deny_tools", match.mcp.denyTools);
  setStringArray(mcp, "allow_resources", match.mcp.allowResources);
  setStringArray(mcp, "deny_resources", match.mcp.denyResources);
  setStringArray(mcp, "allow_actions", match.mcp.allowActions);
  setStringArray(mcp, "deny_actions", match.mcp.denyActions);
  if (Object.keys(mcp).length > 0) {
    out.mcp = mcp;
  } else {
    delete out.mcp;
  }
  return out;
}

function serializeConstraints(
  constraints: GlobalPolicyConstraints,
  base: unknown,
): Record<string, unknown> | undefined {
  const out = cloneRecord(base);
  const budgets = cloneRecord(out.budgets);
  const sandbox = cloneRecord(out.sandbox);
  const toolchain = cloneRecord(out.toolchain);
  const diff = cloneRecord(out.diff);

  setNumber(budgets, "max_runtime_ms", constraints.budgets.maxRuntimeMs);
  setNumber(budgets, "max_retries", constraints.budgets.maxRetries);
  setNumber(budgets, "max_artifact_bytes", constraints.budgets.maxArtifactBytes);
  setNumber(budgets, "max_concurrent_jobs", constraints.budgets.maxConcurrentJobs);
  if (Object.keys(budgets).length > 0) out.budgets = budgets;
  else delete out.budgets;

  if (typeof constraints.sandbox.isolated === "boolean") {
    sandbox.isolated = constraints.sandbox.isolated;
  } else {
    delete sandbox.isolated;
  }
  setStringArray(sandbox, "network_allowlist", constraints.sandbox.networkAllowlist);
  setStringArray(sandbox, "fs_read_only", constraints.sandbox.fsReadOnly);
  setStringArray(sandbox, "fs_read_write", constraints.sandbox.fsReadWrite);
  if (Object.keys(sandbox).length > 0) out.sandbox = sandbox;
  else delete out.sandbox;

  setStringArray(toolchain, "allowed_tools", constraints.toolchain.allowedTools);
  setStringArray(toolchain, "allowed_commands", constraints.toolchain.allowedCommands);
  if (Object.keys(toolchain).length > 0) out.toolchain = toolchain;
  else delete out.toolchain;

  setNumber(diff, "max_files", constraints.diff.maxFiles);
  setNumber(diff, "max_lines", constraints.diff.maxLines);
  setStringArray(diff, "deny_path_globs", constraints.diff.denyPathGlobs);
  if (Object.keys(diff).length > 0) out.diff = diff;
  else delete out.diff;

  if (constraints.redactionLevel?.trim()) {
    out.redaction_level = constraints.redactionLevel.trim();
  } else {
    delete out.redaction_level;
  }

  return Object.keys(out).length > 0 ? out : undefined;
}

function serializeRemediation(remediation: GlobalPolicyRemediation): Record<string, unknown> {
  const out = cloneRecord(remediation.source);
  setTrimmedString(out, "id", remediation.id);
  setTrimmedString(out, "title", remediation.title);
  setTrimmedString(out, "summary", remediation.summary);
  setTrimmedString(out, "replacement_topic", remediation.replacementTopic);
  setTrimmedString(out, "replacement_capability", remediation.replacementCapability);
  if (Object.keys(remediation.addLabels).length > 0) {
    out.add_labels = { ...remediation.addLabels };
  } else {
    delete out.add_labels;
  }
  setStringArray(out, "remove_labels", remediation.removeLabels);
  return out;
}

function serializeInputRule(rule: GlobalPolicyInputRule, index: number): Record<string, unknown> {
  const out = cloneRecord(rule.source);
  out.id = rule.id.trim() || `rule-${index + 1}`;
  out.decision = rule.decision;
  setTrimmedString(out, "reason", rule.reason);
  out.match = serializeInputMatch(rule.match, out.match);
  const constraints = serializeConstraints(rule.constraints, out.constraints);
  if (constraints) out.constraints = constraints;
  else delete out.constraints;
  if (rule.remediations.length > 0) {
    out.remediations = rule.remediations.map(serializeRemediation);
  } else {
    delete out.remediations;
  }
  return out;
}

function serializeOutputMatch(match: GlobalPolicyOutputMatch, base: unknown): Record<string, unknown> {
  const out = cloneRecord(base);
  setStringArray(out, "tenants", match.tenants);
  setStringArray(out, "topics", match.topics);
  setStringArray(out, "capabilities", match.capabilities);
  setStringArray(out, "risk_tags", match.riskTags);
  setStringArray(out, "scanners", match.scanners);
  setStringArray(out, "content_patterns", match.contentPatterns);
  setStringArray(out, "keywords", match.keywords);
  setStringArray(out, "content_types", match.contentTypes);
  setStringArray(out, "detectors", match.detectors);
  setNumber(out, "output_size_gt", match.outputSizeGt);
  setNumber(out, "max_output_bytes", match.maxOutputBytes);
  if (match.hasError === null) {
    delete out.has_error;
  } else {
    out.has_error = match.hasError;
  }
  return out;
}

function serializeOutputRule(rule: GlobalPolicyOutputRule, index: number): Record<string, unknown> {
  const out = cloneRecord(rule.source);
  out.id = rule.id.trim() || `output-rule-${index + 1}`;
  out.enabled = rule.enabled;
  out.severity = rule.severity;
  out.decision = rule.decision;
  setTrimmedString(out, "description", rule.description);
  setTrimmedString(out, "reason", rule.reason);
  out.match = serializeOutputMatch(rule.match, out.match);
  return out;
}

function serializeOutputPolicy(
  outputPolicy: GlobalPolicyOutputPolicy,
  base: unknown,
): Record<string, unknown> {
  const out = cloneRecord(base);
  out.enabled = outputPolicy.enabled;
  out.fail_mode = outputPolicy.failMode;
  return out;
}

export function createDefaultGlobalPolicyDocument(): GlobalPolicyDocument {
  return {
    defaultDecision: "deny",
    rules: [],
    outputPolicy: { ...DEFAULT_OUTPUT_POLICY },
    outputRules: [],
    sourceRoot: {},
  };
}

export function parseGlobalPolicyYaml(yaml: string): ParseGlobalPolicyResult {
  const parsed = parsePolicyYaml(yaml);
  const issues: GlobalPolicyParseIssue[] = [];

  if (!parsed.valid) {
    for (const err of parsed.errors) {
      pushIssue(issues, {
        severity: "error",
        path: "$",
        message: err.message,
        line: err.line,
        column: err.column,
      });
    }
    return {
      policy: createDefaultGlobalPolicyDocument(),
      issues,
      valid: false,
    };
  }

  if (!isRecord(parsed.parsed)) {
    pushIssue(issues, {
      path: "$",
      message: "Policy YAML root must be a mapping. Using secure defaults.",
      severity: "warning",
    });
  }

  const root = isRecord(parsed.parsed) ? cloneRecord(parsed.parsed) : {};
  const defaultDecision = normalizeDefaultDecision(root.default_decision);
  if (root.default_decision !== undefined && defaultDecision === "deny") {
    const normalized = asString(root.default_decision).trim().toLowerCase();
    if (normalized !== "deny") {
      pushIssue(issues, {
        path: "default_decision",
        message: "Invalid default_decision value. Falling back to deny.",
      });
    }
  }

  const rulesRaw = Array.isArray(root.rules) ? root.rules : [];
  if (root.rules !== undefined && !Array.isArray(root.rules)) {
    pushIssue(issues, {
      path: "rules",
      message: "Expected rules to be an array. Ignoring invalid value.",
    });
  }
  const rules = rulesRaw.map(parseInputRule);

  const outputPolicy = parseOutputPolicy(root.output_policy);
  const outputRulesRaw = Array.isArray(root.output_rules) ? root.output_rules : [];
  if (root.output_rules !== undefined && !Array.isArray(root.output_rules)) {
    pushIssue(issues, {
      path: "output_rules",
      message: "Expected output_rules to be an array. Ignoring invalid value.",
    });
  }
  const outputRules = outputRulesRaw.map(parseOutputRule);

  return {
    policy: {
      defaultDecision,
      rules,
      outputPolicy,
      outputRules,
      sourceRoot: root,
    },
    issues,
    valid: issues.every((issue) => issue.severity !== "error"),
  };
}

export function serializeGlobalPolicyYaml(policy: GlobalPolicyDocument): string {
  const root = cloneRecord(policy.sourceRoot);
  root.default_decision = policy.defaultDecision;
  root.rules = policy.rules.map(serializeInputRule);
  root.output_policy = serializeOutputPolicy(policy.outputPolicy, root.output_policy);
  root.output_rules = policy.outputRules.map(serializeOutputRule);
  return stringifyPolicyYaml(root);
}

export const __globalPolicyInternal = {
  parseInputMatch,
  parseConstraints,
  parseOutputMatch,
  serializeInputMatch,
  serializeConstraints,
  serializeOutputMatch,
  isValidGlobalRuleIdSlug,
};
