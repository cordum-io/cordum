import { describe, it, expect, beforeAll, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { assertNoSeriousAxeViolations } from "@/test-utils/a11y";
import HomePage from "./HomePage";

// jsdom does not provide ResizeObserver; recharts' ResponsiveContainer needs it.
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
    http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [], total: 0 })),
    http.get("*/api/v1/workers", () => HttpResponse.json({ items: [] })),
    http.get("*/api/v1/heartbeats", () => HttpResponse.json({ items: [] })),
    http.get("*/api/v1/status", () =>
      HttpResponse.json({ status: "ok", version: "test" }),
    ),
    http.get("*/api/v1/audit/chain/head", () => HttpResponse.json({ head: null })),
  );
});

describe("HomePage a11y (axe-core gate)", () => {
  it("renders with no critical/serious axe violations (light mode)", async () => {
    const { container } = renderWithProviders(<HomePage />, {
      initialEntries: ["/"],
    });
    await waitFor(() => expect(container.querySelector("h1")).toBeTruthy(), {
      timeout: 5000,
    });
    await assertNoSeriousAxeViolations(container, { mode: "light" });
  });

  it("renders with no critical/serious axe violations (dark mode)", async () => {
    const { container } = renderWithProviders(<HomePage />, {
      initialEntries: ["/"],
    });
    await waitFor(() => expect(container.querySelector("h1")).toBeTruthy(), {
      timeout: 5000,
    });
    await assertNoSeriousAxeViolations(container, { mode: "dark" });
  });
});
