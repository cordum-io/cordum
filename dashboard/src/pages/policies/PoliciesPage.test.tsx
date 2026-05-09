import { describe, it, expect } from "vitest";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderWithProviders } from "../../test-utils/render";
import PoliciesPage from "./PoliciesPage";
import BundlesPage from "./BundlesPage";
import DecisionsPage from "./DecisionsPage";

describe("Policy Studio foundation page shells", () => {
  it("PoliciesPage renders the canonical PageHeader title (axe-clean on initial render)", async () => {
    const { getByText } = await renderWithProviders(<PoliciesPage />, {
      runAxe: true,
    });
    expect(getByText("Policy Rules")).toBeTruthy();
    expect(
      getByText("Author and manage rules across job + edge surfaces"),
    ).toBeTruthy();
    expect(getByText("Rules surface coming online")).toBeTruthy();
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
