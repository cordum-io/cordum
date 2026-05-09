import { describe, it, expect } from "vitest";
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

  it("BundlesPage renders the canonical PageHeader title", () => {
    const { getByText } = renderWithProviders(<BundlesPage />);
    expect(getByText("Policy Bundles")).toBeTruthy();
    expect(getByText("Group rules + deploy to scopes")).toBeTruthy();
    expect(getByText("Bundles surface coming online")).toBeTruthy();
  });

  it("DecisionsPage renders the canonical PageHeader title", () => {
    const { getByText } = renderWithProviders(<DecisionsPage />);
    expect(getByText("Policy Decisions")).toBeTruthy();
    expect(getByText("Live stream of policy outcomes")).toBeTruthy();
    expect(getByText("Decisions stream coming online")).toBeTruthy();
  });
});
