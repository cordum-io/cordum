import { beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse, ensureMswServerListening, server } from "@/test-utils/msw";
import { renderWithQueryClient } from "./__tests__/test-utils";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import type { Rule } from "@/api/generated/model/rule";
import { useRulesList, type RuleFilters } from "./useRulesList";

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
});
