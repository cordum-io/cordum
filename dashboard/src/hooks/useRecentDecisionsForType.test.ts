import { beforeEach, describe, expect, it } from "vitest";
import { ensureMswServerListening, http, HttpResponse, server } from "@/test-utils/msw";
import { renderWithQueryClient } from "./__tests__/test-utils";

beforeEach(() => {
  ensureMswServerListening();
});
import { RuleType } from "@/api/generated/model/ruleType";
import { useRecentDecisionsForType } from "./useRecentDecisionsForType";

function mockAuditWith(items: unknown[], total = items.length): URL[] {
  const requested: URL[] = [];
  server.use(
    http.get("*/api/v1/policy/audit", ({ request }) => {
      requested.push(new URL(request.url));
      return HttpResponse.json({ items, total, has_more: false, offset: 0 });
    }),
  );
  return requested;
}

describe("useRecentDecisionsForType", () => {
  it("does not fetch when ruleType is null/undefined (UNKNOWN gate)", async () => {
    const requested = mockAuditWith([]);
    const hook = renderWithQueryClient(() =>
      useRecentDecisionsForType(undefined),
    );
    await new Promise((r) => setTimeout(r, 50));
    expect(requested).toHaveLength(0);
    expect(hook.result.current?.fetchStatus).toBe("idle");
    hook.unmount();
  });

  it("fetches /api/v1/policy/audit with limit=100 + type filter for input", async () => {
    const requested = mockAuditWith([
      { id: "e-1", action: "policy.evaluate", created_at: "2026-04-01T00:00:00Z" },
    ]);
    const hook = renderWithQueryClient(() =>
      useRecentDecisionsForType(RuleType.input),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.entries).toHaveLength(1);
    });
    const params = requested.at(-1)!.searchParams;
    expect(params.get("limit")).toBe("100");
    expect(params.get("type")).toBe("input");
    expect(hook.result.current?.data?.total).toBe(1);
    expect(hook.result.current?.data?.hasMore).toBe(false);
    hook.unmount();
  });

  it.each([
    [RuleType.input, "input"],
    [RuleType.output, "output"],
    [RuleType.velocity, "velocity"],
    [RuleType.edge, "edge"],
  ])("maps RuleType.%s to audit type=%s filter", async (ruleType, expectedFilter) => {
    const requested = mockAuditWith([]);
    const hook = renderWithQueryClient(() =>
      useRecentDecisionsForType(ruleType),
    );
    await hook.waitFor(() => {
      expect(requested).toHaveLength(1);
    });
    expect(requested.at(-1)!.searchParams.get("type")).toBe(expectedFilter);
    hook.unmount();
  });

  it("normalizes a missing total/has_more (legacy output type=output path)", async () => {
    mockAuditWith(
      [
        { id: "e-1", action: "out", created_at: "2026-04-01T00:00:00Z" },
        { id: "e-2", action: "out", created_at: "2026-04-01T00:00:00Z" },
      ],
      // Mock returns total=items.length already; simulate the legacy path
      // by overriding to omit total/has_more.
      undefined as unknown as number,
    );
    server.use(
      http.get("*/api/v1/policy/audit", () =>
        HttpResponse.json({
          items: [
            { id: "e-1", action: "out", created_at: "2026-04-01T00:00:00Z" },
            { id: "e-2", action: "out", created_at: "2026-04-01T00:00:00Z" },
          ],
        }),
      ),
    );
    const hook = renderWithQueryClient(() =>
      useRecentDecisionsForType(RuleType.output),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.data?.entries).toHaveLength(2);
    });
    expect(hook.result.current?.data?.total).toBe(2);
    expect(hook.result.current?.data?.hasMore).toBe(false);
    hook.unmount();
  });

  it("surfaces audit endpoint failure as isError", async () => {
    server.use(
      http.get("*/api/v1/policy/audit", () =>
        HttpResponse.json({ error: "boom" }, { status: 500 }),
      ),
    );
    const hook = renderWithQueryClient(() =>
      useRecentDecisionsForType(RuleType.input),
    );
    await hook.waitFor(() => {
      expect(hook.result.current?.isError).toBe(true);
    }, 3000);
    hook.unmount();
  });
});
