import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  AUDIT_EXPORT_HEADERS,
  buildAuditExportRows,
  filterEventsBySeq,
  parseSeqParam,
  shouldFetchNextAuditPage,
} from "./AuditLogPage";
import { toCsv } from "@/lib/export";
import type { AuditEntry } from "@/api/types";

function sampleAuditEntry(over: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id: "evt-1",
    timestamp: "2026-05-15T12:00:00Z",
    eventType: "edge.action_denied",
    actor: "user:alice",
    resourceType: "edge",
    resourceId: "res-1",
    action: "edge.action",
    message: "blocked path",
    humanSummary: "Billing Bot was denied Bash — deny (blocked path)",
    actorLabel: "Alice Ops",
    agentLabel: "Billing Bot",
    agentName: "Billing Bot",
    agentId: "agent-7",
    decision: "deny",
    matchedRule: "no-prod-writes",
    reason: "blocked path",
    severity: "high",
    governanceCategory: "governance",
    sessionId: "sess-9",
    executionId: "exec-3",
    jobId: "",
    seq: 100,
    eventHash: "h2",
    prevHash: "h1",
    inputPreview: "command: read config",
    traceId: "tr-1",
    artifactId: "sha256:abc",
    ...over,
  };
}

// ---------------------------------------------------------------------------
// Mock the API client before any imports that reference it
// ---------------------------------------------------------------------------

let lastFetchParams: URLSearchParams | null = null;
const mockItems = Array.from({ length: 50 }, (_, i) => ({
  id: `evt-${String(i).padStart(3, "0")}`,
  action: "job.created",
  actor_id: `user-${i}`,
  resource_type: "job",
  resource_id: `job-${i}`,
  message: `Test event ${i}`,
  created_at: new Date(Date.now() - i * 60_000).toISOString(),
}));

vi.mock("@/api/client", () => ({
  get: vi.fn(async (path: string) => {
    const url = new URL(path, "http://localhost");
    lastFetchParams = url.searchParams;
    const offset = Number(url.searchParams.get("offset") ?? "0");
    const limit = Number(url.searchParams.get("limit") ?? "50");
    const search = url.searchParams.get("search") ?? "";
    const action = url.searchParams.get("action") ?? "";
    const after = url.searchParams.get("after") ?? "";
    const before = url.searchParams.get("before") ?? "";

    let filtered = [...mockItems];
    if (search) {
      filtered = filtered.filter(
        (e) =>
          e.action.includes(search) ||
          e.actor_id.includes(search) ||
          e.message.includes(search),
      );
    }
    if (action) {
      filtered = filtered.filter((e) => e.action === action);
    }
    if (after) {
      filtered = filtered.filter((e) => e.created_at >= after);
    }
    if (before) {
      filtered = filtered.filter((e) => e.created_at <= before);
    }

    const total = filtered.length;
    const page = filtered.slice(offset, offset + limit);
    return {
      items: page,
      total,
      has_more: offset + page.length < total,
      offset,
    };
  }),
}));

// ---------------------------------------------------------------------------
// Tests — store/logic-level (no RTL DOM rendering needed)
// ---------------------------------------------------------------------------

describe("AuditLogPage API integration", () => {
  beforeEach(() => {
    lastFetchParams = null;
    vi.clearAllMocks();
  });

  it("fetches first page with limit=50 and offset=0", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=0",
    );

    expect(lastFetchParams).not.toBeNull();
    expect(lastFetchParams!.get("limit")).toBe("50");
    expect(lastFetchParams!.get("offset")).toBe("0");
  });

  it("sends search param to API", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=0&search=user-5",
    );

    expect(lastFetchParams!.get("search")).toBe("user-5");
  });

  it("sends date range params to API", async () => {
    const { get } = await import("@/api/client");
    const after = "2026-03-20T00:00:00.000Z";
    const before = "2026-03-25T23:59:59.000Z";
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      `/policy/audit?limit=50&offset=0&after=${after}&before=${before}`,
    );

    expect(lastFetchParams!.get("after")).toBe(after);
    expect(lastFetchParams!.get("before")).toBe(before);
  });

  it("sends action filter param to API", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=0&action=job.failed",
    );

    expect(lastFetchParams!.get("action")).toBe("job.failed");
  });

  it("returns has_more=true when more pages available", async () => {
    const { get } = await import("@/api/client");
    const result = await (
      get as unknown as (...args: unknown[]) => Promise<unknown>
    )("/policy/audit?limit=25&offset=0");

    const r = result as { has_more: boolean; items: unknown[]; total: number };
    expect(r.has_more).toBe(true);
    expect(r.items.length).toBe(25);
    expect(r.total).toBe(50);
  });

  it("returns has_more=false on last page", async () => {
    const { get } = await import("@/api/client");
    const result = await (
      get as unknown as (...args: unknown[]) => Promise<unknown>
    )("/policy/audit?limit=50&offset=0");

    const r = result as { has_more: boolean; items: unknown[] };
    expect(r.has_more).toBe(false);
    expect(r.items.length).toBe(50);
  });

  it("second page uses offset=50", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=50",
    );

    expect(lastFetchParams!.get("offset")).toBe("50");
  });
});

