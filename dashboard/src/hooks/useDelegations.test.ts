import type { InfiniteData } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DelegationListResponse, DelegationView } from "../api/types";
import { createTestQueryClient, mockFetch, renderWithQueryClient } from "./__tests__/test-utils";
import {
  __delegationsInternal,
  delegationQueryKeys,
  useAgentDelegations,
  useRevokeDelegation,
} from "./useDelegations";

const { mockConfigState, loggerMock } = vi.hoisted(() => ({
  mockConfigState: {
    apiBaseUrl: "/api/v1",
    apiKey: "",
    tenantId: "",
    principalId: "",
    principalRole: "",
    user: null,
    logout: vi.fn(),
  },
  loggerMock: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("../state/config", () => ({
  useConfigStore: {
    getState: () => mockConfigState,
  },
}));

vi.mock("../lib/logger", () => ({ logger: loggerMock }));

describe("useDelegations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000002",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches an agent delegation page and exposes nextCursor", async () => {
    mockFetch([
      {
        match: "/agents/agent-a/delegations",
        method: "GET",
        body: {
          items: [
            {
              jti: "dlg-1",
              issuer: "agent-a",
              subject: "agent-a",
              audience: "agent-b",
              allowed_actions: ["read"],
              allowed_topics: ["job.alpha"],
              chain: [
                {
                  agent_id: "agent-a",
                  issued_at: "2026-04-21T00:00:00Z",
                  expires_at: "2026-04-21T01:00:00Z",
                  jti: "dlg-1",
                  issued_by: "cordum",
                },
              ],
              chain_depth: 1,
              issued_at: "2026-04-21T00:00:00Z",
              expires_at: "2026-04-21T01:00:00Z",
              revoked: false,
            },
          ],
          next_cursor: "cur-2",
        },
      },
    ]);

    const hook = renderWithQueryClient(() => useAgentDelegations("agent-a", { limit: 25 }));
    await hook.waitFor(() => {
      expect(hook.result.current?.isSuccess).toBe(true);
    });

    const page = hook.result.current?.data?.pages[0];
    expect(page?.items[0]?.allowedActions).toEqual(["read"]);
    expect(page?.items[0]?.chain[0]?.agentId).toBe("agent-a");
    expect(page?.nextCursor).toBe("cur-2");
    expect(hook.result.current?.hasNextPage).toBe(true);
    hook.unmount();
  });

  it("serializes delegation filters to snake_case query params", () => {
    const query = __delegationsInternal.buildDelegationQuery(
      {
        status: "revoked",
        scope: "write",
        beforeExpiry: "2026-04-21T02:00:00Z",
        sinceIssued: "2026-04-20T00:00:00Z",
        untilIssued: "2026-04-21T00:00:00Z",
        limit: 20,
      },
      "cur-9",
    );

    expect(query).toContain("status=revoked");
    expect(query).toContain("scope=write");
    expect(query).toContain("before_expiry=2026-04-21T02%3A00%3A00Z");
    expect(query).toContain("since_issued=2026-04-20T00%3A00%3A00Z");
    expect(query).toContain("until_issued=2026-04-21T00%3A00%3A00Z");
    expect(query).toContain("limit=20");
    expect(query).toContain("cursor=cur-9");
  });

  it("invalidates both global and per-agent queries after revoke", async () => {
    mockFetch([
      {
        match: "/agents/revoke-delegation",
        method: "POST",
        body: {
          jti: "dlg-1",
          cascaded_count: 2,
        },
      },
    ]);

    const queryClient = createTestQueryClient();
    const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
    const hook = renderWithQueryClient(() => useRevokeDelegation(), queryClient);

    await hook.waitFor(() => {
      expect(hook.result.current).toBeDefined();
    });

    const result = await hook.result.current!.mutateAsync({
      jti: "dlg-1",
      reason: "manual",
    });

    expect(result).toEqual({ jti: "dlg-1", cascadedCount: 2 });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["delegations", "all"] });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["delegations", "agent"] });
    hook.unmount();
  });

  it("applies optimistic revoke and rolls back on error", async () => {
    const queryClient = createTestQueryClient();
    const allKey = delegationQueryKeys.all();
    const agentKey = delegationQueryKeys.agent("agent-a");
    const seeded = makeInfiniteDelegationData({
      jti: "dlg-1",
      issuer: "agent-a",
      subject: "agent-a",
      audience: "agent-b",
      allowedActions: ["read"],
      allowedTopics: ["job.alpha"],
      chain: [],
      chainDepth: 1,
      issuedAt: "2026-04-21T00:00:00Z",
      expiresAt: "2026-04-21T01:00:00Z",
      revoked: false,
    });
    queryClient.setQueryData(allKey, seeded);
    queryClient.setQueryData(agentKey, seeded);

    const setQueryDataSpy = vi.spyOn(queryClient, "setQueryData");
    setQueryDataSpy.mockClear();

    vi.spyOn(globalThis, "fetch").mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 25));
      throw new Error("network down");
    });

    const hook = renderWithQueryClient(() => useRevokeDelegation(), queryClient);
    await hook.waitFor(() => {
      expect(hook.result.current).toBeDefined();
    });

    const mutation = hook.result.current!.mutateAsync({
      jti: "dlg-1",
      reason: "manual",
    });
    // No-op handler so the in-flight rejection doesn't fire as an
    // unhandled-rejection during the waitFor below; the cached rejection
    // state is still observable via `await expect(mutation).rejects` later.
    mutation.catch(() => {});

    await hook.waitFor(() => {
      const data = queryClient.getQueryData<InfiniteData<DelegationListResponse, string | undefined>>(allKey);
      expect(data?.pages[0]?.items[0]?.revoked).toBe(true);
      expect(data?.pages[0]?.items[0]?.revokedReason).toBe("manual");
    });

    await expect(mutation).rejects.toThrow("network down");

    expect(setQueryDataSpy).toHaveBeenCalledWith(allKey, seeded);
    expect(setQueryDataSpy).toHaveBeenCalledWith(agentKey, seeded);

    await hook.waitFor(() => {
      const data = queryClient.getQueryData<InfiniteData<DelegationListResponse, string | undefined>>(allKey);
      expect(data?.pages[0]?.items[0]?.revoked).toBe(false);
      expect(data?.pages[0]?.items[0]?.revokedReason).toBeUndefined();
    });
    hook.unmount();
  });
});

function makeInfiniteDelegationData(
  item: DelegationView,
): InfiniteData<DelegationListResponse, string | undefined> {
  return {
    pageParams: [undefined],
    pages: [{ items: [item], nextCursor: undefined }],
  };
}
