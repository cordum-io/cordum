import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import { __auditInternal, useAuditCorrelation, useAuditEvent, useAuditLog } from "./useAudit";
import type { AuditEntry } from "../api/types";

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

function makeEntry(overrides: Partial<AuditEntry>): AuditEntry {
  return {
    id: "a",
    timestamp: "2026-02-13T12:00:00.000Z",
    eventType: "approve",
    actor: "alice",
    resourceType: "job",
    resourceId: "job-1",
    action: "approve",
    message: "approved",
    ...overrides,
  };
}

describe("useAudit internals", () => {
  it("applyFilters handles eventType, actor, resource filters, severity, and outcome", () => {
    const entries = [
      makeEntry({ id: "e1", eventType: "approve", actor: "Alice", resourceType: "job", resourceId: "j1", severity: "high", action: "approve" }),
      makeEntry({ id: "e2", eventType: "deny", actor: "bob", resourceType: "policy", resourceId: "p1", severity: "low", action: "deny" }),
      makeEntry({ id: "e3", eventType: "approve", actor: "ALICIA", resourceType: "job", resourceId: "j2", severity: "medium", action: "approve" }),
    ];

    const filtered = __auditInternal.applyFilters(entries, {
      eventType: ["approve"],
      actor: "ali",
      resourceType: "job",
      resourceId: "j1",
      severity: ["high"],
      outcome: ["approve"],
    });

    expect(filtered.map((e) => e.id)).toEqual(["e1"]);
  });

  it("applyFilters handles custom time range and preset range", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T12:00:00.000Z"));

    const entries = [
      makeEntry({ id: "old", timestamp: "2026-02-12T10:00:00.000Z" }),
      makeEntry({ id: "mid", timestamp: "2026-02-13T10:30:00.000Z" }),
      makeEntry({ id: "new", timestamp: "2026-02-13T11:30:00.000Z" }),
    ];

    const custom = __auditInternal.applyFilters(entries, {
      timeRange: "custom",
      from: "2026-02-13T10:00:00.000Z",
      to: "2026-02-13T11:00:00.000Z",
    });
    expect(custom.map((e) => e.id)).toEqual(["mid"]);

    const lastHour = __auditInternal.applyFilters(entries, { timeRange: "1h" });
    expect(lastHour.map((e) => e.id)).toEqual(["new"]);

    vi.useRealTimers();
  });

  it("applyFilters handles case-insensitive search and intersection", () => {
    const entries = [
      makeEntry({ id: "e1", message: "Applied policy", payload: { reason: "sensitive" }, actor: "alice", action: "approve", eventType: "approve", resourceType: "job", resourceId: "j1" }),
      makeEntry({ id: "e2", message: "Other message", payload: { reason: "none" }, actor: "bob", action: "deny", eventType: "deny", resourceType: "policy", resourceId: "p1" }),
    ];

    const filtered = __auditInternal.applyFilters(entries, {
      search: "SENSITIVE",
      eventType: ["approve"],
    });

    expect(filtered.map((e) => e.id)).toEqual(["e1"]);
  });

  it("applySort supports time and action ordering", () => {
    const entries = [
      makeEntry({ id: "a", timestamp: "2026-02-13T12:00:00.000Z", eventType: "zeta", action: "zeta" }),
      makeEntry({ id: "b", timestamp: "2026-02-13T10:00:00.000Z", eventType: "alpha", action: "alpha" }),
      makeEntry({ id: "c", timestamp: "2026-02-13T11:00:00.000Z", eventType: "beta", action: "beta" }),
    ];

    expect(__auditInternal.applySort(entries, "time-desc").map((e) => e.id)).toEqual(["a", "c", "b"]);
    expect(__auditInternal.applySort(entries, "time-asc").map((e) => e.id)).toEqual(["b", "c", "a"]);
    expect(__auditInternal.applySort(entries, "action-asc").map((e) => e.id)).toEqual(["b", "c", "a"]);
    expect(__auditInternal.applySort(entries, "action-desc").map((e) => e.id)).toEqual(["a", "c", "b"]);
  });
});

