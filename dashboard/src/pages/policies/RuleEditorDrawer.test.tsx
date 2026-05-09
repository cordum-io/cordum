import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { RuleEditorDrawer } from "./RuleEditorDrawer";

function renderDrawerAt(path: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/policies" element={<RuleEditorDrawer />} />
    </Routes>,
    { initialEntries: [path] },
  );
}

// Helper: respond to the real list endpoint with a fixture set. The drawer
// resolves existing rules from the list cache (populated by useRulesList);
// no `/policy/rules/:id` detail route exists in the current dashboard/core
// contract (cordum-api.yaml:2609 + gateway.go:1415), so tests must mock the
// list endpoint, not a fabricated detail one.
function mockRulesListEndpoint(rules: unknown[]): void {
  server.use(
    http.get("*/api/v1/policy/rules", () =>
      HttpResponse.json({ items: rules, total: rules.length }),
    ),
  );
}

describe("RuleEditorDrawer URL contract", () => {
  it("renders nothing when ?open=editor is absent", () => {
    renderDrawerAt("/policies");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("renders nothing when rule is missing even if open=editor is set", () => {
    renderDrawerAt("/policies?open=editor");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("renders the drawer for an existing rule sourced from the real list endpoint", async () => {
    mockRulesListEndpoint([
      {
        id: "rule-1",
        name: "Block secrets",
        type: RuleType.input,
        scope: { kind: RuleScopeKind.global },
        status: RuleStatus.published,
        version: "v1",
        audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
        match: {},
        decide: { type: "deny" },
      },
    ]);
    renderDrawerAt("/policies?rule=rule-1&open=editor");
    await waitFor(() => expect(screen.getByText("Block secrets")).not.toBeNull());
  });

  it("renders the drawer for an existing rule sourced from a legacy/snake_case list payload", async () => {
    // Reproduces the live-migration shape: backend returns legacy fields
    // (rule_type, tenant_id, action) and useRulesList normalizes them
    // into the unified Rule envelope. The drawer must source the rule
    // through that same list-cache path — never via a phantom detail GET.
    mockRulesListEndpoint([
      {
        id: "legacy-1",
        name: "Tenant guard (legacy)",
        rule_type: "input_rule",
        tenant_id: "acme",
        enabled: true,
        action: "DENY",
      },
    ]);
    renderDrawerAt("/policies?rule=legacy-1&open=editor");
    await waitFor(() =>
      expect(screen.getByText("Tenant guard (legacy)")).not.toBeNull(),
    );
  });

  it("renders the create-new state for ?rule=new&open=editor&type=input", async () => {
    renderDrawerAt(`/policies?rule=new&open=editor&type=${RuleType.input}`);
    await waitFor(() => expect(screen.getByText(/New input rule/i)).not.toBeNull());
  });

  it("renders the 'Pick a rule type' empty state when create-new lacks a valid type", async () => {
    renderDrawerAt("/policies?rule=new&open=editor");
    await waitFor(() =>
      expect(screen.getByText(/Pick a rule type to start/i)).not.toBeNull(),
    );
  });

  it("renders the not-found empty state for a rule id absent from the list", async () => {
    // The list endpoint exists and returns rows, but the requested id is
    // not among them — drawer renders the explicit not-found copy without
    // throwing on a phantom detail endpoint.
    mockRulesListEndpoint([
      {
        id: "rule-1",
        name: "Block secrets",
        type: RuleType.input,
        scope: { kind: RuleScopeKind.global },
        status: RuleStatus.published,
        version: "v1",
        audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
        match: {},
        decide: { type: "deny" },
      },
    ]);
    renderDrawerAt("/policies?rule=missing-rule&open=editor");
    await waitFor(() =>
      expect(screen.getByText(/doesn't exist or has been removed/i)).not.toBeNull(),
    );
  });

  it("renders the backend-error retry state when the rules list endpoint 5xx's", async () => {
    server.use(
      http.get("*/api/v1/policy/rules", () =>
        HttpResponse.json({ error: "internal" }, { status: 500 }),
      ),
    );
    renderDrawerAt("/policies?rule=boom&open=editor");
    await waitFor(() =>
      expect(screen.getByText(/Couldn't load this rule/i)).not.toBeNull(),
    );
    expect(screen.getByRole("button", { name: /retry/i })).not.toBeNull();
  });

  it("disables Save draft and surfaces the Phase 3E tooltip while the backend mutation is unwired", async () => {
    mockRulesListEndpoint([
      {
        id: "rule-2",
        name: "Edge tool guard",
        type: RuleType.edge,
        scope: { kind: RuleScopeKind.global },
        status: RuleStatus.draft,
        version: "v1",
        audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
        match: {},
        decide: { type: "deny" },
      },
    ]);
    renderDrawerAt("/policies?rule=rule-2&open=editor");
    const saveBtn = await screen.findByRole("button", {
      name: /save draft \(not yet enabled\)/i,
    });
    expect((saveBtn as HTMLButtonElement).disabled).toBe(true);
    expect(saveBtn.getAttribute("title")).toMatch(/Phase 3E/i);
  });

  it("preserves task-15537d13 hotfix safety: refuses to mount Monaco for an unknown rule type", async () => {
    mockRulesListEndpoint([
      {
        id: "unknown-row",
        name: "Legacy classifier",
        // No `type` field; useRulesList.normalizer assigns UNKNOWN_RULE_TYPE.
        scope: { kind: RuleScopeKind.global },
        status: RuleStatus.published,
        version: "v1",
        audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
      },
    ]);
    renderDrawerAt("/policies?rule=unknown-row&open=editor");
    await waitFor(() =>
      expect(screen.getByText(/Unknown rule type/i)).not.toBeNull(),
    );
    // The "editor cannot mount without a known schema" copy is the safety
    // contract preserving the hotfix; if a future change tries to coerce
    // unknown types into RuleType.input this assertion fails and signals
    // the regression at review time.
    expect(
      screen.getByText(/editor cannot mount without a known schema/i),
    ).not.toBeNull();
  });
});
