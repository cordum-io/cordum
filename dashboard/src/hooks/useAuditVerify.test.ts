import { describe, it, expect } from "vitest";
import type {
  ChainVerifyGap,
  ChainVerifyGapType,
  ChainVerifyResult,
  ChainVerifyStatus,
} from "./useAuditVerify";

// useAuditVerify is a thin wrapper around the existing
// useAuditChainVerify hook — the serious behaviour is the Zustand
// persistence side-effect (tested in state/verification.test.ts) and
// the type alias contract. We pin the contract here so a rename in
// the upstream hook breaks THIS suite first and gives a clear signal.

describe("useAuditVerify types", () => {
  it("exports ChainVerifyStatus covering all three gateway statuses", () => {
    const statuses: ChainVerifyStatus[] = ["ok", "compromised", "partial"];
    expect(statuses).toHaveLength(3);
  });

  it("exports ChainVerifyGapType covering all four gap kinds", () => {
    const types: ChainVerifyGapType[] = [
      "missing",
      "out_of_order",
      "hash_mismatch",
      "retention_trimmed",
    ];
    expect(types).toHaveLength(4);
  });

  it("ChainVerifyGap has the expected shape", () => {
    const g: ChainVerifyGap = { at_seq: 42, type: "missing" };
    expect(g.at_seq).toBe(42);
    expect(g.type).toBe("missing");
  });

  it("ChainVerifyResult shape compiles with all optional + required fields", () => {
    const r: ChainVerifyResult = {
      status: "ok",
      total_events: 10,
      verified_events: 10,
      gaps: [],
      retention_boundary_seq: 1,
      retention_window_hours: 168,
      first_seq: 1,
      last_seq: 10,
    };
    expect(r.status).toBe("ok");
    expect(r.gaps).toEqual([]);
  });
});
