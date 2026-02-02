import YAML from "yaml";

export type PolicyMatchDraft = {
  tenants?: string[];
  topics?: string[];
  capabilities?: string[];
  risk_tags?: string[];
  requires?: string[];
  pack_ids?: string[];
  actor_ids?: string[];
  actor_types?: string[];
  labels?: Record<string, string>;
  secrets_present?: boolean;
  mcp?: Record<string, unknown>;
};

export type BudgetConstraintsDraft = {
  max_runtime_ms?: number;
  max_retries?: number;
  max_artifact_bytes?: number;
  max_concurrent_jobs?: number;
};

export type SandboxConstraintsDraft = {
  isolated?: boolean;
  network_allowlist?: string[];
  fs_read_only?: string[];
  fs_read_write?: string[];
};

export type ToolchainConstraintsDraft = {
  allowed_tools?: string[];
  allowed_commands?: string[];
};

export type DiffConstraintsDraft = {
  max_files?: number;
  max_lines?: number;
  deny_path_globs?: string[];
};

export type PolicyConstraintsDraft = {
  budgets?: BudgetConstraintsDraft;
  sandbox?: SandboxConstraintsDraft;
  toolchain?: ToolchainConstraintsDraft;
  diff?: DiffConstraintsDraft;
  redaction_level?: string;
};

export type PolicyRemediationDraft = {
  id?: string;
  title?: string;
  summary?: string;
  replacement_topic?: string;
  replacement_capability?: string;
  add_labels?: Record<string, string>;
  remove_labels?: string[];
};

export type PolicyRuleDraft = {
  uid: string;
  id?: string;
  decision?: string;
  reason?: string;
  match?: PolicyMatchDraft;
  constraints?: PolicyConstraintsDraft;
  remediations?: PolicyRemediationDraft[];
};

export type PolicyBundleRoot = Record<string, unknown>;

export type PolicyBundleParseResult = {
  root: PolicyBundleRoot | null;
  rules: PolicyRuleDraft[];
  error?: string;
  hasLegacyTenants?: boolean;
};

const decisionAliases: Record<string, string> = {
  permit: "allow",
  block: "deny",
  "require-approval": "require_approval",
  require_human: "require_approval",
  "allow-with-constraints": "allow_with_constraints",
};

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  return {};
}

function toString(value: unknown): string | undefined {
  if (typeof value === "string") {
    const trimmed = value.trim();
    return trimmed ? trimmed : undefined;
  }
  return undefined;
}

function toStringArray(value: unknown): string[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const items = value
    .map((item) => (typeof item === "string" ? item.trim() : ""))
    .filter(Boolean);
  return items.length ? items : undefined;
}

function toNumber(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return undefined;
}

function normalizeDecision(value?: string): string | undefined {
  if (!value) {
    return undefined;
  }
  const normalized = value.trim().toLowerCase();
  return decisionAliases[normalized] || normalized;
}

function normalizeMatch(match: Record<string, unknown>): PolicyMatchDraft | undefined {
  const tenants = toStringArray(match.tenants);
  const topics = toStringArray(match.topics);
  const capabilities = toStringArray(match.capabilities);
  const riskTags = toStringArray(match.risk_tags);
  const requires = toStringArray(match.requires);
  const packIds = toStringArray(match.pack_ids);
  const actorIds = toStringArray(match.actor_ids);
  const actorTypes = toStringArray(match.actor_types);
  const labels = asRecord(match.labels);
  const secretsPresent = typeof match.secrets_present === "boolean" ? match.secrets_present : undefined;
  const mcp = asRecord(match.mcp);

  const hasLabels = Object.keys(labels).length > 0;
  const hasMcp = Object.keys(mcp).length > 0;
  const out: PolicyMatchDraft = {
    tenants,
    topics,
    capabilities,
    risk_tags: riskTags,
    requires,
    pack_ids: packIds,
    actor_ids: actorIds,
    actor_types: actorTypes,
    labels: hasLabels ? (labels as Record<string, string>) : undefined,
    secrets_present: secretsPresent,
    mcp: hasMcp ? mcp : undefined,
  };
  if (
    !out.tenants &&
    !out.topics &&
    !out.capabilities &&
    !out.risk_tags &&
    !out.requires &&
    !out.pack_ids &&
    !out.actor_ids &&
    !out.actor_types &&
    !out.labels &&
    out.secrets_present === undefined &&
    !out.mcp
  ) {
    return undefined;
  }
  return out;
}