describe("useAudit hooks", () => {
  beforeEach(() => {
    window.localStorage.clear();
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

  it("useAuditLog starts in loading state", () => {
    mockFetch([
      { match: "/policy/audit", method: "GET", body: { items: [] } },
    ]);

    const hook = renderWithQueryClient(() => useAuditLog({}));
    expect(hook.result.current?.isLoading).toBe(true);
    expect(hook.result.current?.filtered).toEqual([]);
    hook.unmount();
  });

  it("useAuditLog returns error state on fetch failure", async () => {
    mockFetch([
      { match: "/policy/audit", method: "GET", status: 500, body: { error: "server error" } },
    ]);

    const hook = renderWithQueryClient(() => useAuditLog({}));
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    });
    hook.unmount();
  });

  it("useAuditLog fetches and returns filtered/sorted entries", async () => {
    mockFetch([
      {
        match: "/policy/audit",
        method: "GET",
        body: {
          items: [
            {
              id: "a1",
              action: "approve",
              actor_id: "alice",
              resource_type: "job",
              resource_id: "j1",
              message: "approved",
              created_at: "2026-02-13T10:00:00.000Z",
            },
            {
              id: "a2",
              action: "deny",
              actor_id: "bob",
              resource_type: "job",
              resource_id: "j2",
              message: "denied",
              created_at: "2026-02-13T11:00:00.000Z",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() =>
      useAuditLog({ eventType: ["approve"], actor: "ali", sort: "time-asc" }),
    );

    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.filtered).toHaveLength(1);
    expect(hook.result.current?.filtered[0]).toMatchObject({ id: "a1", actor: "alice" });
    hook.unmount();
  });

  it("useAuditEvent resolves by id and supports missing ids", async () => {
    mockFetch([
      {
        match: "/policy/audit",
        method: "GET",
        body: {
          items: [
            {
              id: "a1",
              action: "approve",
              actor_id: "alice",
              resource_type: "job",
              resource_id: "j1",
              created_at: "2026-02-13T10:00:00.000Z",
            },
          ],
        },
      },
    ]);

    const found = renderWithQueryClient(() => useAuditEvent("a1"));
    await found.waitFor(() => {
      expect(found.result.current?.isSuccess).toBe(true);
    });
    expect(found.result.current?.data?.id).toBe("a1");
    found.unmount();

    const missingClient = createTestQueryClient();
    const missing = renderWithQueryClient(() => useAuditEvent("missing"), missingClient);
    await missing.waitFor(() => {
      expect(missing.result.current?.isSuccess).toBe(true);
    });
    expect(missing.result.current?.data).toBeNull();
    missing.unmount();
  });

  it("useAuditEvent uses placeholderData from main audit cache", () => {
    const queryClient = createTestQueryClient();
    // Seed the main audit cache with entries
    queryClient.setQueryData(["audit"], {
      items: [
        makeEntry({ id: "e1", actor: "alice" }),
        makeEntry({ id: "e2", actor: "bob" }),
      ],
    });
    // No fetch mock needed — placeholderData should resolve synchronously
    mockFetch([
      { match: "/policy/audit", method: "GET", body: { items: [] } },
    ]);
    const hook = renderWithQueryClient(() => useAuditEvent("e1"), queryClient);

    // Placeholder should resolve immediately from the audit cache
    expect(hook.result.current?.data?.id).toBe("e1");
    expect(hook.result.current?.data?.actor).toBe("alice");
    hook.unmount();
  });

  it("useAuditCorrelation uses placeholderData from main audit cache", () => {
    const queryClient = createTestQueryClient();
    queryClient.setQueryData(["audit"], {
      items: [
        makeEntry({ id: "c1", resourceId: "j1", timestamp: "2026-02-13T11:00:00.000Z" }),
        makeEntry({ id: "c2", resourceId: "j1", timestamp: "2026-02-13T10:00:00.000Z" }),
        makeEntry({ id: "c3", resourceId: "j2", timestamp: "2026-02-13T09:00:00.000Z" }),
      ],
    });
    mockFetch([
      { match: "/policy/audit", method: "GET", body: { items: [] } },
    ]);
    const hook = renderWithQueryClient(() => useAuditCorrelation("j1"), queryClient);

    // Placeholder should filter to j1 and sort ascending
    expect(hook.result.current?.data?.map((e) => e.id)).toEqual(["c2", "c1"]);
    hook.unmount();
  });

  it("useAuditCorrelation filters by resourceId and sorts ascending", async () => {
    mockFetch([
      {
        match: "/policy/audit",
        method: "GET",
        body: {
          items: [
            {
              id: "a1",
              action: "approve",
              actor_id: "alice",
              resource_type: "job",
              resource_id: "j1",
              created_at: "2026-02-13T11:00:00.000Z",
            },
            {
              id: "a2",
              action: "deny",
              actor_id: "alice",
              resource_type: "job",
              resource_id: "j1",
              created_at: "2026-02-13T10:00:00.000Z",
            },
            {
              id: "a3",
              action: "approve",
              actor_id: "alice",
              resource_type: "job",
              resource_id: "j2",
              created_at: "2026-02-13T09:00:00.000Z",
            },
          ],
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useAuditCorrelation("j1"));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    expect(hook.result.current?.data?.map((e) => e.id)).toEqual(["a2", "a1"]);
    hook.unmount();
  });
});

