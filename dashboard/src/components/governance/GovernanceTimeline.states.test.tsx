import React, { act } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import type { GovernanceDecision } from "@/api/types";
import { createTestQueryClient } from "@/hooks/__tests__/test-utils";
import { GovernanceTimeline } from "./GovernanceTimeline";

function makeDecision(): GovernanceDecision {
  return {
    jobId: "job-1",
    topic: "jobs.review",
    matchedRule: "rule-1",
    ruleName: "Rule 1",
    verdict: "allow",
    reason: "Allowed",
    agentId: "agent-1",
    timestamp: "2026-04-20T10:00:00.000Z",
  };
}

function renderTimeline(
  props: React.ComponentProps<typeof GovernanceTimeline>,
) {
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

function click(element: Element | null) {
  if (!element) throw new Error("Expected element to exist before clicking");
  act(() => {
    element.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
}

describe("GovernanceTimeline states", () => {
  it("renders three layout-stable skeleton nodes while loading", () => {
    const { container, cleanup } = renderTimeline({ isLoading: true });

    try {
      expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
      expect(container.querySelectorAll(".skeleton")).toHaveLength(9);
    } finally {
      cleanup();
    }
  });

  it("renders an error banner with a working retry button", () => {
    const onRetry = vi.fn();
    const { container, cleanup } = renderTimeline({
      error: new Error("backend unavailable"),
      onRetry,
    });

    try {
      expect(container.textContent).toContain("Unable to load governance decisions");
      expect(container.textContent).toContain("backend unavailable");
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Retry"),
        ) ?? null,
      );
      expect(onRetry).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("renders the empty state copy and respects emptyHint overrides", () => {
    const { container, cleanup } = renderTimeline({
      items: [],
      emptyHint: "This workflow run has not evaluated any policy rules yet.",
    });

    try {
      expect(container.textContent).toContain("No governance decisions yet");
      expect(container.textContent).toContain(
        "This workflow run has not evaluated any policy rules yet.",
      );
    } finally {
      cleanup();
    }
  });

  it("shows a load-more button and wires it to the callback", () => {
    const onLoadMore = vi.fn();
    const { container, cleanup } = renderTimeline({
      items: [makeDecision()],
      hasNextPage: true,
      onLoadMore,
    });

    try {
      const button = Array.from(container.querySelectorAll("button")).find(
        (candidate) => candidate.textContent?.includes("Load more"),
      );
      click(button ?? null);
      expect(onLoadMore).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });

  it("disables load more while the next page is fetching", () => {
    const { container, cleanup } = renderTimeline({
      items: [makeDecision()],
      hasNextPage: true,
      isFetchingNextPage: true,
    });

    try {
      const button = Array.from(container.querySelectorAll("button")).find(
        (candidate) => candidate.textContent?.includes("Loading"),
      ) as HTMLButtonElement | undefined;
      expect(button?.disabled).toBe(true);
    } finally {
      cleanup();
    }
  });
});
