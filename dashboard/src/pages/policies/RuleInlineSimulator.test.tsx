import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test-utils/msw";
import { renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { UNKNOWN_RULE_TYPE, type NormalizedRule } from "@/hooks/useRulesList";
import { RuleInlineSimulator } from "./RuleInlineSimulator";

const baseRule: NormalizedRule = {
  id: "rule-1",
  name: "Block secrets",
  type: RuleType.input,
  scope: { kind: RuleScopeKind.global },
  status: RuleStatus.draft,
  version: "v1",
  audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
  match: {},
  decide: { type: "deny" },
};

function mockAudit(items: Array<Record<string, unknown>>): void {
  server.use(
    http.get("*/api/v1/policy/audit", () =>
      HttpResponse.json({ items, total: items.length, has_more: false, offset: 0 }),
    ),
  );
}

function mockEvaluator(decisions: ReadonlyArray<string>): void {
  let i = 0;
  server.use(
    http.post("*/api/v1/policy/evaluate", () => {
      const next = decisions[i % decisions.length];
      i += 1;
      return HttpResponse.json({ decision: next });
    }),
  );
}

describe("RuleInlineSimulator", () => {
  it("renders the disabled state for an unknown rule type", async () => {
    const unknown = { ...baseRule, type: UNKNOWN_RULE_TYPE } as NormalizedRule;
    renderWithProviders(<RuleInlineSimulator draft={unknown} />);
    expect(
      screen.getByText(/Rule type unknown — pick a known type/i),
    ).not.toBeNull();
  });

  it("renders the loading state while audit is in flight", async () => {
    server.use(
      http.get("*/api/v1/policy/audit", async () => {
        await new Promise((r) => setTimeout(r, 200));
        return HttpResponse.json({ items: [], total: 0 });
      }),
    );
    renderWithProviders(<RuleInlineSimulator draft={baseRule} />);
    expect(screen.getByText(/Loading recent events/i)).not.toBeNull();
  });

  it("renders the empty state when audit returns no events", async () => {
    mockAudit([]);
    renderWithProviders(<RuleInlineSimulator draft={baseRule} />);
    await waitFor(() => {
      expect(
        screen.getByText(/No recent events for this rule type/i),
      ).not.toBeNull();
    });
  });

  it("renders the audit-error retry state when /policy/audit 5xx's", async () => {
    server.use(
      http.get("*/api/v1/policy/audit", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    renderWithProviders(<RuleInlineSimulator draft={baseRule} />);
    await waitFor(
      () => {
        expect(screen.getByText(/Couldn't load recent events/i)).not.toBeNull();
      },
      { timeout: 3000 },
    );
    expect(screen.getByRole("button", { name: /retry/i })).not.toBeNull();
  });

  it("evaluates a small batch end-to-end and renders bucket counts (job rule)", async () => {
    mockAudit([
      {
        id: "e-1",
        action: "policy.evaluate",
        actor_id: "agent-1",
        extra: { tenant_id: "acme", topic: "job.payments.transfer" },
        created_at: "2026-04-01T00:00:00Z",
      },
      {
        id: "e-2",
        action: "policy.evaluate",
        actor_id: "agent-2",
        extra: { tenant_id: "acme", topic: "job.email.send" },
        created_at: "2026-04-01T00:00:00Z",
      },
    ]);
    mockEvaluator(["allow", "deny"]);
    renderWithProviders(<RuleInlineSimulator draft={baseRule} />);
    await waitFor(
      () => {
        expect(screen.getByTestId("simulator-total")).not.toBeNull();
      },
      { timeout: 3000 },
    );
    const allowCell = screen.getByTestId("simulator-bucket-allow");
    const denyCell = screen.getByTestId("simulator-bucket-deny");
    expect(allowCell.textContent).toMatch(/1/);
    expect(denyCell.textContent).toMatch(/1/);
    // The deny outcome should appear in the matching list (non-allow,
    // non-error).
    expect(screen.getByTestId("simulator-match-deny")).not.toBeNull();
    expect(screen.getByTestId("simulator-elapsed-ms").textContent).toMatch(/ms/);
  });

  it("buckets evaluator 5xx as error and surfaces the count to the user", async () => {
    mockAudit([
      {
        id: "e-1",
        action: "policy.evaluate",
        actor_id: "agent-1",
        extra: { tenant_id: "acme", topic: "job.payments.transfer" },
        created_at: "2026-04-01T00:00:00Z",
      },
    ]);
    server.use(
      http.post("*/api/v1/policy/evaluate", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    renderWithProviders(<RuleInlineSimulator draft={baseRule} />);
    await waitFor(
      () => {
        expect(screen.getByTestId("simulator-bucket-error").textContent).toMatch(/1/);
      },
      { timeout: 3000 },
    );
    expect(screen.getByText(/1 failed to construct a context/i)).not.toBeNull();
  });
});
