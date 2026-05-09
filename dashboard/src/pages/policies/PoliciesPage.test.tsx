import { act } from "react";
import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import type { Rule } from "@/api/generated/model/rule";
import { RuleScopeKind } from "@/api/generated/model/ruleScopeKind";
import { RuleStatus } from "@/api/generated/model/ruleStatus";
import { RuleType } from "@/api/generated/model/ruleType";
import { fireEvent, renderWithProviders, screen, waitFor } from "../../test-utils/render";
import { server } from "../../test-utils/msw";
import PoliciesPage from "./PoliciesPage";
import BundlesPage from "./BundlesPage";
import DecisionsPage from "./DecisionsPage";

type RuleFixture = Rule & { firing_last_7d?: number[] };

function makeRule(index: number, overrides: Partial<RuleFixture> = {}): RuleFixture {
  const suffix = String(index).padStart(3, "0");
  return {
    id: overrides.id ?? `rule-${suffix}`,
    name: overrides.name ?? `Rule ${suffix}`,
    type: overrides.type ?? RuleType.input,
    scope: overrides.scope ?? { kind: RuleScopeKind.tenant, value: `tenant-${suffix}` },
    status: overrides.status ?? RuleStatus.published,
    version: overrides.version ?? "v1",
    audit: overrides.audit ?? {
      created_at: "2026-05-01T00:00:00Z",
      created_by: "policy-admin",
      updated_at: "2026-05-09T09:00:00Z",
      updated_by: "policy-admin",
    },
    match: overrides.match ?? { pattern: `pattern-${suffix}` },
    decide: overrides.decide ?? { type: "allow" },
    description: overrides.description ?? "MSW-backed unified rule fixture.",
    firing_last_7d: overrides.firing_last_7d ?? [0, 1, 0, 1, 0, 1, 0],
  };
}

function mockRulesResponse(
  rules: RuleFixture[],
  onRequest?: (url: URL) => void,
): void {
  server.use(
    http.get("*/api/v1/policy/rules", ({ request }) => {
      onRequest?.(new URL(request.url));
      return HttpResponse.json({ items: rules, total: rules.length });
    }),
  );
}

function renderPoliciesPage(searchParams = "") {
  return renderWithProviders(
    <NuqsTestingAdapter searchParams={searchParams}>
      <PoliciesPage />
    </NuqsTestingAdapter>,
    { initialEntries: ["/policies"] },
  );
}

