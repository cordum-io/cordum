import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentIdentity, AgentStats } from "@/api/types";
import { ApiError } from "@/api/client";
import AgentsPage from "./AgentsPage";
import AgentIdentityPanel from "@/components/agents/AgentIdentityPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

/* ------------------------------------------------------------------ */
/* Mock state                                                          */
/* ------------------------------------------------------------------ */

const { hookState } = vi.hoisted(() => ({
  hookState: {
    identities: {
      data: undefined as { items: AgentIdentity[]; cursor?: string } | undefined,
      isLoading: false,
      isError: false,
      error: null as Error | null,
    },
    identity: {
      data: undefined as AgentIdentity | undefined,
      isLoading: false,
      isError: false,
      error: null as Error | null,
    },
    stats: {
      data: undefined as AgentStats | undefined,
      isLoading: false,
      isError: false,
    },
    license: {
      data: {
        plan: "enterprise",
        entitlements: { agentIdentity: true },
      },
      isLoading: false,
      isError: false,
    },
  },
}));

/* ------------------------------------------------------------------ */
/* Mocks                                                               */
/* ------------------------------------------------------------------ */

vi.mock("@/hooks/useAgentIdentities", async () => {
  const actual = await vi.importActual<typeof import("@/hooks/useAgentIdentities")>(
    "@/hooks/useAgentIdentities",
  );
  return {
    ...actual,
    useAgentIdentities: () => hookState.identities,
    useAgentIdentity: () => hookState.identity,
    useAgentStats: () => hookState.stats,
    // useCreateAgentIdentity (added for issue #314 empty-state CTA) is mocked
    // as a no-op success so the form can render without a real network call.
    useCreateAgentIdentity: () => ({
      mutate: vi.fn(),
      isPending: false,
      isError: false,
      error: null,
    }),
  };
});

vi.mock("@/hooks/useLicense", () => ({
  useLicense: () => hookState.license,
  useLicenseUsage: () => ({ data: undefined }),
}));

vi.mock("@/hooks/useWorkers", () => ({
  useWorkers: () => ({ data: [], isLoading: false }),
  // useWorker is the heartbeat fetch the AgentIdentityPanel uses to pre-fill
  // the empty-state create form (issue #314). Default to undefined so the
  // panel falls back to deterministic prettify(agent_id) for the name.
  useWorker: () => ({ data: undefined, isLoading: false }),
  useWorkerJobs: () => ({ data: [], isLoading: false }),
}));

vi.mock("@/state/config", () => ({
  useConfigStore: (selector: (s: { principalId: string }) => unknown) =>
    selector({ principalId: "test-operator" }),
}));

vi.mock("@/components/agents/WorkerDetailDrawer", () => ({
  WorkerDetailDrawer: () => null,
}));

vi.mock("@/components/agents/PoolGroupedView", () => ({
  PoolGroupedView: () => null,
}));


/* ------------------------------------------------------------------ */
/* Helpers                                                             */
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

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
}

