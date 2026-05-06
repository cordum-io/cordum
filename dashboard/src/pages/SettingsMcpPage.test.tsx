import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import SettingsMcpPage from "./SettingsMcpPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState, toastState } = vi.hoisted(() => ({
  hookState: {
    mcpConfig: { data: null as unknown, isLoading: false },
    mcpStatus: { data: undefined as unknown },
    mcpTools: { data: [] as unknown[], isLoading: false },
    mcpResources: { data: [] as unknown[], isLoading: false },
    setMcpConfig: { mutate: vi.fn(), isPending: false },
  },
  toastState: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("framer-motion", () => ({
  motion: {
    div: ({ children, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div {...props}>{children}</div>,
  },
}));

vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => toastState.success(...args),
    error: (...args: unknown[]) => toastState.error(...args),
  },
}));

vi.mock("@/hooks/useSettings", () => ({
  useMcpConfig: () => hookState.mcpConfig,
  useMcpStatus: () => hookState.mcpStatus,
  useMcpTools: () => hookState.mcpTools,
  useMcpResources: () => hookState.mcpResources,
  useSetMcpConfig: () => hookState.setMcpConfig,
}));

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);

  hookState.mcpConfig = {
    data: {
      enabled: true,
      transport: "http",
      port: 8787,
      requireAuth: true,
      allowedOrigins: ["http://localhost:3000"],
      tools: {},
      resources: {},
    },
    isLoading: false,
  };
  hookState.mcpStatus = {
    data: {
      running: true,
      connectedClients: 2,
      uptime: 3661,
      transport: "http",
      enabledTools: 1,
      enabledResources: 1,
    },
  };
  hookState.mcpTools = {
    data: [
      { name: "list_jobs", description: "List jobs", enabled: true, inputSchema: {} },
    ],
    isLoading: false,
  };
  hookState.mcpResources = {
    data: [
      {
        uri: "cordum://jobs",
        name: "Jobs",
        description: "Job stream",
        enabled: true,
        mimeType: "application/json",
      },
    ],
    isLoading: false,
  };
  toastState.success.mockReset();
  toastState.error.mockReset();
  hookState.setMcpConfig.mutate.mockReset();
  hookState.setMcpConfig.isPending = false;
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { hostname: "dashboard.local" },
  });
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: vi.fn().mockResolvedValue(undefined),
    },
  });
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function renderPage() {
  act(() => {
    root.render(<SettingsMcpPage />);
  });
}

function clickByText(text: string) {
  const element = Array.from(container.querySelectorAll("button")).find((button) =>
    button.textContent?.includes(text),
  );
  if (!element) {
    throw new Error(`Could not find button with text: ${text}`);
  }
  act(() => {
    element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  });
}

describe("SettingsMcpPage", () => {
  it("renders a loading skeleton while MCP settings are loading", () => {
    hookState.mcpConfig = { data: null, isLoading: true };
    hookState.mcpTools = { data: [], isLoading: true };
    hookState.mcpResources = { data: [], isLoading: true };

    renderPage();

    expect(container.querySelector('[data-testid="settings-mcp-loading"]')).not.toBeNull();
  });

  it("renders an error banner when configuration is unavailable", () => {
    hookState.mcpConfig = { data: null, isLoading: false };
    renderPage();
    expect(container.textContent).toContain("Failed to load MCP configuration");
  });

  it("shows the disabled-server notice when MCP is turned off", () => {
    hookState.mcpConfig = {
      data: {
        enabled: false,
        transport: "http",
        port: 8787,
        requireAuth: true,
        allowedOrigins: [],
        tools: {},
        resources: {},
      },
      isLoading: false,
    };

    renderPage();
    expect(container.textContent).toContain("MCP server is disabled");
    expect(container.textContent).toContain("Enable runtime");
  });

  it("enables MCP runtime from the server panel action", () => {
    hookState.mcpConfig = {
      data: {
        enabled: false,
        transport: "http",
        port: 8787,
        requireAuth: true,
        allowedOrigins: [],
        tools: {},
        resources: {},
      },
      isLoading: false,
    };

    renderPage();
    clickByText("Enable runtime");
    expect(hookState.setMcpConfig.mutate).toHaveBeenCalledWith(
      { enabled: true },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
  });

  it("disables MCP runtime from the server panel action", () => {
    renderPage();
    clickByText("Disable runtime");
    expect(hookState.setMcpConfig.mutate).toHaveBeenCalledWith(
      { enabled: false },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
  });

  it("toggles the server panel details", () => {
    renderPage();
    expect(container.textContent).toContain("List jobs");
    clickByText("Cordum MCP Server");
    expect(container.textContent).not.toContain("List jobs");
    clickByText("Cordum MCP Server");
    expect(container.textContent).toContain("List jobs");
  });

  it("copies the MCP URL through the shared action row", async () => {
    renderPage();
    clickByText("Copy URL");
    await act(async () => {
      await Promise.resolve();
    });

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith("http://dashboard.local:8787");
    expect(toastState.success).toHaveBeenCalledWith("MCP URL copied");
  });
});
