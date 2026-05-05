import { describe, it, expect, beforeAll, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { renderWithProviders, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { assertNoSeriousAxeViolations } from "@/test-utils/a11y";
import PolicyOverviewPage from "./PolicyOverviewPage";

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
    http.get("*/api/v1/policy/bundles", () =>
      HttpResponse.json({ items: [] }),
    ),
    http.get("*/api/v1/policy/rules", () => HttpResponse.json({ items: [] })),
    http.get("*/api/v1/governance/approvals/analytics", () =>
      HttpResponse.json({
        summary: { total: 0, approved: 0, denied: 0, expired: 0 },
        groups: [],
      }),
    ),
  );
});

describe("PolicyOverviewPage a11y (axe-core gate)", () => {
  it("renders with no critical/serious axe violations (light mode)", async () => {
    const { container } = renderWithProviders(<PolicyOverviewPage />, {
      initialEntries: ["/govern/overview"],
    });
    await waitFor(() => expect(container.querySelector("h1")).toBeTruthy(), {
      timeout: 5000,
    });
    await assertNoSeriousAxeViolations(container, { mode: "light" });
  });

  it("renders with no critical/serious axe violations (dark mode)", async () => {
    const { container } = renderWithProviders(<PolicyOverviewPage />, {
      initialEntries: ["/govern/overview"],
    });
    await waitFor(() => expect(container.querySelector("h1")).toBeTruthy(), {
      timeout: 5000,
    });
    await assertNoSeriousAxeViolations(container, { mode: "dark" });
  });
});
