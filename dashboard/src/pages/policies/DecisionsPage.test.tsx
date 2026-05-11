import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { DecisionSource } from "@/api/generated/model/decisionSource";
import { DecisionType } from "@/api/generated/model/decisionType";
import type { Decision } from "@/api/generated/model/decision";
import DecisionsPage from "./DecisionsPage";

function makeDecision(index: number, overrides: Partial<Decision> = {}): Decision {
  return {
    source: DecisionSource.job,
    rule_id: `rule-${index}`,
    bundle_id: `bundle-${index % 2 === 0 ? "core" : "edge"}`,
    bundle_version: "v1",
    type: DecisionType.allow,
    timestamp: new Date(Date.now() - index * 60_000).toISOString(),
    audit_hash: `hash-${index}`,
    ...overrides,
  };
}

describe("DecisionsPage (D8a — filter bar + paged table)", () => {
  it("renders filter bar + table rows from /api/v1/policy/decisions", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: [
            makeDecision(1, { type: DecisionType.deny, source: DecisionSource.edge }),
            makeDecision(2, { type: DecisionType.allow }),
            makeDecision(3, { type: DecisionType.require_human }),
          ],
          has_more: false,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );

    expect(await screen.findByText("Policy Decisions")).not.toBeNull();
    // Filter bar renders Time/Decision/Source affordances.
    expect(screen.getByLabelText("Time range")).not.toBeNull();
    expect(screen.getByLabelText("Decision filter")).not.toBeNull();
    expect(screen.getByLabelText("Source filter")).not.toBeNull();
    // Rows render — assert one rule cell from each fixture.
    await waitFor(() => expect(screen.getByText("rule-1")).not.toBeNull());
    expect(screen.getByText("rule-2")).not.toBeNull();
    expect(screen.getByText("rule-3")).not.toBeNull();
  });

  it("Source badge differentiates job vs edge rows (DoD #3)", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: [
            makeDecision(1, { source: DecisionSource.job }),
            makeDecision(2, { source: DecisionSource.edge }),
          ],
          has_more: false,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );

    await waitFor(() => expect(screen.getByText("rule-1")).not.toBeNull());
    // Both badges render with their literal source value.
    const badges = screen.getAllByText(/^(job|edge)$/);
    expect(badges.some((el) => el.textContent === "job")).toBe(true);
    expect(badges.some((el) => el.textContent === "edge")).toBe(true);
  });

  it("rule cell links to /policies?rule=<id>&open=editor (D10a cross-link contract)", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: [makeDecision(1)],
          has_more: false,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );

    const link = await screen.findByLabelText("Open rule rule-1 in editor");
    expect(link.getAttribute("href")).toBe(
      "/policies?rule=rule-1&open=editor",
    );
    expect(link.getAttribute("data-row-action")).toBe(
      "cross-link-decisions-rule",
    );
  });

  it("bundle cell links to /policies/bundles/<id>?tab=versions&v=<n>", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: [
            makeDecision(1, { bundle_id: "bundle-x", bundle_version: "v3" }),
          ],
          has_more: false,
        }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );

    const link = await screen.findByLabelText("Open bundle bundle-x:v3");
    expect(link.getAttribute("href")).toBe(
      "/policies/bundles/bundle-x?tab=versions&v=v3",
    );
    expect(link.getAttribute("data-row-action")).toBe(
      "cross-link-decisions-bundle",
    );
  });

  it("renders empty-state when /policy/decisions returns no items", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({ items: [], has_more: false }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );

    await waitFor(() =>
      expect(
        screen.getByText(/No decisions match these filters/i),
      ).not.toBeNull(),
    );
  });

  it("filter bar 'Decision' selection toggles visibility into URL state (mock-adapter assert)", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({ items: [], has_more: false }),
      ),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );

    const select = (await screen.findByLabelText(
      "Decision filter",
    )) as HTMLSelectElement;
    // Default — no decision filter.
    expect(select.value).toBe("");

    fireEvent.change(select, { target: { value: DecisionType.deny } });
    await waitFor(() => expect(select.value).toBe(DecisionType.deny));
  });
});
