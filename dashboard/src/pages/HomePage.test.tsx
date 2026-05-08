import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import HomePage from "./HomePage";

const unhandledRequests: string[] = [];
const recordUnhandled = ({ request }: { request: Request }) => {
  unhandledRequests.push(`${request.method} ${request.url}`);
};

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
  // Fail tests on unhandled MSW requests so missing handlers (like
  // AuditChainCard's /api/v1/audit/verify) surface here instead of as a
  // silent warn-and-pass.
  server.events.on("request:unhandled", recordUnhandled);
});

afterAll(() => {
  server.events.removeListener("request:unhandled", recordUnhandled);
});

// Backend job records use `state` (not `status`), `updated_at` in microseconds
// since epoch, and a string `safety_decision` (transformed into `safetyDecision.type`
// by mapJobRecord).
const NOW_MICROS = 1_715_175_660_000_000; // 2024-05-08T13:01:00Z in microseconds
const sampleJob = (overrides: Record<string, unknown> = {}) => ({
  id: "job-abcdef0123456789",
  topic: "fraud.review",
  state: "succeeded",
  updated_at: NOW_MICROS,
  safety_decision: "allow",
  safety_reason: "policy match",
  ...overrides,
});

const sampleHeartbeat = (overrides: Record<string, unknown> = {}) => ({
  worker_id: "agent-alpha",
  status: "idle",
  pool: "default",
  capabilities: ["fraud.review"],
  active_jobs: 0,
  max_parallel_jobs: 4,
  cpu_load: 12,
  memory_load: 38,
  last_heartbeat: "2026-05-08T06:01:00Z",
  ...overrides,
});

beforeEach(() => {
  localStorage.clear();
  server.use(
    http.get("*/api/v1/jobs", () =>
      HttpResponse.json({
        items: [
          sampleJob({ id: "job-allow0000000000a" }),
          sampleJob({
            id: "job-deny00000000000b",
            state: "denied",
            safety_decision: "deny",
          }),
          sampleJob({
            id: "job-approval00000000c",
            state: "approval_required",
            safety_decision: "require_approval",
          }),
          sampleJob({
            id: "job-running000000000d",
            state: "running",
            safety_decision: "allow_with_constraints",
          }),
          sampleJob({
            id: "job-failed00000000000e",
            state: "failed",
            safety_decision: "throttle",
          }),
        ],
        total: 5,
      }),
    ),
    http.get("*/api/v1/workers", () =>
      HttpResponse.json({ items: [sampleHeartbeat()] }),
    ),
    http.get("*/api/v1/status", () =>
      HttpResponse.json({
        uptime_seconds: 3600,
        nats: { connected: true, status: "ok" },
        redis: { ok: true },
        workers: { count: 1 },
        circuit_breakers: { input: { state: "CLOSED" } },
      }),
    ),
    http.get("*/api/v1/governance/health", () =>
      HttpResponse.json({
        status: "healthy",
        chain: { verified_through: 1, head: "0xabc", last_verified_at: 0 },
      }),
    ),
    // AuditChainCard polls /api/v1/audit/verify (5-min refetch) on every
    // HomePage mount; without a handler here MSW falls through to its real-fetch
    // shim and emits unhandled-request warnings.
    http.get("*/api/v1/audit/verify", () =>
      HttpResponse.json({
        status: "ok",
        total_events: 0,
        verified_events: 0,
        gaps: [],
        retention_boundary_seq: 0,
      }),
    ),
  );
});

afterEach(() => {
  const drained = unhandledRequests.splice(0);
  server.resetHandlers();
  if (drained.length > 0) {
    throw new Error(
      `Unhandled MSW requests in HomePage test (add handlers in beforeEach):\n${drained.join("\n")}`,
    );
  }
});

async function waitForKpis() {
  await waitFor(
    () => {
      expect(screen.getByText("Recent Jobs")).toBeTruthy();
    },
    { timeout: 5000 },
  );
}

