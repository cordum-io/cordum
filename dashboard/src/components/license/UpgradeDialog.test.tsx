import { describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test-utils/render";
import { UpgradeDialog } from "./UpgradeDialog";

describe("UpgradeDialog", () => {
  it("renders the feature label and current plan in the body copy", () => {
    renderWithProviders(
      <UpgradeDialog
        open
        onClose={() => {}}
        feature="SSO & SAML"
        currentPlan="community"
      />,
    );

    expect(screen.getByRole("dialog")).toBeTruthy();
    const dialog = screen.getByRole("dialog");
    expect(dialog.textContent).toContain("SSO & SAML requires an upgraded plan");
    expect(dialog.textContent).toContain("community");
  });

  it("falls back to a generic plan label when currentPlan is empty", () => {
    renderWithProviders(
      <UpgradeDialog open onClose={() => {}} feature="SCIM" currentPlan="" />,
    );
    expect(screen.getByRole("dialog").textContent).toContain(
      "your current plan",
    );
  });

  it("calls onClose when Cancel/Maybe later is clicked", () => {
    const onClose = vi.fn();
    renderWithProviders(
      <UpgradeDialog open onClose={onClose} feature="SCIM" currentPlan="team" />,
    );

    const cancelButton = Array.from(
      screen.getByRole("dialog").querySelectorAll("button"),
    ).find((b) => b.textContent?.includes("Maybe later"));
    expect(cancelButton).toBeTruthy();
    fireEvent.click(cancelButton!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("opens the pricing URL in a new tab when 'View pricing' is clicked", () => {
    const onClose = vi.fn();
    const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
    try {
      renderWithProviders(
        <UpgradeDialog
          open
          onClose={onClose}
          feature="Audit Export"
          currentPlan="team"
          pricingUrl="https://example.test/pricing"
        />,
      );

      const viewPricing = Array.from(
        screen.getByRole("dialog").querySelectorAll("button"),
      ).find((b) => b.textContent?.includes("View pricing"));
      expect(viewPricing).toBeTruthy();
      fireEvent.click(viewPricing!);

      expect(openSpy).toHaveBeenCalledWith(
        "https://example.test/pricing",
        "_blank",
        "noopener,noreferrer",
      );
      // Confirm closes the dialog after navigating away.
      expect(onClose).toHaveBeenCalledTimes(1);
    } finally {
      openSpy.mockRestore();
    }
  });
});