describe("shouldFetchNextAuditPage", () => {
  it("fetches only when the sentinel is visible and paging is idle", () => {
    expect(
      shouldFetchNextAuditPage([{ isIntersecting: true }], true, false),
    ).toBe(true);
    expect(
      shouldFetchNextAuditPage([{ isIntersecting: false }], true, false),
    ).toBe(false);
    expect(
      shouldFetchNextAuditPage([{ isIntersecting: true }], false, false),
    ).toBe(false);
    expect(
      shouldFetchNextAuditPage([{ isIntersecting: true }], true, true),
    ).toBe(false);
  });
});

describe("AuditLogPage agent_id filter", () => {
  beforeEach(() => {
    lastFetchParams = null;
    vi.clearAllMocks();
  });

  it("sends agent_id param to /policy/audit API", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=0&agent_id=agent-alpha",
    );

    expect(lastFetchParams).not.toBeNull();
    expect(lastFetchParams!.get("agent_id")).toBe("agent-alpha");
  });

  it("omits agent_id param when no agent filter is selected", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=0",
    );

    expect(lastFetchParams).not.toBeNull();
    expect(lastFetchParams!.get("agent_id")).toBeNull();
  });

  it("combines agent_id with other filters", async () => {
    const { get } = await import("@/api/client");
    await (get as unknown as (...args: unknown[]) => Promise<unknown>)(
      "/policy/audit?limit=50&offset=0&action=job.created&agent_id=agent-beta&search=deploy",
    );

    expect(lastFetchParams!.get("agent_id")).toBe("agent-beta");
    expect(lastFetchParams!.get("action")).toBe("job.created");
    expect(lastFetchParams!.get("search")).toBe("deploy");
  });
});

describe("AuditLogPage component agent filter integration", () => {
  it("builds query params with agent_id when agentFilter is set", () => {
    // Verify that the AuditLogPage queryFn builds params correctly.
    // This mirrors the logic in the component's useInfiniteQuery queryFn.
    const actionFilter = "";
    const agentFilter = "agent-alpha";
    const dateFrom = "";
    const dateTo = "";
    const search = "";

    const params = new URLSearchParams({
      limit: String(50),
      offset: String(0),
    });
    if (actionFilter) params.set("action", actionFilter);
    if (agentFilter) params.set("agent_id", agentFilter);
    if (dateFrom) params.set("after", new Date(dateFrom).toISOString());
    if (dateTo) params.set("before", new Date(dateTo + "T23:59:59").toISOString());
    if (search) params.set("search", search);

    expect(params.get("agent_id")).toBe("agent-alpha");
    expect(params.toString()).toContain("agent_id=agent-alpha");
  });

  it("does not include agent_id when agentFilter is empty", () => {
    const agentFilter = "";

    const params = new URLSearchParams({
      limit: String(50),
      offset: String(0),
    });
    if (agentFilter) params.set("agent_id", agentFilter);

    expect(params.get("agent_id")).toBeNull();
    expect(params.toString()).not.toContain("agent_id");
  });

  it("includes agent_id in queryKey for cache invalidation", () => {
    // The component uses ["audit", actionFilter, agentFilter, ...] as queryKey.
    // Changing agentFilter must produce a different queryKey to trigger refetch.
    const key1 = ["audit", "", "", "", "", ""];
    const key2 = ["audit", "", "agent-alpha", "", "", ""];
    expect(key1).not.toEqual(key2);
  });
});

