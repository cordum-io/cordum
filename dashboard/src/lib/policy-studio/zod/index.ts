import type { z } from "zod";
import { RuleType } from "@/api/generated/model/ruleType";
import type { NormalizedRule } from "@/hooks/useRulesList";
import { inputRuleFormSchema, type InputRuleFormValues } from "./input";
import { outputRuleFormSchema, type OutputRuleFormValues } from "./output";
import { velocityRuleFormSchema, type VelocityRuleFormValues } from "./velocity";
import { edgeRuleFormSchema, type EdgeRuleFormValues } from "./edge";

export {
  inputRuleFormSchema,
  outputRuleFormSchema,
  velocityRuleFormSchema,
  edgeRuleFormSchema,
};

export type {
  EnvelopeFormValues,
} from "./envelope";

export type {
  InputRuleFormValues,
  OutputRuleFormValues,
  VelocityRuleFormValues,
  EdgeRuleFormValues,
};

// Discriminated union of every rule-type's form-values shape. Consumers
// that don't need the per-type narrowing can use this as the union; the
// per-type modules export the narrow types.
export type RuleFormValues =
  | InputRuleFormValues
  | OutputRuleFormValues
  | VelocityRuleFormValues
  | EdgeRuleFormValues;

const SCHEMA_BY_TYPE: Record<RuleType, z.ZodTypeAny> = {
  [RuleType.input]: inputRuleFormSchema,
  [RuleType.output]: outputRuleFormSchema,
  [RuleType.velocity]: velocityRuleFormSchema,
  [RuleType.edge]: edgeRuleFormSchema,
};

/**
 * Returns the Zod schema for the given rule type. Returns null when the
 * type is not one of the four supported authoring types — callers should
 * fall back to the safe Unknown empty state rather than picking a default.
 */
export function getRuleFormSchema(type: RuleType | string | null | undefined): z.ZodTypeAny | null {
  if (typeof type !== "string") return null;
  if (!Object.prototype.hasOwnProperty.call(SCHEMA_BY_TYPE, type)) return null;
  return SCHEMA_BY_TYPE[type as RuleType];
}

// String-array helpers: read a string-array out of a free-form payload
// without coercing non-string entries (drop them silently). The form
// view normalizes user input to string[] before submitting, so on
// submission these helpers are usually identity.
function readStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((entry): entry is string => typeof entry === "string");
}

function readOptionalString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

// Per-type extractors: pull the form-relevant fields out of the rule's
// free-form match/decide payload. Unknown / extra keys are deliberately
// left in place on the source `rule.match`/`rule.decide` objects; we read
// from the rule, but on submit we merge the form output back over the
// preserved source so unknown YAML keys round-trip without loss.

interface FormView<TValues extends RuleFormValues> {
  fromRule: (rule: NormalizedRule) => TValues;
  toRule: (values: TValues, base: NormalizedRule) => NormalizedRule;
}

function envelopeFromRule(rule: NormalizedRule) {
  return {
    name: rule.name ?? "",
    description: rule.description ?? "",
    scope: {
      kind: rule.scope.kind,
      value: rule.scope.value ?? "",
    },
    status: rule.status,
  };
}

// Drop empty optional fields and empty arrays so the canonical rule
// shape stays minimal. We never drop keys with declared/required values.
function compactObject<T extends Record<string, unknown>>(
  obj: T,
  keep: ReadonlyArray<keyof T>,
): Partial<T> {
  const out: Partial<T> = {};
  for (const key of keep) out[key] = obj[key];
  for (const key of Object.keys(obj) as Array<keyof T>) {
    if (keep.includes(key)) continue;
    const value = obj[key];
    if (value === undefined || value === "") continue;
    if (Array.isArray(value) && value.length === 0) continue;
    out[key] = value;
  }
  return out;
}

function envelopeOnRule(values: { name: string; description?: string; scope: { kind: NormalizedRule["scope"]["kind"]; value?: string }; status: NormalizedRule["status"] }, base: NormalizedRule): NormalizedRule {
  const next: NormalizedRule = {
    ...base,
    name: values.name.trim(),
    scope: values.scope.value
      ? { kind: values.scope.kind, value: values.scope.value.trim() }
      : { kind: values.scope.kind },
    status: values.status,
  };
  const description = values.description?.trim();
  if (description) next.description = description;
  else delete next.description;
  return next;
}

