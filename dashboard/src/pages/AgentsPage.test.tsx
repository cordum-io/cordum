import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Worker } from "@/api/types";
import AgentsPage from "./AgentsPage";

const { hookState } = vi.hoisted(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

  return {
    hookState: {
      workers: [] as Worker[],
      isLoading: false,
      isError: false,
      error: null as Error | null,
      refetch: vi.fn(),
    },
  };
});

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: () => ({
      data: hookState.workers,
      isLoading: hookState.isLoading,
      isError: hookState.isError,
      error: hookState.error,
      refetch: hookState.refetch,
    }),
  };
});

vi.mock("@/components/agents/WorkerDetailDrawer", () => ({
  WorkerDetailDrawer: () => null,
}));

vi.mock("@/components/agents/PoolGroupedView", () => ({
  PoolGroupedView: () => null,
}));

function makeWorker(overrides: Partial<Worker> = {}): Worker {
  return {
    id: "worker-1",
    status: "idle",
    pool: "pool-a",
    capabilities: ["job.alpha"],
    activeJobs: 0,
    capacity: 2,
    lastHeartbeat: "2026-01-01T00:00:00.000Z",
    ...overrides,
  } as Worker;
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/agents?pool=pool-a&topic=job.alpha"]}>
        <AgentsPage />
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
  if (!element) throw new Error("Expected element to exist before clicking");
  act(() => {
    element.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
}

function changeInput(element: HTMLInputElement, value: string) {
  act(() => {
    const setter = Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      "value",
    )?.set;
    setter?.call(element, value);
    element.dispatchEvent(new Event("input", { bubbles: true }));
    element.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

describe("AgentsPage filter reset", () => {
  beforeEach(() => {
    hookState.workers = [makeWorker()];
    hookState.isLoading = false;
    hookState.isError = false;
    hookState.error = null;
    hookState.refetch = vi.fn();
  });

  it("clears the search input when the topic coverage filter is cleared", () => {
    const { container, cleanup } = renderPage();
    try {
      const searchInput = container.querySelector(
        'input[placeholder="Search agents..."]',
      ) as HTMLInputElement | null;
      expect(searchInput).not.toBeNull();

      changeInput(searchInput!, "pool");
      expect(searchInput?.value).toBe("pool");

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Clear filter"),
        ) ?? null,
      );

      expect(searchInput?.value).toBe("");
    } finally {
      cleanup();
    }
  });
});