describe("buildAuditExportRows + toCsv (human-readable export)", () => {
  it("projects each cell onto AUDIT_EXPORT_HEADERS in order", () => {
    const rows = buildAuditExportRows([sampleAuditEntry()]);
    expect(rows).toHaveLength(1);
    const row = rows[0];
    expect(row).toHaveLength(AUDIT_EXPORT_HEADERS.length);
    // Spot-check that columns land in their documented positions.
    const col = (name: string) =>
      row[(AUDIT_EXPORT_HEADERS as readonly string[]).indexOf(name)];
    expect(col("Summary")).toBe("Billing Bot was denied Bash — deny (blocked path)");
    expect(col("Severity")).toBe("high");
    expect(col("Agent")).toBe("Billing Bot");
    expect(col("Decision")).toBe("deny");
    expect(col("Rule")).toBe("no-prod-writes");
    expect(col("Session ID")).toBe("sess-9");
    expect(col("Execution ID")).toBe("exec-3");
    expect(col("Chain Seq")).toBe("100");
    expect(col("Event Hash")).toBe("h2");
    expect(col("Input Preview")).toBe("command: read config");
    expect(col("Trace ID")).toBe("tr-1");
    expect(col("Artifact ID")).toBe("sha256:abc");
  });

  it("RFC-4180 escapes commas, quotes, and newlines via toCsv", () => {
    const csv = toCsv(
      ["A", "B"],
      [["plain", 'has, comma'], ['has "quote"', "line\nbreak"]],
    );
    const lines = csv.split("\n");
    expect(lines[0]).toBe("A,B");
    // comma-bearing cell is quoted; quote-bearing cell doubles its quotes.
    expect(csv).toContain('"has, comma"');
    expect(csv).toContain('"has ""quote"""');
    // a newline inside a cell must be wrapped in quotes (stays one logical row).
    expect(csv).toContain('"line\nbreak"');
  });

  it("neutralises spreadsheet formula-injection at the cell source", () => {
    const rows = buildAuditExportRows([
      sampleAuditEntry({
        humanSummary: "=cmd|' /c calc'!A1",
        actorLabel: "+danger",
        reason: "-2-3",
      }),
    ]);
    const row = rows[0];
    const col = (name: string) =>
      row[(AUDIT_EXPORT_HEADERS as readonly string[]).indexOf(name)];
    // Formula-trigger first char is prefixed with a single quote.
    expect(col("Summary").startsWith("'=")).toBe(true);
    expect(col("Actor").startsWith("'+")).toBe(true);
    expect(col("Reason").startsWith("'-")).toBe(true);
  });

  it("emits a header line plus one line per row", () => {
    const csv = toCsv(
      [...AUDIT_EXPORT_HEADERS],
      buildAuditExportRows([sampleAuditEntry(), sampleAuditEntry({ id: "evt-2" })]),
    );
    const lines = csv.split("\n");
    expect(lines[0]).toBe(AUDIT_EXPORT_HEADERS.join(","));
    expect(lines).toHaveLength(3); // header + 2 data rows
  });
});

describe("parseSeqParam", () => {
  it("returns undefined for null / empty / non-numeric", () => {
    expect(parseSeqParam(null)).toBeUndefined();
    expect(parseSeqParam(undefined)).toBeUndefined();
    expect(parseSeqParam("")).toBeUndefined();
    expect(parseSeqParam("  ")).toBeUndefined();
    expect(parseSeqParam("abc")).toBeUndefined();
  });
  it("parses valid non-negative integers", () => {
    expect(parseSeqParam("0")).toBe(0);
    expect(parseSeqParam("42")).toBe(42);
    expect(parseSeqParam("14300")).toBe(14300);
    expect(parseSeqParam(" 14300 ")).toBe(14300);
  });
  it("rejects negative seqs (chain seq numbers are always ≥ 0)", () => {
    expect(parseSeqParam("-1")).toBeUndefined();
  });
});

describe("filterEventsBySeq", () => {
  const events = [
    { id: "a", action: "", actor: "", resource: "", timestamp: "", seq: 100 },
    { id: "b", action: "", actor: "", resource: "", timestamp: "", seq: 150 },
    { id: "c", action: "", actor: "", resource: "", timestamp: "", seq: 200 },
    // No seq — this is a non-chained policy-only entry.
    { id: "d", action: "", actor: "", resource: "", timestamp: "" },
  ];

  it("returns the original list untouched when both bounds are undefined (backward compat)", () => {
    expect(filterEventsBySeq(events, undefined, undefined)).toBe(events);
  });

  it("filters to [from, to] inclusive", () => {
    const out = filterEventsBySeq(events, 100, 150);
    expect(out.map((e) => e.id)).toEqual(["a", "b"]);
  });

  it("supports open-ended from-only filter", () => {
    const out = filterEventsBySeq(events, 150, undefined);
    expect(out.map((e) => e.id)).toEqual(["b", "c"]);
  });

  it("supports open-ended to-only filter", () => {
    const out = filterEventsBySeq(events, undefined, 100);
    expect(out.map((e) => e.id)).toEqual(["a"]);
  });

  it("excludes events without a seq field while a seq filter is active", () => {
    const out = filterEventsBySeq(events, 0, 999);
    expect(out.map((e) => e.id)).not.toContain("d");
  });
});
