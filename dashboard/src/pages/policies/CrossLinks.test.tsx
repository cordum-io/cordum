import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import PoliciesPage from "./PoliciesPage";
import BundleRulesTab from "./BundleRulesTab";

// D10a (task-ce11ca57 split per architect msg-f38f9aff) covers the 4
// cross-links whose source AND destination both exist on the dashboard
// branch today: PoliciesPage Last-7d → Decisions, BundleRulesTab
// "Add rule" → editor pre-bound, TenantDetail "Active policies" →
// Bundles?scope=, AuditLog "View related decisions" → Decisions. The
// 6 D8/D9-blocked cross-links land in D10b (filed once D10a → REVIEW).
//
// These tests assert the canonical URL contract for each link — that's
// the load-bearing contract per spec § "Cross-links are the load-bearing
// wall". Destination surfaces (DecisionsPage stub, etc.) honor the URL
// params once they ship.

describe("D10a cross-links", () => {
  it("PoliciesPage Last-7d sparkline links to /policies/decisions?rule=<id>", async () => {
    server.use(
      http.get("*/api/v1/policy/rules", () =>
        HttpResponse.json({
          rules: [
            {
              id: "rule-firing",
              name: "Block secrets",
              type: RuleType.input,
              scope: { kind: RuleScopeKind.global },
              status: RuleStatus.published,
              version: "v1",
              audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
              match: {},
              decide: { type: "deny" },
              firing_last_7d: [1, 2, 3, 5, 8, 13, 21],
              updated_at: "2026-05-09T00:00:00Z",
            },
          ],
          total: 1,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <PoliciesPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies"] },
    );

    const link = await screen.findByLabelText(
      /View decisions: 53 firings/i,
    );
    expect(link.getAttribute("href")).toBe("/policies/decisions?rule=rule-firing");
    expect(link.getAttribute("data-row-action")).toBe("cross-link-decisions");
  });

  it("PoliciesPage renders an em-dash (no link) when firing_last_7d is null", async () => {
    server.use(
      http.get("*/api/v1/policy/rules", () =>
        HttpResponse.json({
          rules: [
            {
              id: "rule-quiet",
              name: "Quiet rule",
              type: RuleType.input,
              scope: { kind: RuleScopeKind.global },
              status: RuleStatus.draft,
              version: "v1",
              audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
              match: {},
              decide: { type: "allow" },
              // No firing_last_7d
              updated_at: "2026-05-09T00:00:00Z",
            },
          ],
          total: 1,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <PoliciesPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies"] },
    );

    await waitFor(() =>
      expect(screen.getByText("Quiet rule")).not.toBeNull(),
    );
    // No "View decisions" link because the rule never fired (last7dSeries === null).
    expect(
      screen.queryByLabelText(/View decisions:/i),
    ).toBeNull();
  });

  it("BundleRulesTab Add rule button links to /policies pre-bound to bundleId (empty bundle)", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
        HttpResponse.json({
          id: String(params.id),
          name: `bundle-${String(params.id)}`,
          rule_ids: [],
          scope_binding: { kind: "global" },
          versions: [],
        }),
      ),
    );

    renderWithProviders(<BundleRulesTab bundleId="b-empty" />);

    const link = await screen.findByLabelText(/Add a rule to bundle b-empty/i);
    expect(link.getAttribute("href")).toBe(
      "/policies?rule=new&open=editor&type=input&bundle=b-empty",
    );
    expect(link.getAttribute("data-row-action")).toBe("cross-link-add-rule");
  });

  it("BundleRulesTab Add rule button also renders below a non-empty rule list", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
        HttpResponse.json({
          id: String(params.id),
          name: `bundle-${String(params.id)}`,
          rule_ids: ["rule-1", "rule-2"],
          scope_binding: { kind: "global" },
          versions: [],
        }),
      ),
    );

    renderWithProviders(<BundleRulesTab bundleId="b-full" />);

    // Rule list rendered.
    await waitFor(() => expect(screen.getByText("rule-1")).not.toBeNull());
    expect(screen.getByText("rule-2")).not.toBeNull();
    // Add rule link still reachable (lets the operator add more).
    const link = screen.getByLabelText(/Add another rule to bundle b-full/i);
    expect(link.getAttribute("href")).toBe(
      "/policies?rule=new&open=editor&type=input&bundle=b-full",
    );
  });

  it("clicking the Last-7d sparkline navigates to the Decisions URL", async () => {
    // Verifies the link's onClick surface — that is, react-router-dom's
    // Link component fires the SPA navigation rather than producing a
    // full-page reload. We render PoliciesPage at /policies and assert
    // that fireEvent.click changes the URL to /policies/decisions?rule=…
    let observedSearch = "";
    server.use(
      http.get("*/api/v1/policy/rules", () =>
        HttpResponse.json({
          rules: [
            {
              id: "rule-nav",
              name: "Nav target",
              type: RuleType.input,
              scope: { kind: RuleScopeKind.global },
              status: RuleStatus.published,
              version: "v1",
              audit: { created_at: "2026-04-01T00:00:00Z", created_by: "alice" },
              match: {},
              decide: { type: "deny" },
              firing_last_7d: [1, 1, 1],
            },
          ],
          total: 1,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <PoliciesPage />
        <LocationRecorder onChange={(s) => (observedSearch = s)} />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies"] },
    );

    const link = await screen.findByLabelText(/View decisions: 3 firings/i);
    fireEvent.click(link, { button: 0 });
    await waitFor(() => expect(observedSearch).toContain("rule=rule-nav"));
  });
});

import { useLocation } from "react-router-dom";
import { useEffect } from "react";

function LocationRecorder({ onChange }: { onChange: (search: string) => void }) {
  const location = useLocation();
  useEffect(() => {
    onChange(location.search);
  }, [location.search, onChange]);
  return null;
}
