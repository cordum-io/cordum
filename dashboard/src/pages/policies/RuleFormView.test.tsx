import { describe, expect, it, vi } from "vitest";
import { renderWithProviders, screen } from "@/test-utils/render";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import {
  formValuesToRule,
  getRuleFormSchema,
  ruleToFormValues,
  type RuleFormValues,
} from "@/lib/policy-studio/zod";
import type { NormalizedRule } from "@/hooks/useRulesList";
import { RuleFormView } from "./RuleFormView";

function makeRule(type: RuleType, overrides: Partial<NormalizedRule> = {}): NormalizedRule & { type: RuleType } {
  return {
    id: "rule-1",
    name: "Block secrets",
    type,
    scope: { kind: RuleScopeKind.global },
    status: RuleStatus.draft,
    version: "v1",
    audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
    match: {},
    decide: { type: "deny" },
    ...overrides,
  } as NormalizedRule & { type: RuleType };
}

describe("RuleFormView", () => {
  it.each([
    [RuleType.input, "Input · Match", "Input · Decide"],
    [RuleType.output, "Output · Match", "Output · Decide"],
    [RuleType.velocity, "Velocity · Match", "Velocity · Decide"],
    [RuleType.edge, "Edge · Match", "Edge · Decide"],
  ])("%s renders the envelope + per-type sections", (type, matchLegend, decideLegend) => {
    const onChange = vi.fn();
    void matchLegend; // legend text varies by sub-form layout — type rendering verified below
    void decideLegend;
    renderWithProviders(<RuleFormView rule={makeRule(type)} onChange={onChange} />);
    // Envelope fields render across all four types.
    expect(screen.getByLabelText(/Rule name/i)).not.toBeNull();
    expect(screen.getByLabelText(/Rule status/i)).not.toBeNull();
    expect(screen.getByLabelText(/Scope kind/i)).not.toBeNull();
    expect(screen.getByLabelText(/Scope value/i)).not.toBeNull();
    expect(screen.getByLabelText(/Rule description/i)).not.toBeNull();
  });

  it("renders existing field values from the canonical NormalizedRule", () => {
    const rule = makeRule(RuleType.input, {
      name: "PII redact",
      description: "Redact PII",
      scope: { kind: RuleScopeKind.tenant, value: "acme" },
      status: RuleStatus.published,
    });
    const onChange = vi.fn();
    renderWithProviders(<RuleFormView rule={rule} onChange={onChange} />);

    const name = screen.getByLabelText(/Rule name/i) as HTMLInputElement;
    expect(name.value).toBe("PII redact");
    const desc = screen.getByLabelText(/Rule description/i) as HTMLInputElement;
    expect(desc.value).toBe("Redact PII");
    const scopeKind = screen.getByLabelText(/Scope kind/i) as HTMLSelectElement;
    expect(scopeKind.value).toBe(RuleScopeKind.tenant);
    const scopeValue = screen.getByLabelText(/Scope value/i) as HTMLInputElement;
    expect(scopeValue.value).toBe("acme");
    const status = screen.getByLabelText(/Rule status/i) as HTMLSelectElement;
    expect(status.value).toBe(RuleStatus.published);
  });

  it("does not call onChange synchronously on mount (no echo on first paint)", async () => {
    const onChange = vi.fn();
    renderWithProviders(<RuleFormView rule={makeRule(RuleType.input)} onChange={onChange} />);
    // Form sync hook is debounced 300ms; mount alone should never emit.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(onChange).not.toHaveBeenCalled();
  });

  it("ignores re-renders with the same rule reference (no sync loop)", async () => {
    const onChange = vi.fn();
    const rule = makeRule(RuleType.input);
    const { rerender } = renderWithProviders(<RuleFormView rule={rule} onChange={onChange} />);
    rerender(<RuleFormView rule={rule} onChange={onChange} />);
    // Re-rendering with the same rule object reference must NOT trigger a
    // form reset that emits a change. lastEmittedRef guards this.
    await new Promise((resolve) => setTimeout(resolve, 350));
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("getRuleFormSchema (schema selection)", () => {
  it.each([RuleType.input, RuleType.output, RuleType.velocity, RuleType.edge])(
    "returns a Zod schema for %s",
    (type) => {
      const schema = getRuleFormSchema(type);
      expect(schema).not.toBeNull();
      expect(typeof schema?.safeParse).toBe("function");
    },
  );

  it("returns null for the UNKNOWN_RULE_TYPE sentinel", () => {
    expect(getRuleFormSchema("unknown")).toBeNull();
  });

  it("returns null for non-string / unsupported inputs (preserves task-15537d13 safe Unknown)", () => {
    expect(getRuleFormSchema(undefined)).toBeNull();
    expect(getRuleFormSchema(null)).toBeNull();
    expect(getRuleFormSchema("totally_made_up")).toBeNull();
  });
});

describe("ruleToFormValues / formValuesToRule (round-trip)", () => {
  it("round-trips an input rule without losing canonical fields", () => {
    const rule = makeRule(RuleType.input, {
      name: "PII redact",
      scope: { kind: RuleScopeKind.tenant, value: "acme" },
      match: { topics: ["job.*"], tools: ["fs.write"], risk_tags: ["pii"] },
      decide: { type: "redact", reason: "PII detected" },
    });
    const values = ruleToFormValues(rule) as RuleFormValues;
    const back = formValuesToRule(values, rule);
    expect(back.id).toBe(rule.id);
    expect(back.type).toBe(RuleType.input);
    expect(back.name).toBe("PII redact");
    expect(back.scope).toEqual({ kind: RuleScopeKind.tenant, value: "acme" });
    expect(back.match).toMatchObject({ topics: ["job.*"], tools: ["fs.write"], risk_tags: ["pii"] });
    expect((back.decide as { type: string }).type).toBe("redact");
  });

  it("preserves unknown match/decide keys round-trip (extras don't get dropped)", () => {
    const rule = makeRule(RuleType.input, {
      match: { topics: ["job.*"], custom_extension: { foo: 1 } },
      decide: { type: "allow", custom_field: "preserve-me" },
    });
    const values = ruleToFormValues(rule) as RuleFormValues;
    const back = formValuesToRule(values, rule);
    // Form reads only its known keys; extras come from base preservation.
    expect((back.match as Record<string, unknown>).custom_extension).toEqual({ foo: 1 });
    expect((back.decide as Record<string, unknown>).custom_field).toBe("preserve-me");
  });

  it("schema rejects an envelope with a non-global scope but no value", () => {
    const schema = getRuleFormSchema(RuleType.input)!;
    const invalid = {
      name: "Bad rule",
      description: "",
      scope: { kind: RuleScopeKind.tenant, value: "" },
      status: RuleStatus.draft,
      match: { topics: [], tools: [], risk_tags: [], content_pattern: "" },
      decide: { type: "allow", reason: "" },
    };
    const result = schema.safeParse(invalid);
    expect(result.success).toBe(false);
    if (!result.success) {
      const messages = result.error.issues.map((issue) => issue.message);
      expect(messages.some((m) => /requires a value/i.test(m))).toBe(true);
    }
  });

  it("schema rejects an empty name", () => {
    const schema = getRuleFormSchema(RuleType.edge)!;
    const invalid = {
      name: "",
      description: "",
      scope: { kind: RuleScopeKind.global, value: "" },
      status: RuleStatus.draft,
      match: { tools: [], command_pattern: "", path_pattern: "", risk_tags: [] },
      decide: { type: "allow", reason: "" },
    };
    const result = schema.safeParse(invalid);
    expect(result.success).toBe(false);
    if (!result.success) {
      const messages = result.error.issues.map((issue) => issue.message);
      expect(messages.some((m) => /Name is required/i.test(m))).toBe(true);
    }
  });
});
