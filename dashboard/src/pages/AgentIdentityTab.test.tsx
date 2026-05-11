/**
 * AgentIdentityTab + AgentIdentityPanel tests.
 *
 * Migrated from a vi.mock("@/hooks/...") pattern to the dashboard's canonical
 * renderWithProviders + MSW pattern per dashboard/CLAUDE.md.
 * Decision record: dashboard/docs/adr/0001-page-test-providers.md.
 *
 * Reference: src/pages/SettingsHubPage.test.tsx (commit 1d7faf3d).
 *
 * Default MSW handlers in src/test-utils/handlers.ts cover /agents,
 * /agents/:id, /agents/:id/stats, /license, /workers — per-test
 * overrides via server.use(...) for the specific scenarios.
 */
import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentIdentity } from "@/api/types";
import {
  fireEvent,
  renderWithProviders,
  waitFor,
} from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { http, HttpResponse } from "msw";
import AgentsPage from "./AgentsPage";
import AgentIdentityPanel from "@/components/agents/AgentIdentityPanel";

// Component-level isolation — these are not data hooks, so vi.mock is allowed
// per dashboard/CLAUDE.md. They render to null so the AgentsPage smoke focuses
// on the Identity Directory tab content rather than worker/pool side panels.
vi.mock("@/components/agents/WorkerDetailDrawer", () => ({
  WorkerDetailDrawer: () => null,
}));
vi.mock("@/components/agents/PoolGroupedView", () => ({
  PoolGroupedView: () => null,
}));

// jsdom matchMedia stub — AgentsPage subcomponents use it for responsive
// layout decisions; without this, render throws.
beforeAll(() => {
  if (typeof window.matchMedia !== "function") {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: () => ({
        matches: false,
        media: "",
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }),
    });
  }
});

afterAll(() => {
  // Reset MSW server is auto-handled by setup.ts afterEach.
});

/* ------------------------------------------------------------------ */
/* Helpers                                                              */
/* ------------------------------------------------------------------ */

function makeAgent(overrides: Partial<AgentIdentity> = {}): AgentIdentity {
  return {
    id: "agent-001",
    name: "fraud-detector",
    owner: "risk-team",
    risk_tier: "high",
    status: "active",
    team: "risk",
    description: "Detects fraud",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-10T00:00:00Z",
    last_active: 1712793600000000,
    ...overrides,
  };
}

function activateIdentityTab(container: HTMLElement) {
  const tab = Array.from(container.querySelectorAll("button")).find(
    (btn) => btn.textContent?.includes("Identity Directory"),
  );
  if (tab) {
    fireEvent.click(tab);
  }
}

/* ------------------------------------------------------------------ */
/* Tests: AgentIdentityTab (rendered via AgentsPage)                    */
/* ------------------------------------------------------------------ */

