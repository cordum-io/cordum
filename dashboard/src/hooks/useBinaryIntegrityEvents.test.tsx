import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { mockFetch } from "./__tests__/test-utils";
import {
  useBinaryIntegrityEvents,
  type BinaryVerifyListEnvelope,
} from "./useBinaryIntegrityEvents";

const mockConfigState = {
  apiBaseUrl: "/api/v1",
  apiKey: "test-key",
  tenantId: "tenant-a",
  principalId: "p1",
  principalRole: "admin",
  user: null,
  logout: vi.fn(),
};

vi.mock("../state/config", () => ({
  useConfigStore: { getState: () => mockConfigState },
}));

vi.mock("../lib/logger", () => ({
  logger: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

function testClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
}

function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
}

const sampleEnvelope: BinaryVerifyListEnvelope = {
  items: [
    {
      timestamp: "2026-05-17T12:00:00Z",
      tenant_id: "tenant-a",
      endpoint: "host-1",
      event: "binary-verify-fail",
      hash: "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
      path: "cordum-gateway",
      sig_scheme: "gpg",
      fingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
      reason: "hash mismatch cordum-gateway",
      exit_code: 1,
    },
  ],
  next_cursor: "",
  returned: 1,
};

describe("useBinaryIntegrityEvents", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(globalThis.crypto, "randomUUID").mockReturnValue(
      "00000000-0000-0000-0000-000000000123",
    );
    vi.spyOn(performance, "now").mockReturnValue(100);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("resolves with items from the gateway", async () => {
    mockFetch([
      { match: "/edge/binary-integrity/events", body: sampleEnvelope },
    ]);
    const client = testClient();
    const { result } = renderHook(() => useBinaryIntegrityEvents(), {
      wrapper: wrapper(client),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.items).toHaveLength(1);
    expect(result.current.items[0].event).toBe("binary-verify-fail");
    expect(result.current.hasNextPage).toBe(false);
  });

  it("forwards filters as query-string params", async () => {
    const seenURLs: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.toString()
            : input.url;
      seenURLs.push(url);
      return new Response(JSON.stringify(sampleEnvelope), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    const client = testClient();
    renderHook(
      () =>
        useBinaryIntegrityEvents({
          event: "fail",
          sigScheme: "gpg",
          endpoint: "host-7",
          limit: 50,
        }),
      { wrapper: wrapper(client) },
    );
    await waitFor(() => expect(seenURLs.length).toBeGreaterThan(0));
    const url = new URL(seenURLs[0], "http://localhost");
    expect(url.searchParams.get("event")).toBe("fail");
    expect(url.searchParams.get("sig_scheme")).toBe("gpg");
    expect(url.searchParams.get("endpoint")).toBe("host-7");
    expect(url.searchParams.get("limit")).toBe("50");
  });

  it("translates a 503 into a userMessage", async () => {
    mockFetch([
      {
        match: "/edge/binary-integrity/events",
        status: 503,
        body: { error: "audit_chainer_not_installed" },
      },
    ]);
    const client = testClient();
    const { result } = renderHook(() => useBinaryIntegrityEvents(), {
      wrapper: wrapper(client),
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.userMessage).toMatch(/Audit chain not installed/);
  });

  it("exposes next_cursor so consumers can page", async () => {
    mockFetch([
      {
        match: "/edge/binary-integrity/events",
        body: { ...sampleEnvelope, next_cursor: "1700000000000-0" },
      },
    ]);
    const client = testClient();
    const { result } = renderHook(() => useBinaryIntegrityEvents(), {
      wrapper: wrapper(client),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.nextCursor).toBe("1700000000000-0");
    expect(result.current.hasNextPage).toBe(true);
  });
});
