import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import { renderWithProviders, waitFor } from "@/test-utils/render";
import { http, HttpResponse, server } from "@/test-utils/msw";
import InputSafetySettings from "./InputSafetySettings";

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
  registerQueryClient: vi.fn(),
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
  container: HTMLElement;
  queryClient: QueryClient;
  unmount: () => void;
}

function renderPage(): RenderResult {
  const view = renderWithProviders(<InputSafetySettings />, {
    initialEntries: ["/settings/input-safety"],
  });
  return view;
}

function mockInputSafetyEndpoints(
  configBody: Record<string, unknown>,
  statusBody: Record<string, unknown> = {},
): void {
  server.use(
    http.get("*/api/v1/config", () => HttpResponse.json(configBody)),
    http.get("*/api/v1/status", () => HttpResponse.json(statusBody)),
  );
}

describe("InputSafetySettings page", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    server.resetHandlers();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue("00000000-0000-0000-0000-000000000456");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders with default config", async () => {
    mockInputSafetyEndpoints(
      { input_policy: { fail_mode: "closed" } },
      { nats: { connected: true }, redis: { ok: true } },
    );

    const view = renderPage();
    await waitFor(() => {
      expect(view.container.textContent).toContain("Input Safety");
      expect(view.container.textContent).toContain("Fail Mode");
      expect(view.container.textContent).toContain("How It Works");
    });
    view.unmount();
  });

  it("shows warning banner when fail-open is selected", async () => {
    mockInputSafetyEndpoints({ input_policy: { fail_mode: "open" } });

    const view = renderPage();
    await waitFor(() => {
      expect(view.container.textContent).toContain("Fail-open mode bypasses safety checks");
    });
    view.unmount();
  });

  it("saves config through API when changed", async () => {
    let putPayload: Record<string, unknown> | undefined;
    mockInputSafetyEndpoints({ input_policy: { fail_mode: "closed" } });
    server.use(
      http.put("*/api/v1/config", async ({ request }) => {
        putPayload = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({});
      }),
    );

    const view = renderPage();
    await waitFor(() => {
      expect(view.container.textContent).toContain("Input Safety");
    });
    await waitFor(() => {
      expect(view.queryClient.isFetching()).toBe(0);
    });

    // Change the select to "open"
    const select = view.container.querySelector("select") as HTMLSelectElement | null;
    expect(select).toBeTruthy();
    if (select) {
      select.value = "open";
      select.dispatchEvent(new Event("input", { bubbles: true }));
      select.dispatchEvent(new Event("change", { bubbles: true }));
    }

    await waitFor(() => {
      expect(select?.value).toBe("open");
    });

    await waitFor(() => {
      expect(view.container.textContent).toContain("Fail-open mode bypasses safety checks");
      const liveSaveButton = Array.from(view.container.querySelectorAll("button")).find((btn) =>
        btn.textContent?.includes("Save Input Safety Settings"),
      ) as HTMLButtonElement | undefined;
      expect(liveSaveButton).toBeTruthy();
      expect(liveSaveButton?.disabled).toBe(false);
    }, { timeout: 5000 });
    await act(async () => {
      const liveSaveButton = Array.from(view.container.querySelectorAll("button")).find((btn) =>
        btn.textContent?.includes("Save Input Safety Settings"),
      ) as HTMLButtonElement | undefined;
      expect(liveSaveButton).toBeTruthy();
      liveSaveButton!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    await waitFor(() => {
      expect(putPayload).toBeTruthy();
      const data = putPayload?.data as Record<string, unknown>;
      expect(data.policy_check_fail_mode).toBe("open");
    });

    view.unmount();
  }, 15000); // CI flake guard: outer 5s test default was racing the inner 5000ms waitFor on slow runners.
});
