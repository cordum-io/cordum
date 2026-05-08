/*
 * SettingsHubPage click-behavior test.
 *
 * Convention: dashboard/CLAUDE.md (page tests use renderWithProviders + MSW).
 * Decision record: dashboard/docs/adr/0001-page-test-providers.md.
 *
 * Cases:
 *   (a) Click on an unlocked card navigates to the card's path.
 *   (b) Click on an entitlement-locked card opens the upgrade dialog and
 *       does NOT navigate.
 */
import { beforeEach, describe, expect, it } from "vitest";
import { Route, Routes } from "react-router-dom";
import { http, HttpResponse } from "msw";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import SettingsHubPage from "./SettingsHubPage";

const ENTERPRISE_LICENSE = {
  plan: "enterprise",
  entitlements: {
    sso: true,
    saml: true,
    scim: true,
    audit_export: true,
    siem_export: true,
    legal_hold: true,
  },
  rights: null,
  license: null,
};

const COMMUNITY_LICENSE = {
  plan: "community",
  entitlements: {},
  rights: null,
  license: null,
};

beforeEach(() => {
  server.use(
    http.get("*/api/v1/license", () => HttpResponse.json(ENTERPRISE_LICENSE)),
  );
});

function HubHarness() {
  return (
    <Routes>
      <Route path="/settings" element={<SettingsHubPage />} />
      <Route
        path="/settings/config"
        element={<div data-testid="settings-config-stub" />}
      />
      <Route
        path="/settings/sso"
        element={<div data-testid="settings-sso-stub" />}
      />
    </Routes>
  );
}

function findCardByTitle(
  container: ParentNode,
  title: string,
): HTMLButtonElement | null {
  const heading = Array.from(container.querySelectorAll("h2")).find(
    (h) => h.textContent?.trim() === title,
  );
  return (heading?.closest("button") as HTMLButtonElement | null) ?? null;
}

describe("SettingsHubPage", () => {
  it("clicking an unlocked card navigates to its path", async () => {
    const { container } = renderWithProviders(<HubHarness />, {
      initialEntries: ["/settings"],
    });

    // Wait for the page to render its cards.
    const card = await waitFor(() => {
      const found = findCardByTitle(container, "System Config");
      expect(found).not.toBeNull();
      return found!;
    });

    fireEvent.click(card);

    await waitFor(() => {
      expect(
        container.querySelector('[data-testid="settings-config-stub"]'),
      ).not.toBeNull();
    });
    // No upgrade dialog should appear after a normal navigation click.
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  });

  it("clicking a locked card opens UpgradeDialog and does not navigate", async () => {
    server.use(
      http.get("*/api/v1/license", () => HttpResponse.json(COMMUNITY_LICENSE)),
    );
    const { container } = renderWithProviders(<HubHarness />, {
      initialEntries: ["/settings"],
    });

    // Wait for the locked-state evaluation to settle (data-locked attribute
    // resolves only after the license loads since we fail-open while loading).
    const ssoCard = await waitFor(() => {
      const found = findCardByTitle(container, "SSO & SAML");
      expect(found).not.toBeNull();
      expect(found!.getAttribute("data-locked")).toBe("true");
      return found!;
    });

    fireEvent.click(ssoCard);

    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain("SSO & SAML requires an upgraded plan");
    expect(dialog.textContent).toContain("community");
    // Navigation must not occur — the sso stub should not be in the DOM.
    expect(
      container.querySelector('[data-testid="settings-sso-stub"]'),
    ).toBeNull();
  });
});
