import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import MCPPage from "./MCPPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    usage: {
      data: {
        cells: [
          {
            agent_id: "agent-1",
            tool_name: "tool-x",
            count: 3,
            allow_count: 2,
            deny_count: 1,
            approval_required_count: 1,
            p50_latency_ms: 10,
            p99_latency_ms: 20,
            last_invoked_at_ms: 1_700_000_000_000,
          },
        ],
        total_calls: 3,
        window_ms: 86_400_000,
        truncated_at_max: false,
      },
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    },
    pending: { data: [{ id: "a-1" }, { id: "a-2" }], isLoading: false, isError: false },
  },
}));

vi.mock("@/hooks/useMcp", async () => {
  const actual = await vi.importActual<typeof import("@/hooks/useMcp")>("@/hooks/useMcp");
  return {
    ...actual,
    useMcpUsage: () => hookState.usage,
    useMcpPendingApprovals: () => hookState.pending,
    useMcpOutbound: () => ({
      data: { pages: [{ entries: [], next_cursor: "", truncated_at_max: false }] },
      isLoading: false,
      isError: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
      refetch: vi.fn(),
    }),
    useApproveMcp: () => ({ mutate: vi.fn(), isPending: false }),
    useRejectMcp: () => ({ mutate: vi.fn(), isPending: false }),
  };
});

vi.mock("../state/config", () => ({
  useConfigStore: <T,>(selector: (s: { principalId: string }) => T) =>
    selector({ principalId: "user-1" }),
}));

// PromptCatalogMount imports useMcpPrompts from useMcpCatalog. In the
// page test we want to exercise the mount wiring, not the live fetch —
// stub the hook to a deterministic empty catalogue so the existing
// section-render assertions stay focused.
vi.mock("@/hooks/useMcpCatalog", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/useMcpCatalog")>(
      "@/hooks/useMcpCatalog",
    );
  return {
    ...actual,
    useMcpPrompts: () => ({
      data: [],
      isLoading: false,
      error: null,
    }),
  };
});

vi.mock("@/components/layout/PageHeader", () => ({
  PageHeader: ({ title }: { title: string }) => <h1>{title}</h1>,
}));

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    media: "",
    onchange: null,
    dispatchEvent: () => false,
  }));
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

function render() {
  // PromptCatalogMount now uses useMcpPrompts (React Query) so the
  // page must be rendered inside a QueryClientProvider. Retry disabled
  // so a mock-fetch failure fails fast instead of spinning.
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  act(() => {
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <MCPPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
}

describe("MCPPage", () => {
  it("renders the four governance sections", () => {
    render();
    expect(container.querySelector('[data-testid="mcp-summary"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-heatmap-section"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-approvals-section"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-outbound-section"]')).toBeTruthy();
  });

  it("summarises totals using the live usage payload", () => {
    render();
    const summary = container.querySelector('[data-testid="mcp-summary"]')?.textContent ?? "";
    expect(summary).toContain("3"); // tool calls
    expect(summary).toContain("2"); // pending approvals
    expect(summary).toContain("1"); // denied / approval required
  });

  it("offers preset windows and approval status tabs", () => {
    render();
    expect(container.querySelector('[data-testid="mcp-range-1h"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-range-7d"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-approval-tab-pending"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-approval-tab-rejected"]')).toBeTruthy();
  });
});
