import React, { act } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import axe from "axe-core";
import { describe, expect, it } from "vitest";
import type { GovernanceDecision } from "@/api/types";
import { createTestQueryClient } from "@/hooks/__tests__/test-utils";
import { GovernanceTimeline } from "./GovernanceTimeline";

function makeDecision(verdict: GovernanceDecision["verdict"]): GovernanceDecision {
  return {
    jobId: "job-1",
    topic: "jobs.review",
    matchedRule: `rule-${verdict}`,
    ruleName: `Rule ${verdict}`,
    verdict,
    reason: `Reason for ${verdict}`,
    constraints: {
      maxInvocations: 2,
      allowedDomains: ["cordum.io"],
    },
    approvalStatus: verdict === "require_approval" ? "pending" : undefined,
    agentId: "agent-1",
    timestamp: "2026-04-20T10:00:00.000Z",
  };
}

function renderTimeline(props: React.ComponentProps<typeof GovernanceTimeline>) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const queryClient = createTestQueryClient();

  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <GovernanceTimeline {...props} />
      </QueryClientProvider>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
      queryClient.clear();
    },
  };
}

describe("GovernanceTimeline accessibility", () => {
  it("has no axe violations for the rendered governance rail", async () => {
    const { container, cleanup } = renderTimeline({
      items: [
        makeDecision("allow"),
        makeDecision("deny"),
        makeDecision("constrain"),
        makeDecision("require_approval"),
      ],
    });

    try {
      const result = await axe.run(container);
      expect(result.violations).toEqual([]);
    } finally {
      cleanup();
    }
  });
});