describe("Policy Studio foundation page shells", () => {
  it("PoliciesPage renders the canonical PageHeader title and empty table state (axe-clean on initial render)", async () => {
    const { findByText, getByText } = await renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <PoliciesPage />
      </NuqsTestingAdapter>,
      { runAxe: true },
    );
    expect(getByText("Policy Rules")).toBeTruthy();
    expect(
      getByText("Author and manage rules across job + edge surfaces"),
    ).toBeTruthy();
    expect(await findByText("No rules yet")).toBeTruthy();
  });

  it("PoliciesPage renders the filter bar and DataTable row from the MSW rules response", async () => {
    mockRulesResponse([
      makeRule(1, {
        id: "rule-input-pii",
        name: "PII ingress guard",
        scope: { kind: RuleScopeKind.tenant, value: "acme" },
        firing_last_7d: [0, 2, 1, 0, 3, 4, 2],
      }),
    ]);

    renderPoliciesPage();

    expect(screen.getByLabelText("Filter rules by type")).toBeTruthy();
    expect(screen.getByLabelText("Filter rules by status")).toBeTruthy();
    expect(screen.getByLabelText("Filter rules by scope")).toBeTruthy();
    expect(screen.getByLabelText("Search rules")).toBeTruthy();
    expect(screen.getByRole("button", { name: /new rule/i })).toBeTruthy();
    expect(await screen.findByRole("link", { name: /pii ingress guard/i })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: /last 7d/i })).toBeTruthy();
    expect(screen.getByText("tenant:acme")).toBeTruthy();
    expect(screen.getAllByText("published").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByTestId("rule-type-icon-input")).toBeTruthy();
    expect(screen.getByLabelText("12 firings over the last 7 days")).toBeTruthy();
  });

  it("PoliciesPage roundtrips type, scope, and status URL filters into the rules-list request", async () => {
    const requests: URL[] = [];
    mockRulesResponse(
      [makeRule(1, { name: "Authority rule", scope: { kind: RuleScopeKind.tenant, value: "acme" } })],
      (url) => requests.push(url),
    );

    renderPoliciesPage("?type=input&scope=tenant%3Aacme&status=published");

    expect((screen.getByLabelText("Filter rules by type") as HTMLSelectElement).value).toBe("input");
    expect((screen.getByLabelText("Filter rules by scope") as HTMLInputElement).value).toBe("tenant:acme");
    expect((screen.getByLabelText("Filter rules by status") as HTMLSelectElement).value).toBe("published");
    expect(await screen.findByRole("link", { name: /authority rule/i })).toBeTruthy();
    await waitFor(() => {
      const latest = requests[requests.length - 1];
      expect(latest?.searchParams.get("type")).toBe("input");
      expect(latest?.searchParams.get("scope")).toBe("tenant:acme");
      expect(latest?.searchParams.get("status")).toBe("published");
    });
  });

  it("PoliciesPage shows the template CTA empty state when MSW returns no filtered matches", async () => {
    mockRulesResponse([]);

    renderPoliciesPage("?status=published");

    expect(await screen.findByText("No rules match these filters")).toBeTruthy();
    const templateLink = screen.getByRole("link", { name: /use a template/i });
    expect(templateLink.getAttribute("href")).toBe("/policies?templates=1");
  });

  it("PoliciesPage virtualizes the DataTable when MSW returns more than 100 rules", async () => {
    const rules = Array.from({ length: 150 }, (_, index) => makeRule(index + 1));
    mockRulesResponse(rules);

    const { container } = renderPoliciesPage();

    await waitFor(() => {
      expect(container.querySelector('[data-virtualized="true"]')).toBeTruthy();
    });
    const virtualizedTable = container.querySelector('[data-virtualized="true"]');
    expect(virtualizedTable).toBeTruthy();
    // jsdom has no real scroll viewport, so TanStack Virtual may report
    // zero visible rows. The contract we need to lock is that the
    // virtualized path is active and the DOM stays bounded below 50 rows.
    const renderedRows = virtualizedTable!.querySelectorAll("tbody tr[data-index]");
    expect(renderedRows.length).toBeLessThan(50);
  });

  it("PoliciesPage renders each RuleType icon variant from the rules-list response", async () => {
    mockRulesResponse([
      makeRule(1, { name: "Input guard", type: RuleType.input }),
      makeRule(2, { name: "Output guard", type: RuleType.output }),
      makeRule(3, { name: "Velocity guard", type: RuleType.velocity }),
      makeRule(4, { name: "Edge guard", type: RuleType.edge }),
    ]);

    renderPoliciesPage();

    expect(await screen.findByTestId("rule-type-icon-input")).toBeTruthy();
    expect(screen.getByTestId("rule-type-icon-output")).toBeTruthy();
    expect(screen.getByTestId("rule-type-icon-velocity")).toBeTruthy();
    expect(screen.getByTestId("rule-type-icon-edge")).toBeTruthy();
  });

  it("PoliciesPage debounces search typing by 300ms before refetching rules", async () => {
    const requestedSearches: Array<string | null> = [];
    mockRulesResponse([], (url) => requestedSearches.push(url.searchParams.get("search")));
    renderPoliciesPage();

    await waitFor(() => {
      expect(requestedSearches.length).toBeGreaterThan(0);
    });
    const initialRequestCount = requestedSearches.length;

    vi.useFakeTimers();
    try {
      fireEvent.change(screen.getByLabelText("Search rules"), {
        target: { value: "pii" },
      });
      await act(async () => {
        await Promise.resolve();
      });
      expect(requestedSearches).toHaveLength(initialRequestCount);

      await act(async () => {
        vi.advanceTimersByTime(299);
        await Promise.resolve();
      });
      expect(requestedSearches).toHaveLength(initialRequestCount);

      await act(async () => {
        vi.advanceTimersByTime(1);
        await Promise.resolve();
      });
    } finally {
      vi.useRealTimers();
    }

    await waitFor(() => {
      expect(requestedSearches).toHaveLength(initialRequestCount + 1);
    });
    expect(requestedSearches[requestedSearches.length - 1]).toBe("pii");
  });

  it("BundlesPage renders the canonical PageHeader title + empty state (axe-clean on initial render)", async () => {
    // Dashboard 5 step 4a evolved BundlesPage from a static shell into the
    // filter+DataTable list. The empty-state text now reflects the
    // unified Bundle Studio. Header copy stays canonical. Phase 5a's
    // strict axe opt-in is applied to the synchronous-render tree;
    // findByText handles the async empty-state text.
    const { findByText, getByText } = await renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <BundlesPage />
      </NuqsTestingAdapter>,
      { runAxe: true },
    );
    expect(getByText("Policy Bundles")).toBeTruthy();
    expect(getByText("Group rules + deploy to scopes")).toBeTruthy();
    expect(await findByText("No bundles yet")).toBeTruthy();
  });

  it("DecisionsPage renders the canonical PageHeader title (axe-clean on initial render)", async () => {
    const { getByText } = await renderWithProviders(<DecisionsPage />, {
      runAxe: true,
    });
    expect(getByText("Policy Decisions")).toBeTruthy();
    expect(getByText("Live stream of policy outcomes")).toBeTruthy();
    expect(getByText("Decisions stream coming online")).toBeTruthy();
  });
});
