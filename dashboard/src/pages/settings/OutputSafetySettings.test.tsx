import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import OutputSafetySettings from "./OutputSafetySettings";
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
        <MemoryRouter initialEntries={["/settings/output-safety"]}>
          <Routes>
            <Route path="/settings/output-safety" element={<OutputSafetySettings />} />
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

describe("OutputSafetySettings page", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000123");
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders with mocked data", async () => {
    mockFetch([
      { match: "/config", method: "GET", body: { output_policy: { enabled: true } } },
      { match: "/status", method: "GET", body: { nats: { connected: true }, redis: { ok: true } } },
      { match: "/policy/bundles", method: "GET", body: { items: [] } },
      { match: "/policy/audit", method: "GET", body: { items: [] } },
      {
        match: "/policy/output/stats",
        method: "GET",
        body: { total_checks_24h: 42, quarantined_24h: 3, avg_latency_ms: 9.5 },
      },
    ]);

    const view = renderPage();
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Output Safety Scanning");
      expect(view.container.textContent).toContain("Per-Topic Overrides");
      expect(view.container.textContent).toContain("Checks (24h)");
    });
    view.unmount();
  });

  it("updates config through API when global toggle is changed and saved", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/config",
        method: "GET",
        body: { output_policy: { enabled: false, fail_mode: "open" } },
      },
      { match: "/status", method: "GET", body: {} },
      { match: "/policy/bundles", method: "GET", body: { items: [] } },
      { match: "/policy/audit", method: "GET", body: { items: [] } },
      { match: "/policy/output/stats", method: "GET", body: {} },
      { match: "/config", method: "PUT", body: {} },
    ]);

    const view = renderPage();
    await view.waitFor(() => {
      expect(view.container.textContent).toContain("Output Safety Scanning");
    });

    const toggle = view.container.querySelector('button[role="switch"]') as HTMLButtonElement | null;
    expect(toggle).toBeTruthy();
    await act(async () => {
      toggle?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    const saveButton = Array.from(view.container.querySelectorAll("button")).find((btn) =>
      btn.textContent?.includes("Save Output Safety Settings"),
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
      expect(data.output_policy_enabled).toBe(true);
    });

    view.unmount();
  });
});
