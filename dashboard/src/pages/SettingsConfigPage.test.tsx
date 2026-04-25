import { act } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import SettingsConfigPage from "./SettingsConfigPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { apiState, toastState } = vi.hoisted(() => ({
  apiState: {
    get: vi.fn(),
    post: vi.fn(),
  },
  toastState: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/api/client", () => ({
  get: apiState.get,
  post: apiState.post,
}));

vi.mock("sonner", () => ({
  toast: {
    success: toastState.success,
    error: toastState.error,
  },
}));

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderPage() {
  const queryClient = createTestQueryClient();
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/settings/config"]}>
          <SettingsConfigPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  return {
    container,
    queryClient,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
      queryClient.clear();
    },
  };
}

async function waitFor(assertion: () => void, timeoutMs = 2000) {
  const start = Date.now();
  while (true) {
    try {
      assertion();
      return;
    } catch (error) {
      if (Date.now() - start >= timeoutMs) {
        throw error;
      }
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    }
  }
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

describe("SettingsConfigPage save confirmation", () => {
  beforeEach(() => {
    apiState.get.mockReset();
    apiState.post.mockReset();
    toastState.success.mockReset();
    toastState.error.mockReset();

    apiState.get.mockResolvedValue({
      cluster_name: "production",
      log_level: "info",
      safety_enabled: true,
      safety_fail_mode: "block",
      max_concurrent_jobs: 100,
      job_timeout_seconds: 300,
      job_retention_days: 90,
      audit_retention_days: 365,
    });
    apiState.post.mockResolvedValue({});
  });

  it("asks for confirmation before saving production config changes", async () => {
    const { container, cleanup } = renderPage();
    try {
      await waitFor(() => {
        expect(
          container.querySelector('input[type="text"]'),
        ).not.toBeNull();
      });

      const clusterNameInput = container.querySelector(
        'input[type="text"]',
      ) as HTMLInputElement | null;
      expect(clusterNameInput).not.toBeNull();

      changeInput(clusterNameInput!, "staging");

      await waitFor(() => {
        expect(container.textContent).toContain("You have unsaved changes");
      });

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Save Changes"),
        ) ?? null,
      );

      expect(container.textContent).toContain(
        "Apply production configuration changes?",
      );
      expect(apiState.post).not.toHaveBeenCalled();
      expect(container.textContent).toContain("cluster_name");

      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Save configuration"),
        ) ?? null,
      );

      await waitFor(() => {
        expect(apiState.post).toHaveBeenCalledTimes(1);
      });
    } finally {
      cleanup();
    }
  });
});
