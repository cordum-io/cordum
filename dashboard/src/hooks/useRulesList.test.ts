import { beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse, ensureMswServerListening, server } from "@/test-utils/msw";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import type { Rule } from "@/api/generated/model/rule";
import {
  UNKNOWN_RULE_TYPE,
  normalizeRule,
  normalizeRuleScope,
  normalizeRuleStatus,
  normalizeRuleType,
  useRulesList,
  type RuleFilters,
} from "./useRulesList";

function makeRule(index: number, overrides: Partial<Rule> = {}): Rule {
  return {
    id: `rule-${index}`,
    name: `Rule ${index}`,
    type: RuleType.input,
    scope: { kind: RuleScopeKind.tenant, value: "acme" },
    status: RuleStatus.published,
    version: `v${index}`,
    audit: {
      created_at: "2026-05-09T09:00:00Z",
      created_by: "policy-admin",
      updated_at: "2026-05-09T10:00:00Z",
      updated_by: "policy-admin",
    },
    match: { pattern: "pii" },
    decide: { type: "deny" },
    description: `Fixture rule ${index}`,
    ...overrides,
  };
}

describe("useRulesList", () => {
  beforeEach(() => {
    ensureMswServerListening();
  });

  it("uses the default MSW rules-list handler to return an empty list", async () => {
    const hook = renderWithQueryClient(() => useRulesList());

    await hook.waitFor(() => {
      expect(hook.result.current?.data).toEqual({ rules: [], total: 0 });
    });

    hook.unmount();
  });

  it("serializes filters into query params and unmarshals a 50-rule page", async () => {
    const seenSearches: string[] = [];
    const rules = Array.from({ length: 50 }, (_, index) =>
      makeRule(index + 1, {
        id: `input-${index + 1}`,
        name: `Input rule ${index + 1}`,
      }),
    );
    server.use(
      http.get("*/api/v1/policy/rules", ({ request }) => {
        const url = new URL(request.url);
        seenSearches.push(url.search);
        return HttpResponse.json({ items: rules, total: 250 });
      }),
    );

    const filters: RuleFilters = {
      type: RuleType.input,
      scope: "tenant:acme",
      status: RuleStatus.published,
      search: "pii",
      cursor: "cursor-1",
      limit: 50,
    };
    const hook = renderWithQueryClient(() => useRulesList(filters));

    await hook.waitFor(() => {
      expect(hook.result.current?.data?.rules).toHaveLength(50);
    });

    const params = new URLSearchParams(seenSearches.at(-1));
    expect(params.get("type")).toBe(RuleType.input);
    expect(params.get("scope")).toBe("tenant:acme");
    expect(params.get("status")).toBe(RuleStatus.published);
    expect(params.get("search")).toBe("pii");
    expect(params.get("cursor")).toBe("cursor-1");
    expect(params.get("limit")).toBe("50");
    expect(hook.result.current?.data?.total).toBe(250);
    expect(hook.result.current?.data?.rules[0]?.id).toBe("input-1");

    hook.unmount();
  });

  it("normalizes a payload of mixed legacy shapes into renderable rows for tenant:acme", async () => {
    server.use(
      http.get("*/api/v1/policy/rules", () =>
        HttpResponse.json({
          items: [
            // Canonical unified Rule
            makeRule(1, { id: "unified-1" }),
            // Legacy InputRule (camelCase + ALLOW action)
            {
              id: "legacy-input-1",
              name: "Legacy input guard",
              type: "InputRule",
              tenant_id: "acme",
              enabled: true,
              action: "ALLOW",
              conditions: { topics: ["job.*"] },
              firing_last_7d: [0, 1, 0, 1, 0, 1, 0],
            },
            // Legacy OutputRule (snake_case + match.tenants)
            {
              id: "legacy-output-1",
              name: "Output redactor",
              rule_type: "output_rule",
              enabled: false,
              action: "BLOCK",
              match: { tenants: ["acme"], scanners: ["regex"] },
              source: { fragment_id: "policy.yaml", installed_at: "2026-04-01T00:00:00Z" },
            },
            // Legacy VelocityRule via classifier hint
            {
              id: "legacy-velocity-1",
              name: "Velocity guard",
              kind: "velocity_rule",
              scope_kind: "tenant",
              scope_value: "acme",
              status: "deprecated",
              audit: { created_at: "2026-03-01T00:00:00Z", created_by: "ops" },
            },
            // Edge classifier-style row (no explicit type)
            {
              id: "legacy-edge-1",
              name: "Edge classifier",
              classifier: "edge_action",
              tenant: "acme",
              enabled: true,
              decision: "deny",
            },
            // Action-classification shape — type embedded in nested object
            {
              id: "legacy-edge-2",
              name: "Action classifier",
              action_classification: { mode: "block" },
              match: { tenants: ["acme"] },
              status: "published",
            },
            // Truly unmapped type — must keep safe defaults but not crash
            {
              id: "legacy-mystery",
              name: "Mystery rule",
              type: "unsupported_kind",
              match: { tenants: ["acme"] },
            },
            // Unsalvageable: no id → dropped
            { name: "no-id", type: "input" },
          ],
          total: 7,
        }),
      ),
    );

    const hook = renderWithQueryClient(() => useRulesList());
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.rules.length).toBe(7);
    });
    const rules = hook.result.current!.data!.rules;
    const byId = new Map(rules.map((rule) => [rule.id, rule]));

    // tenant:acme should appear on every legacy row that hinted at acme
    for (const id of [
      "legacy-input-1",
      "legacy-output-1",
      "legacy-velocity-1",
      "legacy-edge-1",
      "legacy-edge-2",
      "legacy-mystery",
    ]) {
      expect(byId.get(id)?.scope).toEqual({ kind: RuleScopeKind.tenant, value: "acme" });
    }
    expect(byId.get("legacy-input-1")?.type).toBe(RuleType.input);
    expect(byId.get("legacy-input-1")?.status).toBe(RuleStatus.published);
    expect(byId.get("legacy-output-1")?.type).toBe(RuleType.output);
    expect(byId.get("legacy-output-1")?.status).toBe(RuleStatus.deprecated);
    expect(byId.get("legacy-velocity-1")?.type).toBe(RuleType.velocity);
    expect(byId.get("legacy-velocity-1")?.status).toBe(RuleStatus.deprecated);
    expect(byId.get("legacy-edge-1")?.type).toBe(RuleType.edge);
    expect(byId.get("legacy-edge-2")?.type).toBe(RuleType.edge);
    expect(byId.get("legacy-edge-2")?.status).toBe(RuleStatus.published);
    // Truly unmapped type — `unsupported_kind` matches no legacy hint, so the
    // normalizer returns the UNKNOWN_RULE_TYPE sentinel (DoD #2: Unknown is
    // the safe fallback ONLY for truly unmapped/missing type values; known
    // legacy mappings like InputRule/output_rule above keep their RuleType).
    expect(byId.get("legacy-mystery")?.type).toBe(UNKNOWN_RULE_TYPE);
    expect(byId.get("legacy-mystery")?.status).toBe(RuleStatus.draft);
    // firing_last_7d preserved verbatim where present
    expect(byId.get("legacy-input-1")?.firing_last_7d).toEqual([0, 1, 0, 1, 0, 1, 0]);
    // Source.installed_at synthesizes an audit.created_at when absent
    expect(byId.get("legacy-output-1")?.audit.created_at).toBe("2026-04-01T00:00:00Z");

    hook.unmount();
  });
});