function normalizeConstraints(raw: Record<string, unknown>): PolicyConstraintsDraft | undefined {
  const budgetsRaw = asRecord(raw.budgets);
  const budgets: BudgetConstraintsDraft = {
    max_runtime_ms: toNumber(budgetsRaw.max_runtime_ms),
    max_retries: toNumber(budgetsRaw.max_retries),
    max_artifact_bytes: toNumber(budgetsRaw.max_artifact_bytes),
    max_concurrent_jobs: toNumber(budgetsRaw.max_concurrent_jobs),
  };

  const sandboxRaw = asRecord(raw.sandbox);
  const sandbox: SandboxConstraintsDraft = {
    isolated: typeof sandboxRaw.isolated === "boolean" ? sandboxRaw.isolated : undefined,
    network_allowlist: toStringArray(sandboxRaw.network_allowlist),
    fs_read_only: toStringArray(sandboxRaw.fs_read_only),
    fs_read_write: toStringArray(sandboxRaw.fs_read_write),
  };

  const toolchainRaw = asRecord(raw.toolchain);
  const toolchain: ToolchainConstraintsDraft = {
    allowed_tools: toStringArray(toolchainRaw.allowed_tools),
    allowed_commands: toStringArray(toolchainRaw.allowed_commands),
  };

  const diffRaw = asRecord(raw.diff);
  const diff: DiffConstraintsDraft = {
    max_files: toNumber(diffRaw.max_files),
    max_lines: toNumber(diffRaw.max_lines),
    deny_path_globs: toStringArray(diffRaw.deny_path_globs),
  };

  const redactionLevel = toString(raw.redaction_level);

  const hasBudgets = Object.values(budgets).some((value) => value !== undefined);
  const hasSandbox = Object.values(sandbox).some((value) => value !== undefined);
  const hasToolchain = Object.values(toolchain).some((value) => value !== undefined);
  const hasDiff = Object.values(diff).some((value) => value !== undefined);

  if (!hasBudgets && !hasSandbox && !hasToolchain && !hasDiff && !redactionLevel) {
    return undefined;
  }
  return {
    budgets: hasBudgets ? budgets : undefined,
    sandbox: hasSandbox ? sandbox : undefined,
    toolchain: hasToolchain ? toolchain : undefined,
    diff: hasDiff ? diff : undefined,
    redaction_level: redactionLevel,
  };
}

function normalizeRemediations(value: unknown): PolicyRemediationDraft[] | undefined {
  if (!Array.isArray(value)) {
    return undefined;
  }
  const out = value
    .map((entry) => {
      const raw = asRecord(entry);
      const addLabels = asRecord(raw.add_labels);
      const removeLabels = toStringArray(raw.remove_labels);
      const remediation: PolicyRemediationDraft = {
        id: toString(raw.id),
        title: toString(raw.title),
        summary: toString(raw.summary),
        replacement_topic: toString(raw.replacement_topic),
        replacement_capability: toString(raw.replacement_capability),
        add_labels: Object.keys(addLabels).length ? (addLabels as Record<string, string>) : undefined,
        remove_labels: removeLabels,
      };
      return remediation;
    })
    .filter((item) => Object.values(item).some((value) => value !== undefined));
  return out.length ? out : undefined;
}