function renderPage(route = "/agents") {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = makeQueryClient();

  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[route]}>
          <AgentsPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  // Click the Identities tab to activate it. Tab label was renamed from
  // "Identity Directory" → "Identities" by task-083581ca consolidation.
  const identityTab = Array.from(container.querySelectorAll("button")).find(
    (btn) => btn.textContent?.includes("Identities"),
  );
  if (identityTab) {
    act(() => {
      identityTab.dispatchEvent(
        new MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
  }

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

function renderIdentityPanel(id = "agent-001") {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const qc = makeQueryClient();

  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/agents/${id}?tab=identity`]}>
          <AgentIdentityPanel agentId={id} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

/* ------------------------------------------------------------------ */
/* Tests: Identity Tab (rendered component tests)                      */
/* ------------------------------------------------------------------ */

describe("AgentIdentityTab rendered", () => {
  beforeEach(() => {
    hookState.identities = {
      data: { items: [makeAgent()] },
      isLoading: false,
      isError: false,
      error: null,
    };
    hookState.license = {
      data: {
        plan: "enterprise",
        entitlements: { agentIdentity: true },
      },
      isLoading: false,
      isError: false,
    };
  });

  it("renders agent identity list with name, owner, risk tier, and status", () => {
    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("fraud-detector");
      expect(container.textContent).toContain("risk-team");
      expect(container.textContent).toContain("high");
      expect(container.textContent).toContain("active");
    } finally {
      cleanup();
    }
  });

  it("renders empty state when no identities exist", () => {
    hookState.identities.data = { items: [] };
    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("No agent identities registered");
    } finally {
      cleanup();
    }
  });

  it("renders error state when loading fails", () => {
    hookState.identities = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("Network failure"),
    };
    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("Failed to load agent identities");
      expect(container.textContent).toContain("Network failure");
    } finally {
      cleanup();
    }
  });

  it("renders risk tier badges with correct color classes", () => {
    hookState.identities.data = {
      items: [
        makeAgent({ id: "a1", risk_tier: "low", name: "low-agent" }),
        makeAgent({ id: "a2", risk_tier: "medium", name: "med-agent" }),
        makeAgent({ id: "a3", risk_tier: "high", name: "high-agent" }),
        makeAgent({ id: "a4", risk_tier: "critical", name: "crit-agent" }),
      ],
    };
    const { container, cleanup } = renderPage();
    try {
      // Each risk tier badge should be present in the rendered output.
      const badges = container.querySelectorAll("span");
      const badgeTexts = Array.from(badges).map((b) => b.textContent?.trim());

      expect(badgeTexts).toContain("low");
      expect(badgeTexts).toContain("medium");
      expect(badgeTexts).toContain("high");
      expect(badgeTexts).toContain("critical");

      // Verify color classes on the badge elements.
      const emeraldBadge = Array.from(badges).find(
        (b) => b.textContent?.trim() === "low" && b.className.includes("emerald"),
      );
      const redBadge = Array.from(badges).find(
        (b) => b.textContent?.trim() === "critical" && b.className.includes("red"),
      );
      expect(emeraldBadge).toBeTruthy();
      expect(redBadge).toBeTruthy();
    } finally {
      cleanup();
    }
  });

  it("shows last active from job data, not updated_at", () => {
    const lastActiveMicro = 1713168000000000; // 2024-04-15 in microseconds
    hookState.identities.data = {
      items: [makeAgent({ last_active: lastActiveMicro })],
    };
    const { container, cleanup } = renderPage();
    try {
      // Should NOT show "Never" since last_active is set.
      const cells = Array.from(container.querySelectorAll("td"));
      const lastActiveCell = cells[cells.length - 1];
      expect(lastActiveCell?.textContent).not.toBe("Never");
      // The formatted relative time should be present (not the raw timestamp).
      expect(lastActiveCell?.textContent?.length).toBeGreaterThan(0);
    } finally {
      cleanup();
    }
  });

  it("shows 'Never' when last_active is zero or missing", () => {
    hookState.identities.data = {
      items: [makeAgent({ last_active: 0 })],
    };
    const { container, cleanup } = renderPage();
    try {
      const cells = Array.from(container.querySelectorAll("td"));
      const lastActiveCell = cells[cells.length - 1];
      expect(lastActiveCell?.textContent).toBe("Never");
    } finally {
      cleanup();
    }
  });

  it("shows upgrade prompt behind enterprise license gate", () => {
    hookState.license.data = {
      plan: "community",
      entitlements: {},
    } as any;
    const { container, cleanup } = renderPage();
    try {
      // The gate renders children blurred + an overlay with upgrade CTA.
      expect(container.textContent).toContain("Agent Identities");
      expect(container.textContent).toContain("requires an Enterprise license");
      expect(container.textContent).toContain("View pricing");
      expect(container.textContent).toContain("community");
    } finally {
      cleanup();
    }
  });

  it("keeps Team tenants locked unless agentIdentity is explicitly granted", () => {
    hookState.license.data = {
      plan: "team",
      entitlements: {},
    } as any;
    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("Agent Identities");
      expect(container.textContent).toContain("requires an Enterprise license");
      expect(container.textContent).toContain("team");
    } finally {
      cleanup();
    }
  });
});

/* ------------------------------------------------------------------ */
/* Tests: Identity Detail Page (rendered component tests)              */
/* ------------------------------------------------------------------ */

describe("AgentIdentityPanel rendered", () => {
  beforeEach(() => {
    hookState.identity = {
      data: makeAgent({
        allowed_topics: ["job.fraud.scan"],
        allowed_pools: ["pool-risk"],
        data_classifications: ["pii"],
      }),
      isLoading: false,
      isError: false,
      error: null,
    };
    hookState.stats = {
      data: {
        agent_id: "agent-001",
        total_jobs_7d: 42,
        denied_7d: 3,
        last_active: 1713168000000000,
      },
      isLoading: false,
      isError: false,
    };
  });

  it("renders agent name, status badge, and risk tier", () => {
    const { container, cleanup } = renderIdentityPanel();
    try {
      expect(container.textContent).toContain("fraud-detector");
      expect(container.textContent).toContain("active");
      expect(container.textContent).toContain("high risk");
    } finally {
      cleanup();
    }
  });

  it("renders 7-day activity stats", () => {
    const { container, cleanup } = renderIdentityPanel();
    try {
      expect(container.textContent).toContain("42");
      expect(container.textContent).toContain("jobs");
      expect(container.textContent).toContain("3");
      expect(container.textContent).toContain("denied");
    } finally {
      cleanup();
    }
  });

  it("renders permissions tag lists", () => {
    const { container, cleanup } = renderIdentityPanel();
    try {
      expect(container.textContent).toContain("job.fraud.scan");
      expect(container.textContent).toContain("pool-risk");
      expect(container.textContent).toContain("pii");
    } finally {
      cleanup();
    }
  });

  it("renders EmptyState with create-identity CTA (not ErrorBanner) when useAgentIdentity returns 404", () => {
    // Issue #314 — previously this rendered a text-only placeholder pointing
    // operators at the catalog / CLI. It now renders an actionable empty
    // state with a "Create identity" button that pre-fills from the
    // worker's heartbeat (the form pops inline on click; not asserted here
    // — its behavior is covered by a focused test below).
    hookState.identity = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError(404, "Not Found"),
    };
    const { container, cleanup } = renderIdentityPanel();
    try {
      expect(container.textContent).toContain("No identity registered for this agent");
      // Must explain the operational consequence (raw agent_id in audit).
      expect(container.textContent).toContain("agent_id");
      // Must offer the in-place CTA.
      const cta = container.querySelector('[data-testid="agent-identity-create-button"]');
      expect(cta).not.toBeNull();
      expect(cta?.textContent).toContain("Create identity");
      // Must NOT degrade into the generic error banner.
      expect(container.textContent).not.toContain("Failed to load agent identity");
    } finally {
      cleanup();
    }
  });

  it("renders ErrorBanner (not EmptyState) for ApiError 500", () => {
    hookState.identity = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError(500, "Internal Server Error"),
    };
    const { container, cleanup } = renderIdentityPanel();
    try {
      expect(container.textContent).toContain("Internal Server Error");
      expect(container.textContent).not.toContain("No identity profile");
    } finally {
      cleanup();
    }
  });

  it("renders ErrorBanner (not EmptyState) for a non-ApiError network error", () => {
    hookState.identity = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("Network failure"),
    };
    const { container, cleanup } = renderIdentityPanel();
    try {
      expect(container.textContent).toContain("Network failure");
      expect(container.textContent).not.toContain("No identity profile");
    } finally {
      cleanup();
    }
  });
});
