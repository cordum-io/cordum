import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MCPOutboundLog } from "./MCPOutboundLog";
import type { MCPOutboundEntry, MCPOutboundResponse } from "../../api/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    pages: [] as MCPOutboundResponse[],
    isLoading: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
    refetch: vi.fn(),
  },
}));

vi.mock("../../hooks/useMcp", async () => {
  const actual = await vi.importActual<typeof import("../../hooks/useMcp")>(
    "../../hooks/useMcp",
  );
  return {
    ...actual,
    useMcpOutbound: () => ({
      data: { pages: hookState.pages },
      isLoading: hookState.isLoading,
      isError: hookState.isError,
      hasNextPage: hookState.hasNextPage,
      isFetchingNextPage: hookState.isFetchingNextPage,
      fetchNextPage: hookState.fetchNextPage,
      refetch: hookState.refetch,
    }),
  };
});

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;

function entry(overrides: Partial<MCPOutboundEntry> = {}): MCPOutboundEntry {
  return {
    ts_ms: 1_700_000_000_000,
    stream_id: overrides.stream_id ?? "1-0",
    agent_id: "agent-1",
    tool_name: "search",
    target_server: "github",
    signature_status: "verified",
    signature_key_id: "k-1",
    latency_ms: 42,
    result_type: "ok",
    event_hash: "0xabc",
    ...overrides,
  };
}

function makePage(entries: MCPOutboundEntry[], opts: Partial<MCPOutboundResponse> = {}): MCPOutboundResponse {
  return { entries, next_cursor: opts.next_cursor ?? "", truncated_at_max: opts.truncated_at_max ?? false };
}

beforeEach(() => {
  hookState.pages = [];
  hookState.isLoading = false;
  hookState.isError = false;
  hookState.hasNextPage = false;
  hookState.isFetchingNextPage = false;
  hookState.fetchNextPage.mockReset();
  hookState.refetch.mockReset();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(node: React.ReactElement) {
  act(() => {
    root.render(<MemoryRouter>{node}</MemoryRouter>);
  });
}

describe("MCPOutboundLog", () => {
  it("renders the empty state when no events flow yet", () => {
    render(<MCPOutboundLog filters={{}} />);
    expect(container.querySelector('[data-testid="mcp-outbound-empty"]')).toBeTruthy();
    expect(container.textContent).toContain("CORDUM_MCP_OUTBOUND_SIGNING_KEY");
  });

  it("renders one row per entry with the signature status", () => {
    hookState.pages = [
      makePage([
        entry({ stream_id: "1-0", signature_status: "verified" }),
        entry({ stream_id: "2-0", signature_status: "invalid", agent_id: "agent-2" }),
        entry({ stream_id: "3-0", signature_status: "unverified", target_server: "jira" }),
      ]),
    ];
    render(<MCPOutboundLog filters={{}} />);
    expect(container.querySelector('[data-testid="mcp-outbound-row-1-0"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-outbound-row-2-0"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="mcp-outbound-row-3-0"]')).toBeTruthy();
    expect(
      container
        .querySelector('[data-testid="mcp-outbound-sig-2-0"]')
        ?.getAttribute("data-status"),
    ).toBe("invalid");
  });

  it("annotates the signature badge with key id via aria-label", () => {
    hookState.pages = [makePage([entry({ stream_id: "1-0", signature_key_id: "k-7" })])];
    render(<MCPOutboundLog filters={{}} />);
    const badge = container.querySelector('[data-testid="mcp-outbound-sig-1-0"]');
    expect(badge?.getAttribute("aria-label")).toContain("Signature verified");
    expect(badge?.getAttribute("aria-label")).toContain("k-7");
  });

  it("shows truncation hint when any page reports truncated_at_max", () => {
    hookState.pages = [makePage([entry()], { truncated_at_max: true })];
    render(<MCPOutboundLog filters={{}} />);
    expect(container.textContent).toContain("results may be partial");
  });

  it("paginates via Load more when hasNextPage is true", () => {
    hookState.pages = [makePage([entry()])];
    hookState.hasNextPage = true;
    render(<MCPOutboundLog filters={{}} />);
    const more = container.querySelector(
      '[data-testid="mcp-outbound-load-more"]',
    ) as HTMLButtonElement;
    expect(more).toBeTruthy();
    act(() => {
      more.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(hookState.fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it("links the agent_id cell to the agent detail page", () => {
    hookState.pages = [makePage([entry({ agent_id: "agent-5" })])];
    render(<MCPOutboundLog filters={{}} />);
    const link = container.querySelector('a[href="/agents/agent-5"]');
    expect(link).toBeTruthy();
  });

  it("surfaces a retry on error", () => {
    hookState.isError = true;
    render(<MCPOutboundLog filters={{}} />);
    const err = container.querySelector('[data-testid="mcp-outbound-error"]');
    expect(err).toBeTruthy();
    const retry = err?.querySelector("button") as HTMLButtonElement;
    act(() => {
      retry.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(hookState.refetch).toHaveBeenCalledTimes(1);
  });
});