export function parsePolicyBundle(content: string): PolicyBundleParseResult {
  if (!content.trim()) {
    return { root: null, rules: [] };
  }
  try {
    const parsed = YAML.parse(content);
    const root = asRecord(parsed);
    const rawRules = Array.isArray(root.rules) ? root.rules : [];
    const rules: PolicyRuleDraft[] = rawRules.map((rule, index) => {
      const raw = asRecord(rule);
      const match = normalizeMatch(asRecord(raw.match));
      const constraints = normalizeConstraints(asRecord(raw.constraints));
      const remediations = normalizeRemediations(raw.remediations);
      return {
        uid: toString(raw.id) || `rule-${index + 1}`,
        id: toString(raw.id),
        decision: normalizeDecision(toString(raw.decision)),
        reason: toString(raw.reason),
        match,
        constraints,
        remediations,
      };
    });
    const hasLegacyTenants = Boolean(root.tenants && !rawRules.length);
    return { root, rules, hasLegacyTenants };
  } catch (err) {
    const message = err instanceof Error ? err.message : "Failed to parse bundle";
    return { root: null, rules: [], error: message };
  }
}

function compactRecord<T extends Record<string, unknown>>(value: T | undefined): Record<string, unknown> | undefined {
  if (!value) {
    return undefined;
  }
  const out: Record<string, unknown> = {};
  Object.entries(value).forEach(([key, val]) => {
    if (val === undefined) {
      return;
    }
    if (Array.isArray(val)) {
      if (val.length > 0) {
        out[key] = val;
      }
      return;
    }
    if (typeof val === "object" && val && Object.keys(val).length === 0) {
      return;
    }
    out[key] = val;
  });
  return Object.keys(out).length ? out : undefined;
}

export function serializePolicyRules(rules: PolicyRuleDraft[]): Record<string, unknown>[] {
  return rules.map((rule) => {
    const match = compactRecord({
      tenants: rule.match?.tenants,
      topics: rule.match?.topics,
      capabilities: rule.match?.capabilities,
      risk_tags: rule.match?.risk_tags,
      requires: rule.match?.requires,
      pack_ids: rule.match?.pack_ids,
      actor_ids: rule.match?.actor_ids,
      actor_types: rule.match?.actor_types,
      labels: rule.match?.labels,
      secrets_present: rule.match?.secrets_present,
      mcp: rule.match?.mcp,
    });

    const budgets = compactRecord({
      max_runtime_ms: rule.constraints?.budgets?.max_runtime_ms,
      max_retries: rule.constraints?.budgets?.max_retries,
      max_artifact_bytes: rule.constraints?.budgets?.max_artifact_bytes,
      max_concurrent_jobs: rule.constraints?.budgets?.max_concurrent_jobs,
    });
    const sandbox = compactRecord({
      isolated: rule.constraints?.sandbox?.isolated,
      network_allowlist: rule.constraints?.sandbox?.network_allowlist,
      fs_read_only: rule.constraints?.sandbox?.fs_read_only,
      fs_read_write: rule.constraints?.sandbox?.fs_read_write,
    });
    const toolchain = compactRecord({
      allowed_tools: rule.constraints?.toolchain?.allowed_tools,
      allowed_commands: rule.constraints?.toolchain?.allowed_commands,
    });
    const diff = compactRecord({
      max_files: rule.constraints?.diff?.max_files,
      max_lines: rule.constraints?.diff?.max_lines,
      deny_path_globs: rule.constraints?.diff?.deny_path_globs,
    });
    const constraints = compactRecord({
      budgets,
      sandbox,
      toolchain,
      diff,
      redaction_level: rule.constraints?.redaction_level,
    });

    const remediations = rule.remediations?.map((remediation) =>
      compactRecord({
        id: remediation.id,
        title: remediation.title,
        summary: remediation.summary,
        replacement_topic: remediation.replacement_topic,
        replacement_capability: remediation.replacement_capability,
        add_labels: remediation.add_labels,
        remove_labels: remediation.remove_labels,
      })
    ).filter(Boolean) as Record<string, unknown>[] | undefined;

    return compactRecord({
      id: rule.id,
      decision: rule.decision,
      reason: rule.reason,
      match,
      constraints,
      remediations: remediations && remediations.length ? remediations : undefined,
    }) || {};
  });
}

export function updateBundleRules(root: PolicyBundleRoot | null, rules: PolicyRuleDraft[]): string {
  const base = root ? JSON.parse(JSON.stringify(root)) : {};
  const nextRules = serializePolicyRules(rules);
  base.rules = nextRules;
  if (!base.version) {
    base.version = "v1";
  }
  return YAML.stringify(base, { lineWidth: 0 });
}
