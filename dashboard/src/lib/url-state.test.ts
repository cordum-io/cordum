import { describe, expect, it } from "vitest";
import {
  TIME_RANGE_BUCKETS,
  parseAsEnum,
  parseAsPage,
  parseAsSearchTerm,
  parseAsTimeRange,
} from "./url-state";

describe("parseAsSearchTerm", () => {
  it("parses an arbitrary string back to itself", () => {
    expect(parseAsSearchTerm.parse("payment fraud")).toBe("payment fraud");
  });

  it("round-trips through serialize", () => {
    const value = "deny+quarantine";
    expect(parseAsSearchTerm.parse(parseAsSearchTerm.serialize(value))).toBe(value);
  });

  it("falls back to empty string when the key is missing from the URL", () => {
    expect(parseAsSearchTerm.parseServerSide(undefined)).toBe("");
  });

  it("preserves Unicode and special chars across round-trip", () => {
    const value = "tenant=acme & policy:fraud — 🛡";
    expect(parseAsSearchTerm.parse(parseAsSearchTerm.serialize(value))).toBe(value);
  });
});

describe("parseAsPage", () => {
  it("parses a numeric string into an integer", () => {
    expect(parseAsPage.parse("7")).toBe(7);
  });

  it("rejects non-numeric input by returning null at the parser layer", () => {
    expect(parseAsPage.parse("not-a-number")).toBeNull();
  });

  it("falls back to 1 when the key is missing from the URL", () => {
    expect(parseAsPage.parseServerSide(undefined)).toBe(1);
  });

  it("substitutes the default when given an invalid value via parseServerSide", () => {
    expect(parseAsPage.parseServerSide("not-a-number")).toBe(1);
  });

  it("round-trips integers through serialize", () => {
    expect(parseAsPage.parse(parseAsPage.serialize(42))).toBe(42);
  });
});

describe("parseAsTimeRange", () => {
  it.each(TIME_RANGE_BUCKETS)("accepts the %s bucket", (bucket) => {
    expect(parseAsTimeRange.parse(bucket)).toBe(bucket);
  });

  it("returns null for a bucket outside the allowed set", () => {
    expect(parseAsTimeRange.parse("90d")).toBeNull();
  });

  it("round-trips a valid bucket through serialize", () => {
    expect(parseAsTimeRange.parse(parseAsTimeRange.serialize("7d"))).toBe("7d");
  });

  it("does not bake in a default — undefined parses to null at the parser layer", () => {
    expect(parseAsTimeRange.parseServerSide(undefined)).toBeNull();
  });
});

describe("parseAsEnum", () => {
  const decisionParser = parseAsEnum(["allow", "deny", "quarantine"] as const, "allow");

  it("accepts a value from the enum", () => {
    expect(decisionParser.parse("deny")).toBe("deny");
  });

  it("rejects a value outside the enum at the parser layer", () => {
    expect(decisionParser.parse("escalate")).toBeNull();
  });

  it("falls back to the configured default when the key is missing", () => {
    expect(decisionParser.parseServerSide(undefined)).toBe("allow");
  });

  it("falls back to the configured default for invalid values via parseServerSide", () => {
    expect(decisionParser.parseServerSide("escalate")).toBe("allow");
  });

  it("exposes the configured default on the builder", () => {
    expect(decisionParser.defaultValue).toBe("allow");
  });
});
