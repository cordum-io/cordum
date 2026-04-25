import { describe, it, expect, beforeAll, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { assertNoSeriousAxeViolations } from "@/test-utils/a11y";
import SettingsHubPage from "./SettingsHubPage";

beforeAll(() => {
  if (typeof globalThis.ResizeObserver === "undefined") {
    class ResizeObserverMock {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    globalThis.ResizeObserver =
      ResizeObserverMock as unknown as typeof ResizeObserver;
  }
});

beforeEach(() => {
  server.use(
    http.get("*/api/v1/license", () =>
      HttpResponse.json({
        tier: "enterprise",
        entitlements: {},
        expires_at: null,
      }),
    ),
  );
});

describe("SettingsHubPage a11y (axe-core gate)", () => {
  it("renders with no critical/serious axe violations (light mode)", async () => {
    const { container } = renderWithProviders(<SettingsHubPage />, {
      initialEntries: ["/settings"],
    });
    await waitFor(() => expect(container.querySelector("h1")).toBeTruthy(), {
      timeout: 5000,
    });
    await assertNoSeriousAxeViolations(container, { mode: "light" });
  });

  it("renders with no critical/serious axe violations (dark mode)", async () => {
    const { container } = renderWithProviders(<SettingsHubPage />, {
      initialEntries: ["/settings"],
    });
    await waitFor(() => expect(container.querySelector("h1")).toBeTruthy(), {
      timeout: 5000,
    });
    await assertNoSeriousAxeViolations(container, { mode: "dark" });
  });
});
