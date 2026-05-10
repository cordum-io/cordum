import { useQuery } from "@tanstack/react-query";
import { get } from "../api/client";
import { queryKeys } from "../lib/queryKeys";
import type { Rule } from "@/api/generated/model/rule";
import type { RuleScope } from "@/api/generated/model/ruleScope";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import type { AuditMetadata } from "@/api/generated/model/auditMetadata";

export interface RuleFilters {
  type?: RuleType;
  scope?: string;
  status?: RuleStatus;
  search?: string;
  cursor?: string;
  limit?: number;
}

// Sentinel used when a row's type field is missing or doesn't match any known
// legacy/unified hint. The page renders this through `ruleTypeLabel` /
// `ruleTypeIcon` as the safe "Unknown" + Shield fallback. DoD #2 requires that
// only truly unmapped/missing types reach this state — known legacy hints
// (input_rule, OutputRule, velocity_rule, edge_policy, classifier, ...) keep
// mapping to the unified RuleType.
export const UNKNOWN_RULE_TYPE = "unknown" as const;
export type RuleTypeOrUnknown = RuleType | typeof UNKNOWN_RULE_TYPE;

// Backend may attach `firing_last_7d` (legacy histogram) onto rule rows even
// after the unified Rule envelope. Preserve it as an extension field so
// PoliciesPage can render the sparkline column without per-row casts. The
// `type` field is widened to admit the UNKNOWN_RULE_TYPE sentinel for rows
// the normalizer cannot classify.
export type NormalizedRule = Omit<Rule, "type"> & {
  type: RuleTypeOrUnknown;
  firing_last_7d?: unknown;
};

export interface RulesListResult {
  rules: NormalizedRule[];
  total: number;
  nextCursor?: string;
}

interface BackendRulesListResponse {
  items?: unknown;
  rules?: unknown;
  total?: number;
  next_cursor?: string;
}

const RULE_SCOPE_KINDS = new Set<string>(Object.values(RuleScopeKind));
const RULE_STATUSES = new Set<string>(Object.values(RuleStatus));

// Pattern table maps legacy/snake_case/PascalCase type hints to the unified
// generated RuleType. Order matters: longer/more-specific patterns first so
// "input_rule" doesn't accidentally hit a broader rule.
const RULE_TYPE_HINTS: Array<[RegExp, RuleType]> = [
  [/(^|_)edge(_|$)|edge_policy|edgerule/i, RuleType.edge],
  [/classifier|action_class|action_classification/i, RuleType.edge],
  [/(^|_)velocity(_|$)|velocityrule/i, RuleType.velocity],
  [/(^|_)output(_|$)|outputrule/i, RuleType.output],
  [/(^|_)input(_|$)|inputrule/i, RuleType.input],
];

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function pickString(o: Record<string, unknown>, ...keys: string[]): string {
  for (const k of keys) {
    const v = o[k];
    if (typeof v === "string" && v.length > 0) return v;
  }
  return "";
}

function pickObject(o: Record<string, unknown>, ...keys: string[]): Record<string, unknown> {
  for (const k of keys) {
    const v = o[k];
    if (isPlainObject(v)) return v;
  }
  return {};
}

function pickArray(o: Record<string, unknown>, ...keys: string[]): unknown[] {
  for (const k of keys) {
    const v = o[k];
    if (Array.isArray(v)) return v;
  }
  return [];
}

export function normalizeRuleType(row: Record<string, unknown>): RuleTypeOrUnknown {
  const candidates = [
    pickString(row, "type", "rule_type", "ruleType", "kind", "category"),
    pickString(row, "classifier"),
    isPlainObject(row.action_classification) ? "action_classification" : "",
    isPlainObject(row.classification) ? "classification" : "",
  ];
  for (const candidate of candidates) {
    if (!candidate) continue;
    for (const [pattern, mapped] of RULE_TYPE_HINTS) {
      if (pattern.test(candidate)) return mapped;
    }
  }
  // No known legacy/unified hint matched and no type-shaped fields were
  // present — render the safe Unknown fallback rather than silently coercing
  // to RuleType.input (DoD #2: Unknown is the safe fallback ONLY for truly
  // unmapped/missing type values).
  return UNKNOWN_RULE_TYPE;
}

export function normalizeRuleScope(row: Record<string, unknown>): RuleScope {
  const raw = row.scope;
  if (isPlainObject(raw)) {
    const kindStr = pickString(raw, "kind");
    if (RULE_SCOPE_KINDS.has(kindStr)) {
      const kind = kindStr as RuleScopeKind;
      const value = pickString(raw, "value");
      return value ? { kind, value } : { kind };
    }
  }
  if (typeof raw === "string" && RULE_SCOPE_KINDS.has(raw)) {
    return { kind: raw as RuleScopeKind };
  }
  const snakeKind = pickString(row, "scope_kind", "scopeKind");
  if (RULE_SCOPE_KINDS.has(snakeKind)) {
    const kind = snakeKind as RuleScopeKind;
    const value = pickString(row, "scope_value", "scopeValue");
    return value ? { kind, value } : { kind };
  }
  const tenantId = pickString(row, "tenant_id", "tenantId", "tenant");
  if (tenantId && tenantId !== "*") {
    return { kind: RuleScopeKind.tenant, value: tenantId };
  }
  const matchObj = isPlainObject(row.match) ? row.match : null;
  if (matchObj) {
    const tenants = pickArray(matchObj, "tenants");
    const first = tenants[0];
    if (typeof first === "string" && first.length > 0 && first !== "*") {
      return { kind: RuleScopeKind.tenant, value: first };
    }
  }
  return { kind: RuleScopeKind.global };
}

