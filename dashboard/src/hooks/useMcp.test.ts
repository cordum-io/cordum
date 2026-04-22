// Tests for useMcp hooks. Mirrors the patterns in useStatus.test.ts:
// stub fetch with mockFetch, drive React Query via renderWithQueryClient,
// assert on the surfaced query state.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createTestQueryClient,
  mockFetch,
  renderWithQueryClient,
} from "./__tests__/test-utils";
import { useMcpOutbound, useMcpUsage } from "./useMcp";
import type { MCPOutboundResponse, MCPUsageResponse } from "../api/types";

const { mockConfigState } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "k",
    tenantId: "tenant-acme",
    principalId: "user-1",
    principalRole: "admin",
    user: null,
    logout: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({
  logger: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

let fetchMock: ReturnType<typeof mockFetch>;

beforeEach(() => {
  // Replaced inside each test below.
  fetchMock = mockFetch([]);
});

afterEach(() => {
  fetchMock.mockRestore();
});

describe("useMcpUsage", () => {
  it("calls /mcp/usage with mapped query params and surfaces the typed payload", async () => {
    const payload: MCPUsageResponse = {
      cells: [
        {
          agent_id: "a-1",
          tool_name: "tool-x",
          count: 5,
          allow_count: 4,
          deny_count: 1,
          approval_required_count: 0,
          p50_latency_ms: 12,
          p99_latency_ms: 50,
          last_invoked_at_ms: 1_700_000_000_000,
        },
      ],
      total_calls: 5,
      window_ms: 86_400_000,
      truncated_at_max: false,
    };
    fetchMock.mockRestore();
    fetchMock = mockFetch([{ match: "/api/v1/mcp/usage", body: payload }]);

    const { result, waitFor, unmount } = renderWithQueryClient(() =>
      useMcpUsage({ sinceMs: 1, untilMs: 2, agent: "a-1", tool: "tool-x" }),
    );
    await waitFor(() => {
      expect(result.current?.data).toEqual(payload);
    });
    const call = fetchMock.mock.calls[0];
    const url =
      typeof call[0] === "string"
        ? call[0]
        : call[0] instanceof URL
          ? call[0].toString()
          : (call[0] as Request).url;
    expect(url).toContain("/api/v1/mcp/usage");
    expect(url).toContain("since=1");
    expect(url).toContain("until=2");
    expect(url).toContain("agent=a-1");
    expect(url).toContain("tool=tool-x");
    unmount();
  });

  it("omits empty params from the URL", async () => {
    fetchMock.mockRestore();
    fetchMock = mockFetch([
      {
        match: "/api/v1/mcp/usage",
        body: { cells: [], total_calls: 0, window_ms: 0, truncated_at_max: false },
      },
    ]);
    const { waitFor, unmount } = renderWithQueryClient(() =>
      useMcpUsage({ agent: "", tool: undefined }),
    );
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalled();
    });
    const call = fetchMock.mock.calls[0];
    const url =
      typeof call[0] === "string" ? call[0] : (call[0] as Request).url ?? "";
    expect(url).not.toContain("agent=");
    expect(url).not.toContain("tool=");
    unmount();
  });

  it("surfaces query errors via React Query state", async () => {
    fetchMock.mockRestore();
    fetchMock = mockFetch([
      { match: "/api/v1/mcp/usage", status: 503, body: { error: "down", status: 503 } },
    ]);
    const { result, waitFor, unmount } = renderWithQueryClient(() =>
      useMcpUsage({ sinceMs: 0, untilMs: 1 }),
    );
    await waitFor(() => {
      expect(result.current?.isError).toBe(true);
    });
    unmount();
  });
});

describe("useMcpOutbound (infinite query)", () => {
  it("returns the first page payload", async () => {
    const page1: MCPOutboundResponse = {
      entries: [
        {
          ts_ms: 1,
          stream_id: "1-0",
          agent_id: "a",
          tool_name: "t",
          target_server: "github",
          signature_status: "verified",
        },
      ],
      next_cursor: "1-0",
      truncated_at_max: false,
    };
    fetchMock.mockRestore();
    fetchMock = mockFetch([{ match: "/api/v1/mcp/outbound", body: page1 }]);

    const { result, waitFor, unmount } = renderWithQueryClient(() =>
      useMcpOutbound({
        sinceMs: 0,
        untilMs: 100,
        agent: "a",
        sigStatus: "verified",
      }),
    );
    await waitFor(() => {
      expect(result.current?.data?.pages?.[0]).toEqual(page1);
    });

    const call = fetchMock.mock.calls[0];
    const url =
      typeof call[0] === "string" ? call[0] : (call[0] as Request).url ?? "";
    expect(url).toContain("/api/v1/mcp/outbound");
    expect(url).toContain("sig_status=verified");
    expect(url).toContain("agent=a");
    unmount();
  });

  it("treats empty next_cursor as end-of-pages", async () => {
    const page: MCPOutboundResponse = {
      entries: [],
      next_cursor: "",
      truncated_at_max: false,
    };
    fetchMock.mockRestore();
    fetchMock = mockFetch([{ match: "/api/v1/mcp/outbound", body: page }]);

    const { result, waitFor, unmount } = renderWithQueryClient(() =>
      useMcpOutbound({}, { enabled: true }),
      createTestQueryClient(),
    );
    await waitFor(() => {
      expect(result.current?.data?.pages?.length).toBe(1);
    });
    expect(result.current?.hasNextPage).toBe(false);
    unmount();
  });
});