describe("useRulesList — pure normalizers (table-driven)", () => {
  describe("normalizeRuleType", () => {
    type Expected = RuleType | typeof UNKNOWN_RULE_TYPE;
    const cases: Array<[string, Record<string, unknown>, Expected]> = [
      ["unified input enum", { type: "input" }, RuleType.input],
      ["unified output enum", { type: "output" }, RuleType.output],
      ["unified velocity enum", { type: "velocity" }, RuleType.velocity],
      ["unified edge enum", { type: "edge" }, RuleType.edge],
      ["camelCase InputRule", { type: "InputRule" }, RuleType.input],
      ["snake_case input_rule", { type: "input_rule" }, RuleType.input],
      ["snake_case output_rule via rule_type", { rule_type: "output_rule" }, RuleType.output],
      ["snake_case velocity_rule via kind", { kind: "velocity_rule" }, RuleType.velocity],
      ["edge_policy alias", { type: "edge_policy" }, RuleType.edge],
      ["EdgeRule via category", { category: "EdgeRule" }, RuleType.edge],
      ["classifier hint", { classifier: "edge_action" }, RuleType.edge],
      ["action_classification object", { action_classification: { mode: "block" } }, RuleType.edge],
      ["unmapped non-empty value falls back to Unknown", { type: "totally_made_up" }, UNKNOWN_RULE_TYPE],
      ["completely empty row falls back to Unknown", {}, UNKNOWN_RULE_TYPE],
      ["non-string type field falls back to Unknown", { type: 42 }, UNKNOWN_RULE_TYPE],
    ];
    it.each(cases)("%s", (_label, row, expected) => {
      expect(normalizeRuleType(row)).toBe(expected);
    });
  });

  describe("normalizeRuleScope", () => {
    it("preserves a unified scope object", () => {
      expect(normalizeRuleScope({ scope: { kind: "tenant", value: "acme" } })).toEqual({
        kind: RuleScopeKind.tenant,
        value: "acme",
      });
    });
    it("treats a string scope as kind-only when valid", () => {
      expect(normalizeRuleScope({ scope: "global" })).toEqual({ kind: RuleScopeKind.global });
    });
    it("falls back to snake_case scope_kind/scope_value", () => {
      expect(normalizeRuleScope({ scope_kind: "edge_fleet", scope_value: "fleet-1" })).toEqual({
        kind: RuleScopeKind.edge_fleet,
        value: "fleet-1",
      });
    });
    it("uses tenant_id when present", () => {
      expect(normalizeRuleScope({ tenant_id: "acme" })).toEqual({
        kind: RuleScopeKind.tenant,
        value: "acme",
      });
    });
    it("uses match.tenants[0] for YAML-derived rows", () => {
      expect(normalizeRuleScope({ match: { tenants: ["acme", "umbrella"] } })).toEqual({
        kind: RuleScopeKind.tenant,
        value: "acme",
      });
    });
    it("ignores wildcard tenant entries", () => {
      expect(normalizeRuleScope({ tenant_id: "*" })).toEqual({ kind: RuleScopeKind.global });
    });
    it("falls back to global when nothing matches", () => {
      expect(normalizeRuleScope({})).toEqual({ kind: RuleScopeKind.global });
    });
  });

  describe("normalizeRuleStatus", () => {
    it("returns explicit status when in the enum", () => {
      expect(normalizeRuleStatus({ status: "published" })).toBe(RuleStatus.published);
    });
    it("maps enabled:true to published", () => {
      expect(normalizeRuleStatus({ enabled: true })).toBe(RuleStatus.published);
    });
    it("maps enabled:false to deprecated", () => {
      expect(normalizeRuleStatus({ enabled: false })).toBe(RuleStatus.deprecated);
    });
    it("defaults to draft when both status and enabled are missing", () => {
      expect(normalizeRuleStatus({})).toBe(RuleStatus.draft);
    });
    it("ignores out-of-enum status strings", () => {
      expect(normalizeRuleStatus({ status: "active" })).toBe(RuleStatus.draft);
    });
  });

  describe("normalizeRule", () => {
    it("returns null for unsalvageable rows (no id)", () => {
      expect(normalizeRule({ name: "anonymous" })).toBeNull();
      expect(normalizeRule(null)).toBeNull();
      expect(normalizeRule("string-row")).toBeNull();
      expect(normalizeRule(42)).toBeNull();
    });
    it("uses id as name fallback when name is absent", () => {
      const rule = normalizeRule({ id: "rule-x" });
      expect(rule?.name).toBe("rule-x");
    });
    it("synthesizes audit.created_at from source.installed_at as final fallback", () => {
      const rule = normalizeRule({
        id: "rule-x",
        source: { installed_at: "2026-01-01T00:00:00Z" },
      });
      expect(rule?.audit.created_at).toBe("2026-01-01T00:00:00Z");
    });
    it("falls back decide to {type:'allow'} when decision/action absent", () => {
      const rule = normalizeRule({ id: "rule-x" });
      expect(rule?.decide).toEqual({ type: "allow" });
    });
    it("derives decide.type from legacy decision string", () => {
      const rule = normalizeRule({ id: "rule-x", decision: "DENY" });
      expect(rule?.decide).toEqual({ type: "deny" });
    });
    it("preserves match by mapping legacy conditions", () => {
      const rule = normalizeRule({ id: "rule-x", conditions: { topic: "foo" } });
      expect(rule?.match).toEqual({ topic: "foo" });
    });
    it("never throws on shapes with non-string id", () => {
      expect(normalizeRule({ id: 123 })).toBeNull();
    });
  });
});
