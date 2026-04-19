import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentToolVisibilityTab } from "./AgentToolVisibilityTab";
import type {
  AgentDenyEventsResponse,
  AgentToolVisibility,
} from "@/hooks/useAgentTools";

const { toolsState, eventsState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  return {
    toolsState: {
      data: undefined as AgentToolVisibility | undefined,
      isLoading: false,
      isError: false,
      error: null as Error | null,
    },
    eventsState: {
      data: undefined as AgentDenyEventsResponse | undefined,
      isLoading: false,
      isError: false,
      error: null as Error | null,
    },
  };
});

vi.mock("@/hooks/useAgentTools", () => ({
  useAgentToolVisibility: () => toolsState,
  useAgentDeniedEvents: () => eventsState,
}));

function render() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(<AgentToolVisibilityTab agentId="agent-xyz" />);
  });
  return {
    container,
    unmount: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("AgentToolVisibilityTab", () => {
  beforeEach(() => {
    toolsState.data = undefined;
    toolsState.isLoading = false;
    toolsState.isError = false;
    toolsState.error = null;
    eventsState.data = undefined;
    eventsState.isLoading = false;
    eventsState.isError = false;
    eventsState.error = null;
  });

  it("renders the filtered tool list", () => {
    toolsState.data = {
      agent_id: "agent-xyz",
      filtered: true,
      tools: [
        { name: "fs.read", riskTier: "low", description: "Read a file" },
        { name: "jobs.submit", riskTier: "medium" },
      ],
    };
    eventsState.data = { agent_id: "agent-xyz", events: [], limit: 50 };

    const { container, unmount } = render();
    expect(container.textContent).toContain("fs.read");
    expect(container.textContent).toContain("jobs.submit");
    expect(container.textContent).toContain("Read a file");
    expect(container.querySelector("[data-testid=agent-tool-visibility]"))
      .not.toBeNull();
    unmount();
  });

  it("renders empty state when identity sees zero tools", () => {
    toolsState.data = { agent_id: "agent-xyz", filtered: true, tools: [] };
    eventsState.data = { agent_id: "agent-xyz", events: [], limit: 50 };

    const { container, unmount } = render();
    const empty = container.querySelector("[data-testid=agent-tool-visibility-empty]");
    expect(empty).not.toBeNull();
    expect(empty?.textContent).toContain("No tools visible");
    unmount();
  });

  it("renders a note when identity is revoked", () => {
    toolsState.data = {
      agent_id: "agent-xyz",
      filtered: true,
      tools: [],
      note: "identity is revoked or suspended",
    };
    eventsState.data = { agent_id: "agent-xyz", events: [], limit: 50 };

    const { container, unmount } = render();
    expect(container.textContent).toContain("identity is revoked or suspended");
    unmount();
  });

  it("surfaces tool-visibility errors", () => {
    toolsState.isError = true;
    toolsState.error = new Error("tool fetch 500");
    eventsState.data = { agent_id: "agent-xyz", events: [], limit: 50 };

    const { container, unmount } = render();
    expect(container.textContent).toContain("tool fetch 500");
    unmount();
  });

  it("shows recent denials with sub_reason labels", () => {
    toolsState.data = { agent_id: "agent-xyz", filtered: true, tools: [] };
    eventsState.data = {
      agent_id: "agent-xyz",
      limit: 50,
      events: [
        {
          timestamp: new Date().toISOString(),
          agent_id: "agent-xyz",
          tool_name: "nuke.everything",
          sub_reason: "risk_tier_too_low",
          severity: "HIGH",
        },
      ],
    };

    const { container, unmount } = render();
    expect(container.textContent).toContain("nuke.everything");
    expect(container.textContent).toContain("Risk tier too low");
    unmount();
  });

  it("renders the denials empty state", () => {
    toolsState.data = { agent_id: "agent-xyz", filtered: true, tools: [] };
    eventsState.data = { agent_id: "agent-xyz", events: [], limit: 50 };

    const { container, unmount } = render();
    const empty = container.querySelector("[data-testid=agent-tool-denials-empty]");
    expect(empty).not.toBeNull();
    unmount();
  });
});
