import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TopicRegistration, TopicsResponse } from "@/api/types";
import TopicsPage from "./TopicsPage";

const { hookState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

  return {
    hookState: {
      data: { items: [] as TopicRegistration[] } as TopicsResponse,
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
  };
});

vi.mock("@/hooks/useTopics", () => ({
  useTopics: () => ({
    data: hookState.data,
    isLoading: hookState.isLoading,
    isError: hookState.isError,
    error: hookState.error,
    refetch: hookState.refetch,
  }),
}));

function topic(overrides: Partial<TopicRegistration> = {}): TopicRegistration {
  return {
    name: "job.default",
    pool: "default",
    inputSchemaId: "schema.input",
    outputSchemaId: "schema.output",
    packId: "pack-default",
    requires: [],
    riskTags: [],
    status: "active",
    activeWorkers: 2,
    ...overrides,
  };
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/topics"]}>
        <TopicsPage />
      </MemoryRouter>,
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

function click(element: Element | null) {
  if (!element) {
    throw new Error("Expected element to exist before clicking");
  }
  act(() => {
    element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

beforeEach(() => {
  document.body.innerHTML = "";
  hookState.data = { items: [] };
  hookState.isLoading = false;
  hookState.isError = false;
  hookState.error = null;
  hookState.refetch = vi.fn();
});

describe("TopicsPage", () => {
  it("renders topic rows and marks zero-worker topics as degraded", () => {
    hookState.data = {
      items: [
        topic({
          name: "job.alpha",
          pool: "pool-a",
          inputSchemaId: "schema.alpha.in",
          outputSchemaId: "schema.alpha.out",
          packId: "pack-alpha",
          activeWorkers: 3,
        }),
        topic({
          name: "job.beta",
          pool: "pool-b",
          packId: "pack-beta",
          activeWorkers: 0,
          riskTags: ["external"],
        }),
      ],
    };

    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("job.alpha");
      expect(container.textContent).toContain("job.beta");
      expect(container.textContent).toContain("Degraded");
      expect(container.textContent).toContain("registry:active");

      const hrefs = Array.from(container.querySelectorAll("a")).map((link) =>
        link.getAttribute("href"),
      );
      expect(hrefs).toContain("/schemas/schema.alpha.in");
      expect(hrefs).toContain("/schemas/schema.alpha.out");
      expect(hrefs).toContain("/packs/pack-alpha");
      expect(hrefs).toContain("/agents?pool=pool-b&topic=job.beta");
    } finally {
      cleanup();
    }
  });

  it("shows the empty state when no topics are registered", () => {
    hookState.data = { items: [] };

    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("No topics registered");
      expect(container.textContent).toContain(
        "Install a pack or use `cordumctl topic create`",
      );
    } finally {
      cleanup();
    }
  });

  it("shows an error state with retry action when the query fails", () => {
    hookState.isError = true;
    hookState.error = new Error("Gateway offline");

    const { container, cleanup } = renderPage();
    try {
      expect(container.textContent).toContain("Topic registry unavailable");
      expect(container.textContent).toContain("Gateway offline");

      const retryButton = Array.from(container.querySelectorAll("button")).find((button) =>
        button.textContent?.includes("Retry"),
      );
      click(retryButton ?? null);
      expect(hookState.refetch).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });
});
