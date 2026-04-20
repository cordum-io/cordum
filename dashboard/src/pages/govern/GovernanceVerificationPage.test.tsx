import { describe, it, expect } from "vitest";
import {
  countSigned,
  sortBundles,
} from "./GovernanceVerificationPage";
import type { PolicyBundle } from "@/api/types";

// Render-heavy paths are exercised by the ChainIntegrityWidget and
// SignatureBadge test suites. This suite pins the page-level helpers
// (sortBundles / countSigned) that drive the summary header and the
// signed-first sort ordering.

function mk(id: string, signed?: boolean): PolicyBundle {
  return {
    id,
    name: id,
    rules: [],
    signed,
    signature:
      signed === true
        ? {
            algorithm: "ed25519",
            key_id: "key-1",
            value: "sig-abc",
            hash: "hash-abc",
            signed_bytes: 128,
          }
        : undefined,
  };
}

describe("countSigned", () => {
  it("splits bundles across signed / unsigned / unknown", () => {
    const out = countSigned([
      mk("a", true),
      mk("b", true),
      mk("c", false),
      mk("d", undefined),
    ]);
    expect(out).toEqual({ total: 4, signed: 2, unsigned: 1, unknown: 1 });
  });
  it("handles an empty list", () => {
    expect(countSigned([])).toEqual({ total: 0, signed: 0, unsigned: 0, unknown: 0 });
  });
});

describe("sortBundles", () => {
  const bundles = [
    mk("echo", true),
    mk("alpha", false),
    mk("bravo", true),
    mk("delta", undefined),
    mk("charlie", false),
  ];

  it("sorts by name asc (stable lexicographic)", () => {
    const out = sortBundles(bundles, { field: "name", dir: "asc" });
    expect(out.map((b) => b.id)).toEqual([
      "alpha",
      "bravo",
      "charlie",
      "delta",
      "echo",
    ]);
  });

  it("sorts by name desc", () => {
    const out = sortBundles(bundles, { field: "name", dir: "desc" });
    expect(out.map((b) => b.id)[0]).toBe("echo");
    expect(out.map((b) => b.id).at(-1)).toBe("alpha");
  });

  it("sorts by signed asc: signed → unsigned → unknown, then name", () => {
    const out = sortBundles(bundles, { field: "signed", dir: "asc" });
    const ids = out.map((b) => b.id);
    // signed cohort first (bravo, echo) in name order
    expect(ids[0]).toBe("bravo");
    expect(ids[1]).toBe("echo");
    // unsigned next (alpha, charlie)
    expect(ids[2]).toBe("alpha");
    expect(ids[3]).toBe("charlie");
    // unknown last (delta)
    expect(ids[4]).toBe("delta");
  });

  it("sorts by signed desc: unknown → unsigned → signed", () => {
    const out = sortBundles(bundles, { field: "signed", dir: "desc" });
    expect(out[0].id).toBe("delta");
    expect(out[out.length - 1].id === "bravo" || out[out.length - 1].id === "echo").toBe(true);
  });

  it("does not mutate the input array", () => {
    const input = [mk("a", true), mk("b", false)];
    const before = input.map((b) => b.id).join(",");
    sortBundles(input, { field: "name", dir: "desc" });
    expect(input.map((b) => b.id).join(",")).toBe(before);
  });
});
