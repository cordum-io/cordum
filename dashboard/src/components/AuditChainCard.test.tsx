import { describe, it, expect, beforeEach } from "vitest";
import {
  deriveStatus,
  summarizeGaps,
} from "./AuditChainCard";
import type { AuditVerifyResult, AuditVerifyGap } from "../hooks/useAuditChainVerify";
import { useAuditChainUI } from "../state/auditChain";

// ---------------------------------------------------------------------------
// Fixture factories
// ---------------------------------------------------------------------------

function intact(): AuditVerifyResult {
  return {
    status: "ok",
    total_events: 42,
    verified_events: 42,
    gaps: [],
    retention_boundary_seq: 1,
    retention_window_hours: 168,
    first_seq: 1,
    last_seq: 42,
  };
}

function compromised(): AuditVerifyResult {
  return {
    status: "compromised",
    total_events: 5,
    verified_events: 4,
    gaps: [
      { at_seq: 1, type: "retention_trimmed" },
      { at_seq: 2, type: "retention_trimmed" },
      { at_seq: 3, type: "retention_trimmed" },
      { at_seq: 6, type: "hash_mismatch" },
      { at_seq: 7, type: "missing" },
    ],
    retention_boundary_seq: 4,
    retention_window_hours: 168,
    first_seq: 4,
    last_seq: 8,
  };
}

function partial(): AuditVerifyResult {
  return {
    status: "partial",
    total_events: 10,
    verified_events: 10,
    gaps: [
      { at_seq: 1, type: "retention_trimmed" },
      { at_seq: 2, type: "retention_trimmed" },
    ],
    retention_boundary_seq: 3,
    retention_window_hours: 168,
    first_seq: 3,
    last_seq: 12,
  };
}

// ---------------------------------------------------------------------------
// deriveStatus: the state machine that maps react-query state to the UI key
// ---------------------------------------------------------------------------

describe("deriveStatus", () => {
  it("returns loading while the first request is in flight", () => {
    expect(
      deriveStatus({ isLoading: true, isError: false, data: undefined }).key,
    ).toBe("loading");
  });

  it("returns error when the first request fails", () => {
    expect(
      deriveStatus({ isLoading: false, isError: true, data: undefined }).key,
    ).toBe("error");
  });

  it("prefers last-known data over loading during a background refetch", () => {
    // react-query keeps data around during refetch; deriveStatus must
    // show the last verified status rather than flipping to "Verifying..."
    // every 5 minutes (that would churn the Command Center badge).
    const out = deriveStatus({ isLoading: true, isError: false, data: intact() });
    expect(out.key).toBe("ok");
    expect(out.data).toEqual(intact());
  });

  it("passes through ok / compromised / partial", () => {
    for (const status of ["ok", "compromised", "partial"] as const) {
      const data = { ...intact(), status };
      const out = deriveStatus({ isLoading: false, isError: false, data });
      expect(out.key).toBe(status);
    }
  });

  it("returns unknown when neither loading, error, nor data is available", () => {
    expect(
      deriveStatus({ isLoading: false, isError: false, data: undefined }).key,
    ).toBe("unknown");
  });
});

// ---------------------------------------------------------------------------
// summarizeGaps: bucket + cap logic; stable ordering so tampering rows
// always sort above retention_trimmed rows in the expanded panel.
// ---------------------------------------------------------------------------

describe("summarizeGaps", () => {
  it("groups gaps by type with stable ordering", () => {
    const gaps: AuditVerifyGap[] = [
      { at_seq: 11, type: "missing" },
      { at_seq: 12, type: "retention_trimmed" },
      { at_seq: 13, type: "hash_mismatch" },
      { at_seq: 14, type: "out_of_order" },
    ];
    const buckets = summarizeGaps(gaps);
    expect(buckets.map((b) => b.type)).toEqual([
      "hash_mismatch",
      "missing",
      "out_of_order",
      "retention_trimmed",
    ]);
  });

  it("caps per-bucket display at 10 seqs and records overflow", () => {
    const gaps: AuditVerifyGap[] = Array.from({ length: 25 }, (_, i) => ({
      at_seq: i + 1,
      type: "retention_trimmed",
    }));
    const buckets = summarizeGaps(gaps);
    expect(buckets).toHaveLength(1);
    expect(buckets[0].seqs).toHaveLength(10);
    expect(buckets[0].total).toBe(25);
    expect(buckets[0].overflow).toBe(15);
  });

  it("omits buckets with no entries", () => {
    expect(summarizeGaps([])).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// Zustand store: expanded state is per-tenant so two mounts of the card
// on different pages share the user's choice.
// ---------------------------------------------------------------------------

describe("useAuditChainUI", () => {
  beforeEach(() => {
    useAuditChainUI.setState({ expandedByTenant: {} });
  });

  it("toggles expanded independently per tenant", () => {
    const { toggleExpanded, isExpanded } = useAuditChainUI.getState();
    toggleExpanded("tenant-a");
    expect(isExpanded("tenant-a")).toBe(true);
    expect(isExpanded("tenant-b")).toBe(false);
    toggleExpanded("tenant-a");
    expect(isExpanded("tenant-a")).toBe(false);
  });

  it("setExpanded is idempotent", () => {
    const { setExpanded, isExpanded } = useAuditChainUI.getState();
    setExpanded("t", true);
    setExpanded("t", true);
    expect(isExpanded("t")).toBe(true);
    setExpanded("t", false);
    expect(isExpanded("t")).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// Integration sanity: the three gateway response shapes drive the expected
// UI state keys. This is the "msw mocks for the three states" assertion
// expressed at the data-plane boundary — the dashboard has no msw
// installed, so we feed the shapes directly into deriveStatus +
// summarizeGaps and assert the observable surface.
// ---------------------------------------------------------------------------

describe("three-state gateway response", () => {
  it("ok response produces no gap buckets and verified == total", () => {
    const data = intact();
    const out = deriveStatus({ isLoading: false, isError: false, data });
    expect(out.key).toBe("ok");
    expect(summarizeGaps(data.gaps)).toEqual([]);
    expect(data.verified_events).toBe(data.total_events);
  });

  it("compromised response separates tampering from retention_trimmed", () => {
    const data = compromised();
    const out = deriveStatus({ isLoading: false, isError: false, data });
    expect(out.key).toBe("compromised");
    const buckets = summarizeGaps(data.gaps);
    // hash_mismatch + missing rendered as tampering (danger/warning tones)
    expect(buckets.find((b) => b.type === "hash_mismatch")?.total).toBe(1);
    expect(buckets.find((b) => b.type === "missing")?.total).toBe(1);
    // retention_trimmed rendered as retention (muted tone)
    expect(buckets.find((b) => b.type === "retention_trimmed")?.total).toBe(3);
  });

  it("partial response contains only retention_trimmed gaps", () => {
    const data = partial();
    const out = deriveStatus({ isLoading: false, isError: false, data });
    expect(out.key).toBe("partial");
    const buckets = summarizeGaps(data.gaps);
    expect(buckets).toHaveLength(1);
    expect(buckets[0].type).toBe("retention_trimmed");
  });
});