describe("HomePage", () => {
  it("renders all four KPI tiles after data loads", async () => {
    renderWithProviders(<HomePage />, { initialEntries: ["/"] });
    await waitForKpis();
    expect(screen.getByText("Active Agents")).toBeTruthy();
    expect(screen.getByText("Safety Decisions")).toBeTruthy();
    expect(screen.getByText("Pending Approvals")).toBeTruthy();
  });

  it("uses primitives/DataTable for the recent activity row (decision-tier left edge present)", async () => {
    const { container } = renderWithProviders(<HomePage />, {
      initialEntries: ["/"],
    });
    await waitForKpis();
    await waitFor(
      () => {
        expect(
          container.querySelector("[data-decision-tier]"),
        ).not.toBeNull();
      },
      { timeout: 5000 },
    );
  });

  it("emits no raw chart colors — donut path[fill] is var(--chart-N) only, never hex/rgb/rgba", async () => {
    const { container } = renderWithProviders(<HomePage />, {
      initialEntries: ["/"],
    });
    await waitForKpis();
    const cells = container.querySelectorAll("path[fill]");
    const fills = Array.from(cells)
      .map((c) => c.getAttribute("fill") ?? "")
      .filter((f) => f && f !== "none" && !f.startsWith("url("));
    // Reject ANY raw color literal (hex, rgb(), rgba(), hsl()) — donut cells
    // must consume `var(--chart-N)` so palette swaps land via the token system.
    const rawColors = fills.filter((f) =>
      /^#[0-9a-fA-F]{3,8}$/.test(f) ||
      /^rgba?\(/.test(f) ||
      /^hsla?\(/.test(f),
    );
    expect(rawColors).toEqual([]);
    // The legend chips below the chart use inline `style.backgroundColor` set
    // from the same DECISION_PALETTE constant — assert at least one CSS-var
    // chart token is emitted to the DOM somewhere, so the assertion above is
    // not vacuous if recharts itself hasn't painted the SVG paths yet in jsdom.
    const html = container.innerHTML;
    expect(html).toMatch(/var\(--chart-[1-5]\)/);
  });

  it("toggles live mode via aria-pressed when the Live button is clicked", async () => {
    const { container } = renderWithProviders(<HomePage />, {
      initialEntries: ["/"],
    });
    await waitForKpis();
    const liveButton = container.querySelector<HTMLButtonElement>(
      'button[aria-label^="Live mode"]',
    );
    expect(liveButton).not.toBeNull();
    expect(liveButton?.getAttribute("aria-pressed")).toBe("false");
    if (liveButton) fireEvent.click(liveButton);
    await waitFor(
      () => {
        expect(liveButton?.getAttribute("aria-pressed")).toBe("true");
      },
      { timeout: 2000 },
    );
  });

  it("does not render the onboarding checklist when jobs/workers exist", async () => {
    renderWithProviders(<HomePage />, { initialEntries: ["/"] });
    await waitForKpis();
    expect(screen.queryByText(/welcome to cordum/i)).toBeNull();
  });
});

describe("HomePage onboarding", () => {
  beforeEach(() => {
    server.use(
      http.get("*/api/v1/jobs", () => HttpResponse.json({ items: [], total: 0 })),
      http.get("*/api/v1/workers", () => HttpResponse.json({ items: [] })),
      http.get("*/api/v1/approvals", () => HttpResponse.json({ items: [] })),
    );
  });

  it("persists onboarding-dismissed=true to localStorage when the user dismisses it", async () => {
    const { container } = renderWithProviders(<HomePage />, {
      initialEntries: ["/"],
    });
    await waitForKpis();
    // Onboarding dismiss button text varies; rely on localStorage write side-effect.
    // First confirm the onboarding shell rendered (any element bearing the Dismiss/Got it action).
    const dismissButton = await waitFor(
      () => {
        const button = Array.from(container.querySelectorAll("button")).find((b) =>
          /dismiss|got it|hide/i.test(b.textContent ?? ""),
        );
        if (!button) throw new Error("dismiss button not found yet");
        return button;
      },
      { timeout: 5000 },
    );
    fireEvent.click(dismissButton);
    await waitFor(
      () => {
        expect(localStorage.getItem("onboarding-dismissed")).toBe("true");
      },
      { timeout: 2000 },
    );
  });
});
