import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, within } from "@/test-utils/render";
import { mockFetch } from "@/hooks/__tests__/test-utils";
import EdgeBinaryIntegrityPage from "./EdgeBinaryIntegrityPage";
import type { BinaryVerifyListEnvelope } from "@/hooks/useBinaryIntegrityEvents";

const mockConfigState = {
  apiBaseUrl: "/api/v1",
  apiKey: "test-key",
  tenantId: "tenant-a",
  principalId: "p1",
  principalRole: "admin",
  user: null,
  logout: vi.fn(),
};

vi.mock("@/state/config", () => ({
  useConfigStore: { getState: () => mockConfigState },
  registerQueryClient: vi.fn(),
}));

vi.mock("../lib/logger", () => ({
  logger: {
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  },
}));

const failItem = {
  timestamp: "2026-05-17T12:00:00Z",
  tenant_id: "tenant-a",
  endpoint: "host-1",
  event: "binary-verify-fail" as const,
  hash: "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
  path: "cordum-gateway",
  sig_scheme: "gpg" as const,
  fingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
  reason: "hash mismatch cordum-gateway",
  exit_code: 1,
};
const okItem = {
  timestamp: "2026-05-17T11:00:00Z",
  tenant_id: "tenant-a",
  endpoint: "host-2",
  event: "binary-verify-ok" as const,
  hash: "b1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f91",
  path: "cordum-scheduler",
  sig_scheme: "codesign" as const,
  fingerprint: "",
  reason: "",
  exit_code: 0,
};

function envelope(
  items: BinaryVerifyListEnvelope["items"],
): BinaryVerifyListEnvelope {
  return { items, next_cursor: "", returned: items.length };
}

describe("EdgeBinaryIntegrityPage", () => {
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

  it("renders the empty state when the gateway returns no events", async () => {
    mockFetch([
      {
        match: "/edge/binary-integrity/events",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);
    renderWithProviders(<EdgeBinaryIntegrityPage />);
    expect(
      await screen.findByText(/No binary-verify events recorded/i),
    ).toBeTruthy();
    expect(screen.queryByTestId("binary-integrity-table")).toBeNull();
  });

  it("renders the events table with rows when the gateway returns events", async () => {
    mockFetch([
      {
        match: "/edge/binary-integrity/events",
        body: envelope([failItem, okItem]),
      },
    ]);
    renderWithProviders(<EdgeBinaryIntegrityPage />);
    const table = await screen.findByTestId("binary-integrity-table");
    expect(within(table).getByTestId("binary-integrity-row-binary-verify-fail")).toBeTruthy();
    expect(within(table).getByTestId("binary-integrity-row-binary-verify-ok")).toBeTruthy();
    // Failed-event row carries the runbook link.
    expect(screen.getByTestId("binary-integrity-runbook-link")).toBeTruthy();
    // Summary banner appears for at least one failure.
    expect(screen.getByTestId("binary-integrity-fail-summary")).toBeTruthy();
  });

  it("forwards filter changes as new gateway requests", async () => {
    const seen: string[] = [];
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.toString()
            : input.url;
      seen.push(url);
      return new Response(
        JSON.stringify({ items: [], next_cursor: "", returned: 0 }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    });
    renderWithProviders(<EdgeBinaryIntegrityPage />);
    await screen.findByTestId("binary-integrity-filters");
    fireEvent.change(screen.getByTestId("binary-integrity-filter-event"), {
      target: { value: "fail" },
    });
    fireEvent.change(screen.getByTestId("binary-integrity-filter-sig-scheme"), {
      target: { value: "gpg" },
    });
    // The render should have triggered a new fetch with these query params.
    // Wait for at least one URL carrying both filters.
    await waitForCondition(() =>
      seen.some(
        (u) => u.includes("event=fail") && u.includes("sig_scheme=gpg"),
      ),
    );
    expect(
      seen.some((u) => u.includes("event=fail") && u.includes("sig_scheme=gpg")),
    ).toBe(true);
  });

  it("renders a 503 user message via the error banner", async () => {
    mockFetch([
      {
        match: "/edge/binary-integrity/events",
        status: 503,
        body: { error: "audit_chainer_not_installed" },
      },
    ]);
    renderWithProviders(<EdgeBinaryIntegrityPage />);
    expect(
      await screen.findByText(/Audit chain not installed/i),
    ).toBeTruthy();
  });

  it("renders the reset button and clears filter selections", async () => {
    mockFetch([
      {
        match: "/edge/binary-integrity/events",
        body: { items: [], next_cursor: "", returned: 0 },
      },
    ]);
    renderWithProviders(<EdgeBinaryIntegrityPage />);
    const eventSelect = await screen.findByTestId("binary-integrity-filter-event");
    fireEvent.change(eventSelect, { target: { value: "fail" } });
    expect((eventSelect as HTMLSelectElement).value).toBe("fail");
    fireEvent.click(screen.getByTestId("binary-integrity-reset"));
    expect((eventSelect as HTMLSelectElement).value).toBe("");
  });
});

async function waitForCondition(predicate: () => boolean, timeoutMs = 2000) {
  const start = Date.now();
  while (!predicate()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error("waitForCondition timed out");
    }
    await new Promise((res) => setTimeout(res, 20));
  }
}
