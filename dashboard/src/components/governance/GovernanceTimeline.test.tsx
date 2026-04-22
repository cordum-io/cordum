import React, { act } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import type { GovernanceDecision } from "@/api/types";
import { createTestQueryClient } from "@/hooks/__tests__/test-utils";
import { GovernanceTimeline } from "./GovernanceTimeline";

function makeDecision(
  verdict: GovernanceDecision["verdict"],
  overrides: Partial<GovernanceDecision> = {},
): GovernanceDecision {
  return {
    jobId: "job-1",
    topic: "jobs.review",
    matchedRule: `rule-${verdict}`,
    ruleName: `Rule ${verdict}`,
    verdict,
    reason: `Reason for ${verdict}`,
    constraints: {
      maxInvocations: 3,
      allowedDomains: ["cordum.io"],
      maskedFields: ["token"],
      rateLimit: { requests: 10, windowSeconds: 60 },
      requireReviewer: "security-admin",
    },
    agentId: "agent-1",
    timestamp: "2026-04-20T10:00:00.000Z",
    ...overrides,
  };
}

function renderTimeline(items: GovernanceDecision[]) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const queryClient = createTestQueryClient();

  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <GovernanceTimeline items={items} />
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

function click(element: Element | null) {
  if (!element) throw new Error("Expected element to exist before clicking");
  act(() => {
    element.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
}

function keydown(element: Element | null, key: string) {
  if (!(element instanceof HTMLElement)) {
    throw new Error("Expected focusable element before keydown");
  }
  act(() => {
    element.dispatchEvent(
      new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }),
    );
  });
}

describe("GovernanceTimeline", () => {
  it("renders all five verdicts with the expected badge tones", () => {
    const { container, cleanup } = renderTimeline([
      makeDecision("allow"),
      makeDecision("deny"),
      makeDecision("constrain"),
      makeDecision("require_approval"),
      makeDecision("throttle"),
    ]);

    try {
      const badges = Array.from(
        container.querySelectorAll('[aria-label^="Verdict:"]'),
      );
      expect(badges).toHaveLength(5);
      expect(badges[0]?.className).toContain("text-[var(--color-success)]");
      expect(badges[1]?.className).toContain("text-[var(--color-governance)]");
      expect(badges[2]?.className).toContain("text-[var(--color-warning)]");
      expect(badges[3]?.className).toContain("text-[var(--color-violet-500)]");
      expect(badges[4]?.className).toContain("text-muted-foreground");
    } finally {
      cleanup();
    }
  });

  it("expands and collapses the constraints section", () => {
    const { container, cleanup } = renderTimeline([makeDecision("allow")]);

    try {
      expect(container.textContent).toContain("Max invocations");

      const toggle = Array.from(container.querySelectorAll("button")).find(
        (button) => button.textContent?.includes("Rule allow"),
      );
      click(toggle ?? null);
      expect(container.textContent).not.toContain("Max invocations");

      click(toggle ?? null);
      expect(container.textContent).toContain("Max invocations");
    } finally {
      cleanup();
    }
  });

  it("renders approval status when present", () => {
    const { container, cleanup } = renderTimeline([
      makeDecision("require_approval", { approvalStatus: "pending" }),
    ]);

    try {
      expect(container.textContent).toContain("Approval pending");
    } finally {
      cleanup();
    }
  });

  it("supports arrow-key navigation between rail nodes", () => {
    const { container, cleanup } = renderTimeline([
      makeDecision("allow"),
      makeDecision("deny"),
    ]);

    try {
      const nodes = Array.from(
        container.querySelectorAll('li[tabindex="0"]'),
      ) as HTMLElement[];
      expect(nodes).toHaveLength(2);

      nodes[0]?.focus();
      keydown(nodes[0], "ArrowDown");
      expect(document.activeElement).toBe(nodes[1]);

      keydown(nodes[1], "ArrowUp");
      expect(document.activeElement).toBe(nodes[0]);
    } finally {
      cleanup();
    }
  });

  it("shows rule, verdict, reason, and constraints for each governance decision", () => {
    const items = [
      makeDecision("allow", { ruleName: "Allow safe requests" }),
      makeDecision("deny", {
        ruleName: "Block unsafe requests",
        matchedRule: "rule-deny",
      }),
    ];
    const { container, cleanup } = renderTimeline(items);

    try {
      const toggles = Array.from(container.querySelectorAll("button")).filter(
        (button) =>
          button.textContent?.includes("Allow safe requests") ||
          button.textContent?.includes("Block unsafe requests"),
      );

      click(toggles[1] ?? null);

      expect(container.textContent).toContain("Allow safe requests");
      expect(container.textContent).toContain("Block unsafe requests");
      expect(container.textContent).toContain("Reason for allow");
      expect(container.textContent).toContain("Reason for deny");
      expect(container.textContent).toContain("Constraints");
    } finally {
      cleanup();
    }
  });

  it("renders 200 decisions without extra layout thrash", () => {
    const items = Array.from({ length: 200 }, (_, index) =>
      makeDecision(index % 2 === 0 ? "allow" : "deny", {
        matchedRule: `rule-${index}`,
        ruleName: `Rule ${index}`,
        timestamp: new Date(
          Date.UTC(2026, 3, 20, 10, 0, index),
        ).toISOString(),
      }),
    );

    let renderCount = 0;
    function Harness() {
      renderCount += 1;
      return <GovernanceTimeline items={items} />;
    }

    const container = document.createElement("div");
    document.body.appendChild(container);
    const root = createRoot(container);
    const queryClient = createTestQueryClient();

    try {
      act(() => {
        root.render(
          <QueryClientProvider client={queryClient}>
            <Harness />
          </QueryClientProvider>,
        );
      });

      expect(container.querySelectorAll('li[tabindex="0"]')).toHaveLength(200);
      expect(renderCount).toBeLessThanOrEqual(2);
    } finally {
      act(() => root.unmount());
      container.remove();
      queryClient.clear();
    }
  });
});