const inputView: FormView<InputRuleFormValues> = {
  fromRule(rule) {
    const match = (rule.match ?? {}) as Record<string, unknown>;
    const decide = (rule.decide ?? {}) as Record<string, unknown>;
    const decideType = readOptionalString(decide.type) || "allow";
    return {
      ...envelopeFromRule(rule),
      match: {
        topics: readStringArray(match.topics),
        tools: readStringArray(match.tools),
        risk_tags: readStringArray(match.risk_tags),
        content_pattern: readOptionalString(match.content_pattern),
      },
      decide: {
        type: decideType as InputRuleFormValues["decide"]["type"],
        reason: readOptionalString(decide.reason),
      },
    };
  },
  toRule(values, base) {
    const matchSource = (base.match ?? {}) as Record<string, unknown>;
    const decideSource = (base.decide ?? {}) as Record<string, unknown>;
    const matchOverlay = compactObject(
      {
        topics: values.match.topics ?? [],
        tools: values.match.tools ?? [],
        risk_tags: values.match.risk_tags ?? [],
        content_pattern: values.match.content_pattern ?? "",
      } as Record<string, unknown>,
      [],
    );
    const decideOverlay = compactObject(
      {
        type: values.decide.type,
        reason: values.decide.reason ?? "",
      } as Record<string, unknown>,
      ["type"],
    );
    return envelopeOnRule(values, {
      ...base,
      match: { ...matchSource, ...stripFormKeys(matchSource, ["topics", "tools", "risk_tags", "content_pattern"]), ...matchOverlay },
      decide: { ...stripFormKeys(decideSource, ["type", "reason"]), ...decideOverlay },
    });
  },
};

const outputView: FormView<OutputRuleFormValues> = {
  fromRule(rule) {
    const match = (rule.match ?? {}) as Record<string, unknown>;
    const decide = (rule.decide ?? {}) as Record<string, unknown>;
    const decideType = readOptionalString(decide.type) || "allow";
    return {
      ...envelopeFromRule(rule),
      match: {
        topics: readStringArray(match.topics),
        tools: readStringArray(match.tools),
        risk_tags: readStringArray(match.risk_tags),
        finding_types: readStringArray(match.finding_types) as OutputRuleFormValues["match"]["finding_types"],
      },
      decide: {
        type: decideType as OutputRuleFormValues["decide"]["type"],
        reason: readOptionalString(decide.reason),
        redact_strategy: readOptionalString(decide.redact_strategy),
      },
    };
  },
  toRule(values, base) {
    const matchSource = (base.match ?? {}) as Record<string, unknown>;
    const decideSource = (base.decide ?? {}) as Record<string, unknown>;
    const matchOverlay = compactObject(
      {
        topics: values.match.topics ?? [],
        tools: values.match.tools ?? [],
        risk_tags: values.match.risk_tags ?? [],
        finding_types: values.match.finding_types ?? [],
      } as Record<string, unknown>,
      [],
    );
    const decideOverlay = compactObject(
      {
        type: values.decide.type,
        reason: values.decide.reason ?? "",
        redact_strategy: values.decide.redact_strategy ?? "",
      } as Record<string, unknown>,
      ["type"],
    );
    return envelopeOnRule(values, {
      ...base,
      match: { ...stripFormKeys(matchSource, ["topics", "tools", "risk_tags", "finding_types"]), ...matchOverlay },
      decide: { ...stripFormKeys(decideSource, ["type", "reason", "redact_strategy"]), ...decideOverlay },
    });
  },
};

