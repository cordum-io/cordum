import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createTestQueryClient,
  mockFetch,
  renderWithQueryClient,
} from "./__tests__/test-utils";
import { useAuditEvents, useInfiniteAuditEvents } from "./useAuditEvents";

const { loggerMock } = vi.hoisted(() => ({
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

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({
  logger: loggerMock,
}));

function siemEvent(overrides: Record<string, unknown>) {
  return {
    id: "evt-default",
    seq: 1,
    timestamp: "2026-05-15T12:00:00Z",
    event_type: "safety.decision",
    severity: "INFO",
    tenant_id: "default",
    action: "evaluate",
    decision: "allow",
    reason: "",
    identity: "",
    extra: {},
    ...overrides,
  };
}

describe("useAuditEvents", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.clearAllMocks();
    mockConfigState.apiBaseUrl = "/api/v1";
    mockConfigState.apiKey = "";
    mockConfigState.tenantId = "";
    mockConfigState.principalId = "";
    mockConfigState.principalRole = "";
    mockConfigState.user = null;
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000123",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches the new /audit/events feed and maps SIEM events to AuditEntry", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: {
          items: [
            siemEvent({
              id: "ev-1",
              seq: 10,
              event_type: "mcp.tool_invocation",
              action: "invoke",
              identity: "alice",
              extra: { tool_name: "search.web", duration_ms: "12" },
            }),
            siemEvent({
              id: "ev-2",
              seq: 11,
              event_type: "edge.action_attempted",
              action: "attempt",
              identity: "bob",
            }),
            siemEvent({
              id: "ev-3",
              seq: 12,
              event_type: "worker.handshake",
              action: "handshake",
              identity: "worker-7",
            }),
          ],
          next_cursor: "",
          returned: 3,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useAuditEvents({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const items = hook.result.current?.items ?? [];
    expect(items).toHaveLength(3);
    const ids = items.map((e) => e.id);
    expect(ids).toEqual(["ev-1", "ev-2", "ev-3"]);
    const types = items.map((e) => e.eventType);
    expect(types).toContain("mcp.tool_invocation");
    expect(types).toContain("edge.action_attempted");
    expect(types).toContain("worker.handshake");
    hook.unmount();
  });

  it("rejects the legacy /policy/audit URL (drift gate)", async () => {
    // The previous implementation went to /policy/audit. If the new hook
    // accidentally hits the old path, the mock below will throw and the
    // test fails. This is the canary against silent regression.
    const fetchSpy = mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);

    const hook = renderWithQueryClient(() => useAuditEvents({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const requestedUrls = fetchSpy.mock.calls.map((call) => {
      const input = call[0];
      return typeof input === "string"
        ? input
        : input instanceof URL
          ? input.toString()
          : input.url;
    });
    expect(requestedUrls.some((u) => u.includes("/audit/events"))).toBe(true);
    expect(requestedUrls.some((u) => u.includes("/policy/audit"))).toBe(false);
    hook.unmount();
  });

  it("applies the event_type filter via query string", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useAuditEvents({ eventType: ["mcp.tool_invocation"] }),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const lastUrl = String(fetchSpy.mock.calls.at(-1)?.[0] ?? "");
    expect(lastUrl).toContain("event_type=mcp.tool_invocation");
    hook.unmount();
  });

  it("sends the X-Tenant-ID header from config store", async () => {
    mockConfigState.tenantId = "tenant-a";
    const fetchSpy = mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);

    const hook = renderWithQueryClient(() => useAuditEvents({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const lastInit = fetchSpy.mock.calls.at(-1)?.[1] as RequestInit;
    const headers = lastInit?.headers as Record<string, string> | undefined;
    expect(headers?.["X-Tenant-ID"]).toBe("tenant-a");
    hook.unmount();
  });

  it("surfaces a user-readable error when the backend returns 503 audit_chainer_not_installed", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        status: 503,
        body: {
          error: "audit_chainer_not_installed",
          status: 503,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useAuditEvents({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    const msg = hook.result.current?.userMessage ?? "";
    expect(msg.toLowerCase()).toContain("audit chain");
    hook.unmount();
  });

  it("exposes a cursor for the next page when the server returns one", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: {
          items: [siemEvent({ id: "p1-a", seq: 100 })],
          next_cursor: "cursor-page-2",
          returned: 1,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useAuditEvents({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    expect(hook.result.current?.nextCursor).toBe("cursor-page-2");
    expect(hook.result.current?.hasNextPage).toBe(true);
    hook.unmount();
  });

  it("uses cursor pagination via useInfiniteAuditEvents: page 2 request forwards the previous response's next_cursor", async () => {
    // Two mock entries:
    //  - the cursor-less request returns page 1 with next_cursor="cursor-p2"
    //  - the request carrying ?cursor=cursor-p2 returns page 2 with empty
    //    next_cursor (end of stream)
    // The hook MUST chain the second request using the first response's
    // cursor and concatenate the items into a single flat list.
    const fetchSpy = mockFetch([
      {
        match: (url) =>
          url.includes("/audit/events") && !url.includes("cursor="),
        method: "GET",
        body: {
          items: [
            siemEvent({ id: "p1-a", seq: 200 }),
            siemEvent({ id: "p1-b", seq: 199 }),
          ],
          next_cursor: "cursor-p2",
          returned: 2,
        },
      },
      {
        match: (url) =>
          url.includes("/audit/events") && url.includes("cursor=cursor-p2"),
        method: "GET",
        body: {
          items: [
            siemEvent({ id: "p2-a", seq: 198 }),
            siemEvent({ id: "p2-b", seq: 197 }),
          ],
          next_cursor: "",
          returned: 2,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useInfiniteAuditEvents({}));

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    // Page 1 only — should see 2 items, hasNextPage=true.
    expect(hook.result.current?.items.map((e) => e.id)).toEqual([
      "p1-a",
      "p1-b",
    ]);
    expect(hook.result.current?.hasNextPage).toBe(true);

    // Trigger page 2 fetch. fetchNextPage returns a promise, but the
    // harness re-renders synchronously so we just wait for items length
    // to grow.
    void hook.result.current?.fetchNextPage();

    await hook.waitFor(() => {
      expect(hook.result.current?.items.length).toBe(4);
    });

    // All 4 items flattened across both pages in chronological order.
    expect(hook.result.current?.items.map((e) => e.id)).toEqual([
      "p1-a",
      "p1-b",
      "p2-a",
      "p2-b",
    ]);
    // Server signalled end of stream → hasNextPage flips to false.
    expect(hook.result.current?.hasNextPage).toBe(false);

    // Verify the SECOND request actually carried the cursor — the bug
    // QA caught was the page never sending the cursor parameter at all.
    const urls = fetchSpy.mock.calls.map((c) => String(c[0] ?? ""));
    const cursorRequests = urls.filter((u) => u.includes("cursor=cursor-p2"));
    expect(cursorRequests).toHaveLength(1);

    hook.unmount();
  });

  it("useInfiniteAuditEvents stops paging when next_cursor is empty on the first response", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: {
          items: [siemEvent({ id: "only", seq: 1 })],
          next_cursor: "",
          returned: 1,
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useInfiniteAuditEvents({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.items.map((e) => e.id)).toEqual(["only"]);
    expect(hook.result.current?.hasNextPage).toBe(false);
    hook.unmount();
  });

  it("clamps requested limit at the documented MaxAuditEventsLimit on the server side and does not duplicate the param", async () => {
    const fetchSpy = mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);

    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(
      () => useAuditEvents({ limit: 500 }),
      queryClient,
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const url = String(fetchSpy.mock.calls.at(-1)?.[0] ?? "");
    const matches = url.match(/limit=/g) ?? [];
    expect(matches).toHaveLength(1);
    // Caller asked for 500. The hook forwards what it was asked; the server
    // is the authoritative clamp. The contract here is "no double-param,
    // limit is forwarded verbatim".
    expect(url).toContain("limit=500");
    hook.unmount();
  });

  // P2a — live audit poll. The Audit Log "Live" toggle drives options.live;
  // the hook must translate that into refetchInterval === 2000 (exact, so a
  // mutated cadence is caught) when ON and false (no auto-poll) when OFF.
  it("useInfiniteAuditEvents polls at exactly 2000ms only when live=true", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);
    const queryClient = createTestQueryClient();
    const hook = renderWithQueryClient(
      () => useInfiniteAuditEvents({}, { live: true }),
      queryClient,
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });
    const query = queryClient.getQueryCache().findAll()[0];
    expect(query?.observers[0]?.options.refetchInterval).toBe(2000);
    hook.unmount();
  });

  it("useInfiniteAuditEvents does NOT auto-poll when live is false or omitted", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);
    // live: false
    const offClient = createTestQueryClient();
    const offHook = renderWithQueryClient(
      () => useInfiniteAuditEvents({}, { live: false }),
      offClient,
    );
    await offHook.waitFor(() => {
      expect(offHook.result.current?.isSuccess).toBe(true);
    });
    expect(
      offClient.getQueryCache().findAll()[0]?.observers[0]?.options
        .refetchInterval,
    ).toBe(false);
    offHook.unmount();

    // options omitted entirely — default is OFF (preserve prod 15s cadence).
    const defaultClient = createTestQueryClient();
    const defaultHook = renderWithQueryClient(
      () => useInfiniteAuditEvents({}),
      defaultClient,
    );
    await defaultHook.waitFor(() => {
      expect(defaultHook.result.current?.isSuccess).toBe(true);
    });
    expect(
      defaultClient.getQueryCache().findAll()[0]?.observers[0]?.options
        .refetchInterval,
    ).toBe(false);
    defaultHook.unmount();
  });

  it("useAuditEvents (one-shot) gates the same 2000ms live poll on the live flag", async () => {
    mockFetch([
      {
        match: "/audit/events",
        method: "GET",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);
    const liveClient = createTestQueryClient();
    const liveHook = renderWithQueryClient(
      () => useAuditEvents({}, { live: true }),
      liveClient,
    );
    await liveHook.waitFor(() => {
      expect(liveHook.result.current?.isSuccess).toBe(true);
    });
    expect(
      liveClient.getQueryCache().findAll()[0]?.observers[0]?.options
        .refetchInterval,
    ).toBe(2000);
    liveHook.unmount();
  });
});