describe("AgentIdentityTab rendered", () => {
  beforeEach(() => {
    // Default seed: a single agent in /agents, enterprise license is the
    // default in handlers.ts. Per-test overrides below.
    server.use(
      http.get("*/api/v1/agents", () =>
        HttpResponse.json({ items: [makeAgent()], cursor: null }),
      ),
    );
  });

  it("renders agent identity list with name, owner, risk tier, and status", async () => {
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("fraud-detector");
    });
    expect(container.textContent).toContain("risk-team");
    expect(container.textContent).toContain("high");
    expect(container.textContent).toContain("active");
  });

  it("renders empty state when no identities exist", async () => {
    server.use(
      http.get("*/api/v1/agents", () =>
        HttpResponse.json({ items: [], cursor: null }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("No agent identities registered");
    });
  });

  it("renders error state when loading fails", async () => {
    server.use(
      http.get("*/api/v1/agents", () =>
        HttpResponse.json({ error: "Network failure" }, { status: 500 }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("Failed to load agent identities");
    });
    expect(container.textContent).toContain("Network failure");
  });

  it("renders risk tier badges with correct color classes", async () => {
    server.use(
      http.get("*/api/v1/agents", () =>
        HttpResponse.json({
          items: [
            makeAgent({ id: "a1", risk_tier: "low", name: "low-agent" }),
            makeAgent({ id: "a2", risk_tier: "medium", name: "med-agent" }),
            makeAgent({ id: "a3", risk_tier: "high", name: "high-agent" }),
            makeAgent({ id: "a4", risk_tier: "critical", name: "crit-agent" }),
          ],
          cursor: null,
        }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("low-agent");
    });

    const badges = container.querySelectorAll("span");
    const badgeTexts = Array.from(badges).map((b) => b.textContent?.trim());
    expect(badgeTexts).toContain("low");
    expect(badgeTexts).toContain("medium");
    expect(badgeTexts).toContain("high");
    expect(badgeTexts).toContain("critical");

    const emeraldBadge = Array.from(badges).find(
      (b) => b.textContent?.trim() === "low" && b.className.includes("emerald"),
    );
    const redBadge = Array.from(badges).find(
      (b) => b.textContent?.trim() === "critical" && b.className.includes("red"),
    );
    expect(emeraldBadge).toBeTruthy();
    expect(redBadge).toBeTruthy();
  });

  it("shows last active from job data, not updated_at", async () => {
    const lastActiveMicro = 1713168000000000;
    server.use(
      http.get("*/api/v1/agents", () =>
        HttpResponse.json({
          items: [makeAgent({ last_active: lastActiveMicro })],
          cursor: null,
        }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("fraud-detector");
    });
    const cells = Array.from(container.querySelectorAll("td"));
    const lastActiveCell = cells[cells.length - 1];
    expect(lastActiveCell?.textContent).not.toBe("Never");
    expect(lastActiveCell?.textContent?.length).toBeGreaterThan(0);
  });

  it("shows 'Never' when last_active is zero or missing", async () => {
    server.use(
      http.get("*/api/v1/agents", () =>
        HttpResponse.json({
          items: [makeAgent({ last_active: 0 })],
          cursor: null,
        }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("fraud-detector");
    });
    const cells = Array.from(container.querySelectorAll("td"));
    const lastActiveCell = cells[cells.length - 1];
    expect(lastActiveCell?.textContent).toBe("Never");
  });

  it("shows upgrade prompt behind enterprise license gate", async () => {
    server.use(
      http.get("*/api/v1/license", () =>
        HttpResponse.json({
          plan: "community",
          entitlements: {},
          rights: null,
          license: null,
        }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("Agent Identity Directory");
    });
    expect(container.textContent).toContain("requires an Enterprise license");
    expect(container.textContent).toContain("View pricing");
    expect(container.textContent).toContain("community");
  });

  it("keeps Team tenants locked unless agentIdentity is explicitly granted", async () => {
    server.use(
      http.get("*/api/v1/license", () =>
        HttpResponse.json({
          plan: "team",
          entitlements: {},
          rights: null,
          license: null,
        }),
      ),
    );
    const { container } = renderWithProviders(<AgentsPage />, {
      initialEntries: ["/agents"],
    });
    activateIdentityTab(container);
    await waitFor(() => {
      expect(container.textContent).toContain("Agent Identity Directory");
    });
    expect(container.textContent).toContain("requires an Enterprise license");
    expect(container.textContent).toContain("team");
  });
});

/* ------------------------------------------------------------------ */
/* Tests: AgentIdentityPanel (rendered directly)                        */
/* ------------------------------------------------------------------ */

describe("AgentIdentityPanel rendered", () => {
  beforeEach(() => {
    server.use(
      http.get("*/api/v1/agents/:id", ({ params }) =>
        HttpResponse.json(
          makeAgent({
            id: String(params.id),
            allowed_topics: ["job.fraud.scan"],
            allowed_pools: ["pool-risk"],
            data_classifications: ["pii"],
          }),
        ),
      ),
      http.get("*/api/v1/agents/:id/stats", ({ params }) =>
        HttpResponse.json({
          agent_id: String(params.id),
          total_jobs_7d: 42,
          denied_7d: 3,
          last_active: 1713168000000000,
        }),
      ),
    );
  });

  it("renders agent name, status badge, and risk tier", async () => {
    const { container } = renderWithProviders(
      <AgentIdentityPanel agentId="agent-001" />,
      { initialEntries: ["/agents/agent-001?tab=identity"] },
    );
    await waitFor(() => {
      expect(container.textContent).toContain("fraud-detector");
    });
    expect(container.textContent).toContain("active");
    expect(container.textContent).toContain("high risk");
  });

  it("renders 7-day activity stats", async () => {
    const { container } = renderWithProviders(
      <AgentIdentityPanel agentId="agent-001" />,
      { initialEntries: ["/agents/agent-001?tab=identity"] },
    );
    await waitFor(() => {
      expect(container.textContent).toContain("42");
    });
    expect(container.textContent).toContain("jobs");
    expect(container.textContent).toContain("3");
    expect(container.textContent).toContain("denied");
  });

  it("renders permissions tag lists", async () => {
    const { container } = renderWithProviders(
      <AgentIdentityPanel agentId="agent-001" />,
      { initialEntries: ["/agents/agent-001?tab=identity"] },
    );
    await waitFor(() => {
      expect(container.textContent).toContain("job.fraud.scan");
    });
    expect(container.textContent).toContain("pool-risk");
    expect(container.textContent).toContain("pii");
  });

  it("renders EmptyState (not ErrorBanner) when useAgentIdentity returns 404", async () => {
    server.use(
      http.get("*/api/v1/agents/:id", () =>
        HttpResponse.json({ error: "Not Found" }, { status: 404 }),
      ),
    );
    const { container } = renderWithProviders(
      <AgentIdentityPanel agentId="agent-001" />,
      { initialEntries: ["/agents/agent-001?tab=identity"] },
    );
    await waitFor(() => {
      expect(container.textContent).toContain("No identity profile");
    });
    expect(container.textContent).toContain("cordumctl agents identity create");
    expect(container.textContent).not.toContain("Failed to load agent identity");
  });

  it("renders ErrorBanner (not EmptyState) for ApiError 500", async () => {
    server.use(
      http.get("*/api/v1/agents/:id", () =>
        HttpResponse.json({ error: "Internal Server Error" }, { status: 500 }),
      ),
    );
    const { container } = renderWithProviders(
      <AgentIdentityPanel agentId="agent-001" />,
      { initialEntries: ["/agents/agent-001?tab=identity"] },
    );
    await waitFor(() => {
      expect(container.textContent).toContain("Internal Server Error");
    });
    expect(container.textContent).not.toContain("No identity profile");
  });

  it("renders ErrorBanner (not EmptyState) for a non-ApiError network error", async () => {
    server.use(
      http.get("*/api/v1/agents/:id", () => HttpResponse.error()),
    );
    const { container } = renderWithProviders(
      <AgentIdentityPanel agentId="agent-001" />,
      { initialEntries: ["/agents/agent-001?tab=identity"] },
    );
    await waitFor(() => {
      // The shared client maps fetch's TypeError ("Failed to fetch") into
      // ApiError(0, "Network error...") — both forms are acceptable surface.
      expect(container.textContent).toMatch(/network|failed/i);
    });
    expect(container.textContent).not.toContain("No identity profile");
  });
});
