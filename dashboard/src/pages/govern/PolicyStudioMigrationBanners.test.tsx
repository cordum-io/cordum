import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import ReplayPage from "./ReplayPage";
import SimulatorPage from "./SimulatorPage";
import PolicyAnalyticsPage from "./PolicyAnalyticsPage";

// D9 step 6 — DoD #5: ReplayPage + SimulatorPage + PolicyAnalyticsPage
// marked deprecated with banners that link operators to the Policy Studio
// equivalents. The banner is the load-bearing user-visible change in this
// PR; deletion of these pages happens in D11 (cut-over). These tests
// assert the banner renders + carries the canonical migration link.

describe("Policy Studio migration banners on /govern/* pages", () => {
  it("ReplayPage renders the deprecation banner with a /policies/decisions migration link", async () => {
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [] })),
      http.get("*/api/v1/policy/bundles", () => HttpResponse.json({ items: [] })),
    );
    renderWithProviders(<ReplayPage />, { initialEntries: ["/govern/replay"] });
    await waitFor(() =>
      expect(screen.getByText(/Replay has moved to Policy Studio/i)).not.toBeNull(),
    );
    const link = screen.getByRole("link", { name: "/policies/decisions" });
    expect(link.getAttribute("href")).toBe("/policies/decisions");
    expect(link.getAttribute("data-row-action")).toBe(
      "cross-link-d9-replay-banner",
    );
  });

  it("SimulatorPage renders the deprecation banner with /policies and /policies/decisions migration links", async () => {
    server.use(
      http.get("*/api/v1/policy/bundles", () => HttpResponse.json({ items: [] })),
      http.get("*/api/v1/policy/global", () => HttpResponse.json({ sections: {} })),
    );
    renderWithProviders(<SimulatorPage />, { initialEntries: ["/govern/simulator"] });
    await waitFor(() =>
      expect(
        screen.getByText(/Simulator has moved to Policy Studio/i),
      ).not.toBeNull(),
    );
    const policiesLink = screen.getByRole("link", { name: "/policies" });
    expect(policiesLink.getAttribute("href")).toBe("/policies");
    expect(policiesLink.getAttribute("data-row-action")).toBe(
      "cross-link-d9-simulator-banner",
    );
    const decisionsLink = screen.getByRole("link", {
      name: "/policies/decisions",
    });
    expect(decisionsLink.getAttribute("href")).toBe("/policies/decisions");
    expect(decisionsLink.getAttribute("data-row-action")).toBe(
      "cross-link-d9-whatif-banner",
    );
  });

  it("PolicyAnalyticsPage renders the deprecation banner with /policies/decisions?charts=on migration link", async () => {
    server.use(
      http.get("*/api/v1/policy/analytics", () =>
        HttpResponse.json({ rules: [] }),
      ),
    );
    renderWithProviders(<PolicyAnalyticsPage />, {
      initialEntries: ["/govern/analytics"],
    });
    await waitFor(() =>
      expect(
        screen.getByText(/Analytics has moved to Policy Studio/i),
      ).not.toBeNull(),
    );
    const link = screen.getByRole("link", {
      name: "/policies/decisions?charts=on",
    });
    expect(link.getAttribute("href")).toBe("/policies/decisions?charts=on");
    expect(link.getAttribute("data-row-action")).toBe(
      "cross-link-d9-analytics-banner",
    );
  });
});
