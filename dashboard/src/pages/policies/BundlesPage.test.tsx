import { beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { fireEvent, renderWithProviders, screen } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import BundlesPage from "./BundlesPage";

// MSW default handler returns `{items: [], total: 0}` per
// `src/test-utils/handlers.ts` so the empty-state path is the default.
// Tests that need populated data override with `server.use(...)`.

const SAMPLE_BUNDLE = {
  id: "b-acme-prod",
  name: "ACME prod",
  rule_ids: ["r-1", "r-2"],
  scope_binding: { kind: "tenant", value: "acme" },
  versions: [
    {
      version: "v1",
      rule_snapshot: [],
      deployed_at: "2026-05-01T12:00:00Z",
    },
    {
      version: "v2",
      rule_snapshot: [],
      deployed_at: "2026-05-08T09:30:00Z",
    },
  ],
};

const DRAFT_BUNDLE = {
  id: "b-draft-only",
  name: "Draft sandbox",
  rule_ids: [],
  scope_binding: { kind: "global" },
  versions: [],
};

beforeEach(() => {
  // Reset to handlers.ts default before each test for deterministic state.
  server.resetHandlers();
});

describe("BundlesPage", () => {
  it("renders the empty-state CTA when no bundles exist", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <BundlesPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/bundles"] },
    );
    expect(
      await screen.findByRole("heading", { name: /policy bundles/i }),
    ).toBeTruthy();
    expect(await screen.findByText(/no bundles yet/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /create your first bundle/i })).toBeTruthy();
  });

  it("renders bundle rows + status dots when the list returns items", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles", () =>
        HttpResponse.json({
          items: [SAMPLE_BUNDLE, DRAFT_BUNDLE],
          total: 2,
        }),
      ),
    );
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <BundlesPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/bundles"] },
    );

    expect(await screen.findByRole("link", { name: /acme prod/i })).toBeTruthy();
    expect(screen.getByRole("link", { name: /draft sandbox/i })).toBeTruthy();
    expect(screen.getByLabelText(/^deployed$/i)).toBeTruthy();
    expect(screen.getByLabelText(/draft \(never deployed\)/i)).toBeTruthy();
    expect(screen.getByText("tenant:acme")).toBeTruthy();
    expect(screen.getByText("global")).toBeTruthy();
  });

  it("mirrors the search input into the URL via nuqs (?search=…)", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <BundlesPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/bundles"] },
    );
    const search = await screen.findByLabelText(/search bundles by name/i);
    fireEvent.change(search, { target: { value: "acme" } });
    await screen.findByDisplayValue("acme");
  });

  it("shows the scope-filter active hint + Clear filters action when filters are set", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="?scope=tenant%3Aacme">
        <BundlesPage />
      </NuqsTestingAdapter>,
      { initialEntries: ["/policies/bundles?scope=tenant%3Aacme"] },
    );
    expect(
      await screen.findByText(/no bundles match the active filter/i),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: /clear filters/i })).toBeTruthy();
  });
});
