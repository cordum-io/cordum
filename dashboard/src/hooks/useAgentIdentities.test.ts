import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AgentIdentity, AgentStats } from "../api/types";

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({
  logger: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

describe("AgentIdentity types", () => {
  it("AgentIdentity has required fields", () => {
    const agent: AgentIdentity = {
      id: "agent-1",
      name: "fraud-detector",
      owner: "risk-team",
      risk_tier: "high",
      status: "active",
      created_at: "2026-04-15T00:00:00Z",
      updated_at: "2026-04-15T00:00:00Z",
    };
    expect(agent.id).toBe("agent-1");
    expect(agent.risk_tier).toBe("high");
    expect(agent.status).toBe("active");
  });

  it("AgentIdentity risk_tier values are typed", () => {
    const tiers: AgentIdentity["risk_tier"][] = ["low", "medium", "high", "critical"];
    expect(tiers).toHaveLength(4);
  });

  it("AgentIdentity status values are typed", () => {
    const statuses: AgentIdentity["status"][] = ["active", "suspended", "revoked"];
    expect(statuses).toHaveLength(3);
  });

  it("AgentStats has 7-day metrics", () => {
    const stats: AgentStats = {
      agent_id: "agent-1",
      total_jobs_7d: 42,
      denied_7d: 3,
      last_active: 1713168000000000,
    };
    expect(stats.total_jobs_7d).toBe(42);
    expect(stats.denied_7d).toBe(3);
  });

  it("AgentIdentity optional fields default correctly", () => {
    const agent: AgentIdentity = {
      id: "agent-2",
      name: "minimal-agent",
      owner: "admin",
      risk_tier: "low",
      status: "active",
      created_at: "2026-04-15T00:00:00Z",
      updated_at: "2026-04-15T00:00:00Z",
    };
    expect(agent.description).toBeUndefined();
    expect(agent.team).toBeUndefined();
    expect(agent.allowed_topics).toBeUndefined();
    expect(agent.data_classifications).toBeUndefined();
  });
});

describe("RiskTierBadge colors", () => {
  const tierColors: Record<string, string> = {
    low: "emerald",
    medium: "amber",
    high: "orange",
    critical: "red",
  };

  it.each(Object.entries(tierColors))("tier %s maps to %s color", (tier, expectedColor) => {
    expect(tierColors[tier]).toBe(expectedColor);
  });
});
