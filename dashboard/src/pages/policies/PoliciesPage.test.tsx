import { describe, it, expect } from "vitest";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderWithProviders } from "../../test-utils/render";
import PoliciesPage from "./PoliciesPage";
import BundlesPage from "./BundlesPage";
import DecisionsPage from "./DecisionsPage";

describe("Policy Studio foundation page shells", () => {
  it("PoliciesPage renders the canonical PageHeader title", () => {
    const { getByText } = renderWithProviders(<PoliciesPage />);
    expect(getByText("Policy Rules")).toBeTruthy();
    expect(
      getByText("Author and manage rules across job + edge surfaces"),
    ).toBeTruthy();
    expect(getByText("Rules surface coming online")).toBeTruthy();
  });

  it("BundlesPage renders the canonical PageHeader title + empty state", async () => {
    // Dashboard 5 step 4a evolved BundlesPage from a static shell into the
    // filter+DataTable list. The empty-state text now reflects the
    // unified Bundle Studio. Header copy stays canonical.
    const { findByText, getByText } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <BundlesPage />
      </NuqsTestingAdapter>,
    );
    expect(getByText("Policy Bundles")).toBeTruthy();
    expect(getByText("Group rules + deploy to scopes")).toBeTruthy();
    expect(await findByText("No bundles yet")).toBeTruthy();
  });

  it("DecisionsPage renders the canonical PageHeader title", () => {
    const { getByText } = renderWithProviders(<DecisionsPage />);
    expect(getByText("Policy Decisions")).toBeTruthy();
    expect(getByText("Live stream of policy outcomes")).toBeTruthy();
    expect(getByText("Decisions stream coming online")).toBeTruthy();
  });
});