export function normalizeRuleStatus(row: Record<string, unknown>): RuleStatus {
  const explicit = pickString(row, "status", "lifecycle");
  if (RULE_STATUSES.has(explicit)) return explicit as RuleStatus;
  const enabled = row.enabled;
  if (typeof enabled === "boolean") {
    return enabled ? RuleStatus.published : RuleStatus.deprecated;
  }
  return RuleStatus.draft;
}

function normalizeAudit(row: Record<string, unknown>): AuditMetadata {
  const auditObj = isPlainObject(row.audit) ? row.audit : null;
  const sourceObj = isPlainObject(row.source) ? row.source : null;
  const fromAudit = (...keys: string[]) =>
    auditObj ? pickString(auditObj, ...keys) : "";
  const fromSource = (...keys: string[]) =>
    sourceObj ? pickString(sourceObj, ...keys) : "";

  const created_at =
    fromAudit("created_at", "createdAt") ||
    pickString(row, "created_at", "createdAt") ||
    fromSource("installed_at", "installedAt");
  const created_by =
    fromAudit("created_by", "createdBy") ||
    pickString(row, "created_by", "createdBy", "author");
  const updated_at =
    fromAudit("updated_at", "updatedAt") || pickString(row, "updated_at", "updatedAt");
  const updated_by =
    fromAudit("updated_by", "updatedBy") || pickString(row, "updated_by", "updatedBy");

  const audit: AuditMetadata = { created_at, created_by };
  if (updated_at) audit.updated_at = updated_at;
  if (updated_by) audit.updated_by = updated_by;
  return audit;
}

function normalizeDecide(row: Record<string, unknown>): Record<string, unknown> {
  const decide = row.decide;
  if (isPlainObject(decide)) return { ...decide };
  const inferred = pickString(row, "decision", "action");
  if (inferred) return { type: inferred.toLowerCase() };
  return { type: "allow" };
}

// Accepts an `unknown` backend row and returns a dashboard-safe NormalizedRule
// or `null` when the row is unsalvageable (no id). Never throws on malformed
// input, even when fields are missing or have unexpected types.
export function normalizeRule(raw: unknown): NormalizedRule | null {
  if (!isPlainObject(raw)) return null;
  const id = pickString(raw, "id", "rule_id", "ruleId");
  if (!id) return null;
  const sourceObj = isPlainObject(raw.source) ? raw.source : null;
  const version =
    pickString(raw, "version") ||
    (sourceObj ? pickString(sourceObj, "version") : "") ||
    "v1";
  const description = pickString(raw, "description");
  const rule: NormalizedRule = {
    id,
    name: pickString(raw, "name") || id,
    type: normalizeRuleType(raw),
    scope: normalizeRuleScope(raw),
    status: normalizeRuleStatus(raw),
    version,
    audit: normalizeAudit(raw),
    match: pickObject(raw, "match", "conditions"),
    decide: normalizeDecide(raw),
  };
  if (description) rule.description = description;
  if (raw.firing_last_7d !== undefined) {
    rule.firing_last_7d = raw.firing_last_7d;
  } else if (raw.firingLast7d !== undefined) {
    rule.firing_last_7d = raw.firingLast7d;
  }
  return rule;
}

function rulesListPath(filters: RuleFilters): string {
  const params = new URLSearchParams();
  if (filters.type) params.set("type", filters.type);
  if (filters.scope) params.set("scope", filters.scope);
  if (filters.status) params.set("status", filters.status);
  if (filters.search) params.set("search", filters.search);
  if (filters.cursor) params.set("cursor", filters.cursor);
  if (filters.limit !== undefined) params.set("limit", String(filters.limit));
  const qs = params.toString();
  return `/policy/rules${qs ? `?${qs}` : ""}`;
}

function normalizeRulesList(response: BackendRulesListResponse): RulesListResult {
  const rawList = Array.isArray(response.rules)
    ? response.rules
    : Array.isArray(response.items)
      ? response.items
      : [];
  const rules: NormalizedRule[] = [];
  for (const raw of rawList) {
    const rule = normalizeRule(raw);
    if (rule) rules.push(rule);
  }
  const result: RulesListResult = {
    rules,
    total: typeof response.total === "number" ? response.total : rules.length,
  };
  if (typeof response.next_cursor === "string" && response.next_cursor.length > 0) {
    result.nextCursor = response.next_cursor;
  }
  return result;
}

export function useRulesList(filters: RuleFilters = {}) {
  return useQuery<RulesListResult>({
    queryKey: queryKeys.policyStudioRules.list(filters),
    queryFn: async () =>
      normalizeRulesList(await get<BackendRulesListResponse>(rulesListPath(filters))),
    staleTime: 30_000,
  });
}
