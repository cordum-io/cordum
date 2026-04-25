import { act } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/api/client";
import SettingsAuditExportPage from "./SettingsAuditExportPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { apiState, hookState, toastState } = vi.hoisted(() => ({
  apiState: {
    get: vi.fn(),
    post: vi.fn(),
    del: vi.fn(),
  },
  hookState: {
    license: {} as any,
  },
  toastState: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
  return {
    ...actual,
    get: apiState.get,
    post: apiState.post,
    del: apiState.del,
  };
});

vi.mock("@/hooks/useLicense", () => ({
  useLicense: () => hookState.license,
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
        <MemoryRouter initialEntries={["/settings/audit-export"]}>
          <SettingsAuditExportPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      queryClient.clear();
      container.remove();
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

describe("SettingsAuditExportPage", () => {
  beforeEach(() => {
    apiState.get.mockReset();
    apiState.post.mockReset();
    apiState.del.mockReset();
    toastState.success.mockReset();
    toastState.error.mockReset();

    hookState.license = {
      data: {
        plan: "enterprise",
        entitlements: {
          siemExport: false,
          auditExport: false,
          legalHold: true,
        },
      },
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    };
  });

  it("shows a legal-hold error banner with retry instead of the empty state", async () => {
    const refetchError = new ApiError(503, "Service unavailable");
    apiState.get.mockRejectedValue(refetchError);

    const { container, cleanup } = renderPage();

    try {
      await waitFor(() => {
        expect(apiState.get).toHaveBeenCalledWith("/audit/legal-holds");
      });

      click(container.querySelector('button[aria-label="Legal Hold"]'));

      await waitFor(() => {
        expect(container.textContent).toContain("Service temporarily unavailable");
        expect(container.textContent).toContain("The system is under maintenance or overloaded. Try again shortly.");
        expect(container.textContent).toContain("Retry");
      });
      expect(container.textContent).not.toContain("No active legal holds");

      apiState.get.mockResolvedValueOnce({ holds: [] });
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Retry"),
        ) ?? null,
      );

      await waitFor(() => {
        expect(container.textContent).toContain("No active legal holds");
      });
    } finally {
      cleanup();
    }
  });
});
