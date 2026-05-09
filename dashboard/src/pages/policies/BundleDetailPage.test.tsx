import { beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { Route, Routes } from "react-router-dom";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import BundleDetailPage from "./BundleDetailPage";

const SAMPLE_BUNDLE = {
  id: "b-acme-prod",
  name: "ACME prod",
  rule_ids: ["r-1", "r-2", "r-3"],
  scope_binding: { kind: "tenant", value: "acme" },
  versions: [
    { version: "v1", rule_snapshot: [], deployed_at: "2026-05-01T12:00:00Z" },
    { version: "v2", rule_snapshot: [], deployed_at: "2026-05-08T09:30:00Z" },
  ],
};

const SAMPLE_VERSIONS = {
  items: [
    { version: "v1", rule_snapshot: [], deployed_at: "2026-05-01T12:00:00Z" },
    { version: "v2", rule_snapshot: [], deployed_at: "2026-05-08T09:30:00Z" },
  ],
};

function HarnessWith(search: string) {
  return (
    <NuqsTestingAdapter searchParams={search}>
      <Routes>
        <Route path="/policies/bundles/:id" element={<BundleDetailPage />} />
      </Routes>
    </NuqsTestingAdapter>
  );
}

beforeEach(() => {
  server.resetHandlers();
});

describe("BundleDetailPage", () => {
  it("renders the PageHeader with bundle name + Deployed status badge", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
        HttpResponse.json({ ...SAMPLE_BUNDLE, id: String(params.id) }),
      ),
      http.get("*/api/v1/policy/bundles/:id/versions", () =>
        HttpResponse.json(SAMPLE_VERSIONS),
      ),
    );
    renderWithProviders(HarnessWith(""), {
      initialEntries: ["/policies/bundles/b-acme-prod"],
    });

    expect(await screen.findByRole("heading", { name: /acme prod/i })).toBeTruthy();
    expect(screen.getByText(/scope: tenant:acme/i)).toBeTruthy();
    expect(screen.getByText(/^Deployed$/i)).toBeTruthy();
  });

  it("renders all 4 tabs with counts and defaults to the Rules tab", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
        HttpResponse.json({ ...SAMPLE_BUNDLE, id: String(params.id) }),
      ),
      http.get("*/api/v1/policy/bundles/:id/versions", () =>
        HttpResponse.json(SAMPLE_VERSIONS),
      ),
    );
    renderWithProviders(HarnessWith(""), {
      initialEntries: ["/policies/bundles/b-acme-prod"],
    });

    const rulesTab = await screen.findByRole("tab", { name: /^Rules$/i });
    expect(rulesTab).toBeTruthy();
    expect(rulesTab.getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tab", { name: /^Versions$/i })).toBeTruthy();
    expect(screen.getByRole("tab", { name: /^Deployments$/i })).toBeTruthy();
    expect(screen.getByRole("tab", { name: /^Diff$/i })).toBeTruthy();

    // Rules tab renders the rule_ids list (3 items from SAMPLE_BUNDLE).
    await waitFor(() => {
      expect(screen.getByText("r-1")).toBeTruthy();
    });
  });

  it("clicking the Versions tab swaps the rendered panel + activates aria-selected", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id", ({ params }) =>
        HttpResponse.json({ ...SAMPLE_BUNDLE, id: String(params.id) }),
      ),
      http.get("*/api/v1/policy/bundles/:id/versions", () =>
        HttpResponse.json(SAMPLE_VERSIONS),
      ),
    );
    renderWithProviders(HarnessWith(""), {
      initialEntries: ["/policies/bundles/b-acme-prod"],
    });

    const versionsTab = await screen.findByRole("tab", { name: /^Versions$/i });
    fireEvent.click(versionsTab);

    // Wait for the Tabs primitive to rerender with aria-selected on Versions
    // + the lazy-loaded BundleVersionsTab to mount and reveal v1/v2.
    await waitFor(() => {
      expect(versionsTab.getAttribute("aria-selected")).toBe("true");
    });
    // The Versions tab renders v1/v2 both as the row label AND as
    // <option> values in the Compare-with picker — use getAllByText
    // and assert the row labels are present.
    await waitFor(
      () => {
        expect(screen.getAllByText("v2").length).toBeGreaterThan(0);
        expect(screen.getAllByText("v1").length).toBeGreaterThan(0);
      },
      { timeout: 5_000 },
    );
    // Newest-first ordering — pick the row labels (the first match for
    // each version is the row label, not the <option>).
    const v2Row = screen.getAllByText("v2")[0];
    const v1Row = screen.getAllByText("v1")[0];
    expect(
      v2Row.compareDocumentPosition(v1Row) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // "Latest" pill on v2 (newest).
    expect(screen.getByText(/^Latest$/i)).toBeTruthy();
  });

  it("renders empty-rules state when the bundle has no rule_ids", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles/:id", () =>
        HttpResponse.json({
          id: "b-empty",
          name: "Empty bundle",
          rule_ids: [],
          scope_binding: { kind: "global" },
          versions: [],
        }),
      ),
    );
    renderWithProviders(HarnessWith(""), {
      initialEntries: ["/policies/bundles/b-empty"],
    });

    expect(await screen.findByText(/no rules in this bundle/i)).toBeTruthy();
    // Draft status (no versions).
    expect(screen.getByText(/^Draft$/i)).toBeTruthy();
  });
});
