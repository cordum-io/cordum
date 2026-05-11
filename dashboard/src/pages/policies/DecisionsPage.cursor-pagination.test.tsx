import { describe, it, expect } from "vitest";
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
    bundle_id: "bundle-x",
    bundle_version: "v1",
    type: DecisionType.allow,
    timestamp: new Date(Date.UTC(2026, 4, 10, 12, 0, index)).toISOString(),
    audit_hash: `sha256:${String(index).padStart(4, "0")}`,
    ...overrides,
  };
}

// D8b adds cursor pagination to the Decisions table. The runner queries
// /api/v1/policy/decisions with `cursor=` and the response carries a
// `next_cursor` opaque token; clients pass it back to advance. The
// dashboard renders a "Load more" button when has_more=true; clicking it
// appends the next page to the existing rows and bumps the cursor.

describe("DecisionsPage (D8b — cursor pagination)", () => {
  it("renders 'Load more' when next_cursor is set + has_more=true", async () => {
    server.use(
      http.get("*/api/v1/policy/decisions", () =>
        HttpResponse.json({
          items: [makeDecision(1), makeDecision(2)],
          has_more: true,
          next_cursor: "cursor-page-2",
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
    expect(
      await screen.findByRole("button", { name: /load more/i }),
    ).not.toBeNull();
  });

  it("does NOT render 'Load more' when has_more=false", async () => {
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
    await waitFor(() => expect(screen.getByText("rule-1")).not.toBeNull());
    expect(screen.queryByRole("button", { name: /load more/i })).toBeNull();
  });

  it("clicking 'Load more' appends the next page rows", async () => {
    let pageCalls = 0;
    server.use(
      http.get("*/api/v1/policy/decisions", ({ request }) => {
        const url = new URL(request.url);
        const cursor = url.searchParams.get("cursor");
        pageCalls += 1;
        if (!cursor) {
          return HttpResponse.json({
            items: [makeDecision(1), makeDecision(2)],
            has_more: true,
            next_cursor: "cursor-page-2",
          });
        }
        if (cursor === "cursor-page-2") {
          return HttpResponse.json({
            items: [makeDecision(3), makeDecision(4)],
            has_more: false,
          });
        }
        return HttpResponse.json({ items: [], has_more: false });
      }),
    );
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/decisions"] },
    );
    await waitFor(() => expect(screen.getByText("rule-1")).not.toBeNull());
    expect(screen.queryByText("rule-3")).toBeNull();

    const loadMore = await screen.findByRole("button", { name: /load more/i });
    fireEvent.click(loadMore);

    await waitFor(() => expect(screen.getByText("rule-3")).not.toBeNull());
    // Page-1 rows are still rendered after appending page 2.
    expect(screen.getByText("rule-1")).not.toBeNull();
    expect(screen.getByText("rule-4")).not.toBeNull();
    expect(pageCalls).toBeGreaterThanOrEqual(2);
    // After the last page lands, the button is removed.
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /load more/i })).toBeNull(),
    );
  });
});
