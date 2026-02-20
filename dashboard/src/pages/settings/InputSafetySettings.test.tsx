import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import InputSafetySettings from "./InputSafetySettings";
import { mockFetch } from "../../hooks/__tests__/test-utils";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { addToastMock, loggerMock } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

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

vi.mock("../../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../../state/toast", () => {
  const state = {
    toasts: [],
    addToast: addToastMock,
    dismissToast: vi.fn(),
  };
  const hook = ((selector?: (s: typeof state) => unknown) =>
    selector ? selector(state) : state) as ((
    selector?: (s: typeof state) => unknown,
  ) => unknown) & { getState: () => typeof state };
  hook.getState = () => state;
  return { useToastStore: hook };
});

vi.mock("../../lib/logger", () => ({
  logger: loggerMock,
}));

interface RenderResult {
  container: HTMLDivElement;
  queryClient: QueryClient;
  unmount: () => void;
  waitFor: (assertion: () => void, timeoutMs?: number) => Promise<void>;
}

function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderPage(): RenderResult {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root: Root = createRoot(container);
  const queryClient = createTestQueryClient();

  act(() => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={["/settings/input-safety"]}>
          <Routes>
            <Route path="/settings/input-safety" element={<InputSafetySettings />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });

  async function waitFor(assertion: () => void, timeoutMs = 2500): Promise<void> {
    const start = Date.now();
    while (true) {
      try {
        assertion();
        return;
      } catch (error) {
        if (Date.now() - start >= timeoutMs) throw error;
        await act(async () => {
          await new Promise((resolve) => setTimeout(resolve, 10));
        });
      }
    }
  }

  return {
    container,
    queryClient,
    unmount: () => {
      act(() => {
        root.unmount();
      });
      queryClient.clear();
      container.remove();
    },
    waitFor,
  };
}

describe("InputSafetySettings page", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000456");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders with default config", async () => {
    mockFetch([
      { match: "/config", method: "GET", body: { input_policy: { fail_mode: "closed" } } },
      { match: "/status", method: "GET", body: { nats: { connected: true }, redis: { ok: true } } },
    ]);

    const view = renderPage();
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Input Safety");
      expect(view.container.textContent).toContain("Fail Mode");
      expect(view.container.textContent).toContain("How It Works");
    });
    view.unmount();
  });

  it("shows warning banner when fail-open is selected", async () => {
    mockFetch([
      { match: "/config", method: "GET", body: { input_policy: { fail_mode: "open" } } },
      { match: "/status", method: "GET", body: {} },
    ]);

    const view = renderPage();
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Fail-open mode bypasses safety checks");
    });
    view.unmount();
  });

  it("saves config through API when changed", async () => {
    const fetchSpy = mockFetch([
      { match: "/config", method: "GET", body: { input_policy: { fail_mode: "closed" } } },
      { match: "/status", method: "GET", body: {} },
      { match: "/config", method: "PUT", body: {} },
    ]);

    const view = renderPage();
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Input Safety");
    });

    // Change the select to "open"
    const select = view.container.querySelector("select") as HTMLSelectElement | null;
    expect(select).toBeTruthy();
    await act(async () => {
      if (select) {
        select.value = "open";
        select.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });

    const saveButton = Array.from(view.container.querySelectorAll("button")).find((btn) =>
      btn.textContent?.includes("Save Input Safety Settings"),
    ) as HTMLButtonElement | undefined;
    expect(saveButton).toBeTruthy();
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    await view.waitFor(() => {
      const putCall = fetchSpy.mock.calls.find((call) => {
        const [, init] = call as [string, RequestInit];
        return init.method === "PUT";
      });
      expect(putCall).toBeTruthy();
      const [, putInit] = putCall as [string, RequestInit];
      const payload = JSON.parse(String(putInit.body)) as Record<string, unknown>;
      const data = payload.data as Record<string, unknown>;
      expect(data.policy_check_fail_mode).toBe("open");
    });

    view.unmount();
  });
});
