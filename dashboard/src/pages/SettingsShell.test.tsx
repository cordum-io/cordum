/*
 * SettingsShell smoke test.
 *
 * Convention: dashboard/CLAUDE.md (page tests use renderWithProviders + MSW).
 * Decision record: dashboard/docs/adr/0001-page-test-providers.md.
 *
 * Cases mirror the shell's contract:
 *   (a) renders all 5 sub-nav groups in canonical order
 *   (b) NavLink active state highlights the current sub-route
 *   (c) entitlement-locked rows render the Lock icon when license lacks the entitlement
 *   (d) Outlet renders the index route element when the path is /settings exactly
 */
import { beforeEach, describe, expect, it } from "vitest";
import { Route, Routes } from "react-router-dom";
import { http, HttpResponse } from "msw";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import SettingsShell from "./SettingsShell";

const HUB_TESTID = "settings-hub-stub";

const ENTERPRISE_LICENSE = {
  plan: "enterprise",
  entitlements: {
    sso: true,
    saml: true,
    scim: true,
    rbac: true,
    audit: true,
    audit_export: true,
    siem_export: true,
    legal_hold: true,
    velocity_rules: true,
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

function ShellHarness() {
  return (
    <Routes>
      <Route path="/settings" element={<SettingsShell />}>
        <Route index element={<div data-testid={HUB_TESTID}>hub</div>} />
        <Route path="users" element={<div data-testid="users-page" />} />
        <Route path="sso" element={<div data-testid="sso-page" />} />
        <Route path="scim" element={<div data-testid="scim-page" />} />
        <Route
          path="audit-export"
          element={<div data-testid="audit-page" />}
        />
        <Route path="license" element={<div data-testid="license-page" />} />
        <Route path="health" element={<div data-testid="health-page" />} />
        <Route path="keys" element={<div data-testid="keys-page" />} />
        <Route path="config" element={<div data-testid="config-page" />} />
        <Route
          path="environments"
          element={<div data-testid="env-page" />}
        />
        <Route
          path="notifications"
          element={<div data-testid="notif-page" />}
        />
        <Route path="mcp" element={<div data-testid="mcp-page" />} />
      </Route>
    </Routes>
  );
}

function findLinkByText(
  container: ParentNode,
  text: string,
): HTMLAnchorElement | null {
  return (
    Array.from(container.querySelectorAll("a")).find((a) =>
      a.textContent?.includes(text),
    ) ?? null
  );
}

describe("SettingsShell", () => {
  it("renders all 5 sub-nav groups in canonical order and unlocks gated rows under enterprise license", async () => {
    const { container } = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings"],
    });
    const aside = container.querySelector(
      'aside[aria-label="Settings navigation"]',
    );
    expect(aside).not.toBeNull();
    const groupLabels = Array.from(aside!.querySelectorAll("h3")).map((h) =>
      h.textContent?.trim(),
    );
    expect(groupLabels).toEqual([
      "Overview",
      "Plan & Health",
      "Identity & Access",
      "Configuration",
      "Audit Export",
    ]);
    // After enterprise license resolves, no gated row should still render the Lock badge.
    // Combined with the community-license case below, this proves the loading→entitled
    // transition flips the lock state — not just the static "lock during loading".
    await waitFor(() => {
      const locks = container.querySelectorAll(
        '[aria-label="Enterprise feature"]',
      );
      expect(locks.length).toBe(0);
    });
  });

  it("highlights the active sub-nav entry for the current sub-route", async () => {
    const { container } = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings/users"],
    });
    await waitFor(() => {
      expect(container.querySelector('a[aria-current="page"]')).not.toBeNull();
    });
    const active = container.querySelector('a[aria-current="page"]')!;
    expect(active.textContent).toContain("Users & RBAC");
    expect(active.className).toContain("text-cordum");
    expect(active.className).toContain("font-medium");
  });

  it("renders the Lock icon on entitlement-gated rows when license lacks entitlements", async () => {
    server.use(
      http.get("*/api/v1/license", () => HttpResponse.json(COMMUNITY_LICENSE)),
    );
    const { container } = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings"],
    });

    // SSO & SAML, SCIM, SIEM Export are the three gated rows in SUB_NAV.
    await waitFor(() => {
      const locks = container.querySelectorAll(
        '[aria-label="Enterprise feature"]',
      );
      expect(locks.length).toBe(3);
    });

    const ssoLink = findLinkByText(container, "SSO & SAML");
    const scimLink = findLinkByText(container, "SCIM");
    const siemLink = findLinkByText(container, "SIEM Export");
    const usersLink = findLinkByText(container, "Users & RBAC");
    const licenseLink = findLinkByText(container, "License & Limits");

    expect(
      ssoLink?.querySelector('[aria-label="Enterprise feature"]'),
    ).not.toBeNull();
    expect(
      scimLink?.querySelector('[aria-label="Enterprise feature"]'),
    ).not.toBeNull();
    expect(
      siemLink?.querySelector('[aria-label="Enterprise feature"]'),
    ).not.toBeNull();

    // Ungated rows must never render the Lock — guards against over-locking regressions.
    expect(
      usersLink?.querySelector('[aria-label="Enterprise feature"]'),
    ).toBeNull();
    expect(
      licenseLink?.querySelector('[aria-label="Enterprise feature"]'),
    ).toBeNull();
  });

  it("clicking a locked entry intercepts navigation and opens the upgrade dialog", async () => {
    server.use(
      http.get("*/api/v1/license", () => HttpResponse.json(COMMUNITY_LICENSE)),
    );
    const { container } = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings"],
    });

    // Wait for the community license to settle so locks render.
    await waitFor(() => {
      expect(
        container.querySelectorAll('[aria-label="Enterprise feature"]').length,
      ).toBe(3);
    });

    const ssoLink = findLinkByText(container, "SSO & SAML")!;
    fireEvent.click(ssoLink);

    // Dialog appears with the feature name; navigation does not occur (no sso-page stub).
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain("SSO & SAML requires an upgraded plan");
    expect(dialog.textContent).toContain("community");
    expect(container.querySelector('[data-testid="sso-page"]')).toBeNull();
    // Hub stub remains rendered — proves we stayed at /settings.
    expect(
      container.querySelector(`[data-testid="${HUB_TESTID}"]`),
    ).not.toBeNull();
  });

  it("clicking an unlocked entry navigates normally and does not open the upgrade dialog", async () => {
    server.use(
      http.get("*/api/v1/license", () => HttpResponse.json(COMMUNITY_LICENSE)),
    );
    const { container } = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings"],
    });

    // Wait for license to settle so the locked-state evaluation is stable.
    await waitFor(() => {
      expect(
        container.querySelectorAll('[aria-label="Enterprise feature"]').length,
      ).toBe(3);
    });

    const usersLink = findLinkByText(container, "Users & RBAC")!;
    fireEvent.click(usersLink);

    await waitFor(() => {
      expect(
        container.querySelector('[data-testid="users-page"]'),
      ).not.toBeNull();
    });
    // No dialog should be open after a normal navigation click.
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  });

  it("renders the index Outlet element inside <main> at /settings exactly and not on sub-routes", () => {
    const { container, unmount } = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings"],
    });
    const main = container.querySelector("main");
    expect(main).not.toBeNull();
    expect(main!.querySelector(`[data-testid="${HUB_TESTID}"]`)).not.toBeNull();
    // Hub stub must not leak into the navigation rail.
    const aside = container.querySelector(
      'aside[aria-label="Settings navigation"]',
    );
    expect(aside!.querySelector(`[data-testid="${HUB_TESTID}"]`)).toBeNull();
    unmount();

    // Index route must NOT render at a non-index sub-route.
    const subRoute = renderWithProviders(<ShellHarness />, {
      initialEntries: ["/settings/users"],
    });
    expect(
      subRoute.container.querySelector(`[data-testid="${HUB_TESTID}"]`),
    ).toBeNull();
    expect(
      subRoute.container.querySelector('[data-testid="users-page"]'),
    ).not.toBeNull();
  });
});