const velocityView: FormView<VelocityRuleFormValues> = {
  fromRule(rule) {
    const match = (rule.match ?? {}) as Record<string, unknown>;
    const decide = (rule.decide ?? {}) as Record<string, unknown>;
    const numberOrUndefined = (value: unknown): number | undefined =>
      typeof value === "number" && Number.isFinite(value) ? value : undefined;
    return {
      ...envelopeFromRule(rule),
      match: {
        tenants: readStringArray(match.tenants),
        topics: readStringArray(match.topics),
        risk_tags: readStringArray(match.risk_tags),
      },
      decide: {
        type: "throttle",
        max_per_minute: numberOrUndefined(decide.max_per_minute),
        max_per_hour: numberOrUndefined(decide.max_per_hour),
        max_per_day: numberOrUndefined(decide.max_per_day),
        burst_limit: numberOrUndefined(decide.burst_limit),
        reason: readOptionalString(decide.reason),
      },
    };
  },
  toRule(values, base) {
    const matchSource = (base.match ?? {}) as Record<string, unknown>;
    const decideSource = (base.decide ?? {}) as Record<string, unknown>;
    const matchOverlay = compactObject(
      {
        tenants: values.match.tenants ?? [],
        topics: values.match.topics ?? [],
        risk_tags: values.match.risk_tags ?? [],
      } as Record<string, unknown>,
      [],
    );
    const decideOverlay = compactObject(
      {
        type: "throttle",
        max_per_minute: values.decide.max_per_minute,
        max_per_hour: values.decide.max_per_hour,
        max_per_day: values.decide.max_per_day,
        burst_limit: values.decide.burst_limit,
        reason: values.decide.reason ?? "",
      } as Record<string, unknown>,
      ["type"],
    );
    return envelopeOnRule(values, {
      ...base,
      match: { ...stripFormKeys(matchSource, ["tenants", "topics", "risk_tags"]), ...matchOverlay },
      decide: {
        ...stripFormKeys(decideSource, [
          "type",
          "max_per_minute",
          "max_per_hour",
          "max_per_day",
          "burst_limit",
          "reason",
        ]),
        ...decideOverlay,
      },
    });
  },
};

const edgeView: FormView<EdgeRuleFormValues> = {
  fromRule(rule) {
    const match = (rule.match ?? {}) as Record<string, unknown>;
    const decide = (rule.decide ?? {}) as Record<string, unknown>;
    const decideType = readOptionalString(decide.type) || "allow";
    return {
      ...envelopeFromRule(rule),
      match: {
        tools: readStringArray(match.tools),
        command_pattern: readOptionalString(match.command_pattern),
        path_pattern: readOptionalString(match.path_pattern),
        risk_tags: readStringArray(match.risk_tags),
      },
      decide: {
        type: decideType as EdgeRuleFormValues["decide"]["type"],
        reason: readOptionalString(decide.reason),
      },
    };
  },
  toRule(values, base) {
    const matchSource = (base.match ?? {}) as Record<string, unknown>;
    const decideSource = (base.decide ?? {}) as Record<string, unknown>;
    const matchOverlay = compactObject(
      {
        tools: values.match.tools ?? [],
        command_pattern: values.match.command_pattern ?? "",
        path_pattern: values.match.path_pattern ?? "",
        risk_tags: values.match.risk_tags ?? [],
      } as Record<string, unknown>,
      [],
    );
    const decideOverlay = compactObject(
      {
        type: values.decide.type,
        reason: values.decide.reason ?? "",
      } as Record<string, unknown>,
      ["type"],
    );
    return envelopeOnRule(values, {
      ...base,
      match: { ...stripFormKeys(matchSource, ["tools", "command_pattern", "path_pattern", "risk_tags"]), ...matchOverlay },
      decide: { ...stripFormKeys(decideSource, ["type", "reason"]), ...decideOverlay },
    });
  },
};

const VIEW_BY_TYPE = {
  [RuleType.input]: inputView,
  [RuleType.output]: outputView,
  [RuleType.velocity]: velocityView,
  [RuleType.edge]: edgeView,
} as const;

function stripFormKeys(
  source: Record<string, unknown>,
  formKeys: ReadonlyArray<string>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(source)) {
    if (formKeys.includes(key)) continue;
    out[key] = value;
  }
  return out;
}

/**
 * Read form values out of a NormalizedRule. The rule's `type` selects
 * which Zod-typed view we instantiate; passing a rule whose type isn't
 * one of the four authoring types throws because the caller should have
 * gated the form mount on `ruleHasKnownType` already.
 */
export function ruleToFormValues<T extends RuleType>(
  rule: NormalizedRule & { type: T },
): RuleFormValues {
  const view = VIEW_BY_TYPE[rule.type];
  return view.fromRule(rule as never);
}

/**
 * Apply form values back to the canonical NormalizedRule. Unknown keys
 * inside `base.match`/`base.decide` (e.g. extras from a hand-edited
 * YAML) are preserved on the result; only the form's known keys are
 * replaced. The caller's canonical state remains the source of truth
 * for id/version/audit/type — this function never mutates them.
 */
export function formValuesToRule<T extends RuleType>(
  values: RuleFormValues,
  base: NormalizedRule & { type: T },
): NormalizedRule {
  const view = VIEW_BY_TYPE[base.type];
  return view.toRule(values as never, base);
}
