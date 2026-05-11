import yaml from "yaml";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import type { NormalizedRule } from "@/hooks/useRulesList";

const RULE_TYPE_VALUES = new Set<string>(Object.values(RuleType));
const RULE_STATUS_VALUES = new Set<string>(Object.values(RuleStatus));
const RULE_SCOPE_KIND_VALUES = new Set<string>(Object.values(RuleScopeKind));

/**
 * Serializes a NormalizedRule into the canonical YAML the editor presents
 * to authors. Field order matches the spec: id/name/type/scope/status come
 * first (envelope), then version, then audit, then match/decide. Empty
 * envelope fields are still emitted so the YAML structure is stable across
 * round-trips.
 */
export function ruleToYaml(rule: NormalizedRule): string {
  const out: Record<string, unknown> = {
    id: rule.id,
    name: rule.name,
    type: rule.type,
    scope: rule.scope,
    status: rule.status,
    version: rule.version,
  };
  if (rule.description) {
    out.description = rule.description;
  }
  out.match = rule.match ?? {};
  out.decide = rule.decide ?? {};
  // Audit metadata is informational on the authoring surface; emit at the
  // bottom so it doesn't clutter the typing area but stays round-trippable.
  if (rule.audit && (rule.audit.created_at || rule.audit.updated_at)) {
    out.audit = rule.audit;
  }
  return yaml.stringify(out, { indent: 2, lineWidth: 100 });
}

export interface ParseResult {
  rule: NormalizedRule | null;
  error: string | null;
}

/**
 * Parses YAML text back into a NormalizedRule, merging with the existing
 * rule so envelope fields the user hasn't touched are preserved. Returns
 * `{ rule: null, error: <message> }` when the YAML is unparseable or the
 * envelope is invalid; the editor surfaces this in a non-destructive
 * banner without overwriting the in-memory rule.
 *
 * Author errors are recoverable. We accept partial documents (missing
 * fields default to the existing rule) so a half-typed YAML doesn't
 * panic the round-trip.
 */
export function yamlToPartialRule(text: string, base: NormalizedRule): ParseResult {
  let raw: unknown;
  try {
    raw = yaml.parse(text);
  } catch (err) {
    return {
      rule: null,
      error: err instanceof Error ? err.message : "YAML parse failed",
    };
  }
  if (raw == null) {
    // Empty document — preserve the base rule and surface no error.
    return { rule: base, error: null };
  }
  if (typeof raw !== "object" || Array.isArray(raw)) {
    return { rule: null, error: "Top-level YAML must be an object." };
  }
  const obj = raw as Record<string, unknown>;

  const idVal = pickString(obj, "id");
  const nameVal = pickString(obj, "name");
  const typeVal = pickString(obj, "type");
  if (typeVal && !RULE_TYPE_VALUES.has(typeVal)) {
    return { rule: null, error: `Unsupported rule type "${typeVal}".` };
  }
  const statusVal = pickString(obj, "status");
  if (statusVal && !RULE_STATUS_VALUES.has(statusVal)) {
    return { rule: null, error: `Unsupported rule status "${statusVal}".` };
  }
  const scopeRes = parseScope(obj.scope, base.scope);
  if (scopeRes.error) {
    return { rule: null, error: scopeRes.error };
  }
  const versionVal = pickString(obj, "version");
  const descVal = pickString(obj, "description");

  const merged: NormalizedRule = {
    ...base,
    id: idVal || base.id,
    name: nameVal || base.name,
    type: (typeVal as RuleType) || base.type,
    scope: scopeRes.scope ?? base.scope,
    status: (statusVal as RuleStatus) || base.status,
    version: versionVal || base.version,
    match: pickRecord(obj, "match") ?? base.match,
    decide: pickRecord(obj, "decide") ?? base.decide,
  };
  if (descVal !== "") {
    merged.description = descVal;
  } else {
    delete merged.description;
  }
  // Audit is preserved from the base rule by default; authors don't edit it
  // directly. If the YAML carries an audit block we accept it for
  // round-trip but never use it as the source of truth on save.
  const auditRecord = pickRecord(obj, "audit");
  if (auditRecord) {
    merged.audit = {
      created_at: typeof auditRecord.created_at === "string" ? auditRecord.created_at : base.audit.created_at,
      created_by: typeof auditRecord.created_by === "string" ? auditRecord.created_by : base.audit.created_by,
      ...(typeof auditRecord.updated_at === "string" ? { updated_at: auditRecord.updated_at } : {}),
      ...(typeof auditRecord.updated_by === "string" ? { updated_by: auditRecord.updated_by } : {}),
    };
  }
  return { rule: merged, error: null };
}

function pickString(o: Record<string, unknown>, key: string): string {
  const v = o[key];
  return typeof v === "string" ? v : "";
}

function pickRecord(o: Record<string, unknown>, key: string): Record<string, unknown> | null {
  const v = o[key];
  if (v && typeof v === "object" && !Array.isArray(v)) {
    return v as Record<string, unknown>;
  }
  return null;
}

interface ScopeParseResult {
  scope: NormalizedRule["scope"] | null;
  error: string | null;
}

function parseScope(raw: unknown, fallback: NormalizedRule["scope"]): ScopeParseResult {
  if (raw == null) return { scope: fallback, error: null };
  if (typeof raw === "string") {
    if (RULE_SCOPE_KIND_VALUES.has(raw)) {
      return { scope: { kind: raw as RuleScopeKind }, error: null };
    }
    return { scope: null, error: `Unsupported scope kind "${raw}".` };
  }
  if (typeof raw !== "object" || Array.isArray(raw)) {
    return { scope: null, error: "scope must be a string or { kind, value? } object." };
  }
  const obj = raw as Record<string, unknown>;
  const kindStr = typeof obj.kind === "string" ? obj.kind : "";
  if (!RULE_SCOPE_KIND_VALUES.has(kindStr)) {
    return { scope: null, error: `Unsupported scope kind "${kindStr || "(empty)"}".` };
  }
  const kind = kindStr as RuleScopeKind;
  const valueStr = typeof obj.value === "string" ? obj.value : "";
  return {
    scope: valueStr ? { kind, value: valueStr } : { kind },
    error: null,
  };
}
