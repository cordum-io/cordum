import { describe, it, expect } from "vitest";
import { formatBytes, formatCount, formatDateTime } from "./format";

describe("formatCount", () => {
  it("returns plain number below 1000", () => {
    expect(formatCount(0)).toBe("0");
    expect(formatCount(1)).toBe("1");
    expect(formatCount(999)).toBe("999");
  });

  it("formats thousands as K", () => {
    expect(formatCount(1000)).toBe("1K");
    expect(formatCount(1500)).toBe("1.5K");
    expect(formatCount(10000)).toBe("10K");
    expect(formatCount(999999)).toBe("1000K");
  });

  it("formats millions as M", () => {
    expect(formatCount(1000000)).toBe("1M");
    expect(formatCount(1500000)).toBe("1.5M");
    expect(formatCount(10000000)).toBe("10M");
  });

  it("drops trailing .0", () => {
    expect(formatCount(2000)).toBe("2K");
    expect(formatCount(3000000)).toBe("3M");
  });
});

describe("formatDateTime", () => {
  it("delegates to toLocaleString", () => {
    const iso = "2026-05-09T12:34:56Z";
    expect(formatDateTime(iso)).toBe(new Date(iso).toLocaleString());
  });
});

describe("formatBytes", () => {
  describe("default options", () => {
    it("returns the default fallback for missing / invalid / negative / zero", () => {
      expect(formatBytes(undefined)).toBe("—");
      expect(formatBytes(null)).toBe("—");
      expect(formatBytes(NaN)).toBe("—");
      expect(formatBytes(-1)).toBe("—");
      expect(formatBytes(0)).toBe("—");
    });
    it("renders bytes under 1 KB without a decimal", () => {
      expect(formatBytes(1)).toBe("1 B");
      expect(formatBytes(1023)).toBe("1023 B");
    });
    it("renders KB at the 1024-byte boundary with 1 decimal", () => {
      expect(formatBytes(1024)).toBe("1.0 KB");
      expect(formatBytes(1536)).toBe("1.5 KB");
    });
    it("renders MB at the 1 MiB boundary with 2 decimals", () => {
      expect(formatBytes(1024 * 1024)).toBe("1.00 MB");
      expect(formatBytes(1024 * 1024 * 5 + 512 * 1024)).toBe("5.50 MB");
    });
    it("does NOT render GB when includeGB is false", () => {
      const fiveGiB = 5 * 1024 * 1024 * 1024;
      expect(formatBytes(fiveGiB)).toMatch(/MB$/);
    });
  });

  describe("fallback option", () => {
    it("uses the caller-provided fallback for invalid values", () => {
      expect(formatBytes(undefined, { fallback: "-" })).toBe("-");
      expect(formatBytes(undefined, { fallback: "unknown size" })).toBe("unknown size");
      expect(formatBytes(0, { fallback: "n/a" })).toBe("n/a");
    });
  });

  describe("zeroAsBytes option", () => {
    it("renders 0 as '0 B' when zeroAsBytes=true", () => {
      expect(formatBytes(0, { zeroAsBytes: true })).toBe("0 B");
    });
    it("still uses fallback for negative / NaN even with zeroAsBytes", () => {
      expect(formatBytes(-1, { zeroAsBytes: true })).toBe("—");
      expect(formatBytes(NaN, { zeroAsBytes: true })).toBe("—");
    });
  });

  describe("iec option", () => {
    it("uses KiB / MiB / GiB labels when iec=true", () => {
      expect(formatBytes(1536, { iec: true })).toBe("1.5 KiB");
      expect(formatBytes(1024 * 1024, { iec: true })).toBe("1.00 MiB");
      expect(formatBytes(1024 * 1024 * 1024, { iec: true, includeGB: true })).toBe("1.0 GiB");
    });
  });

  describe("includeGB option", () => {
    it("renders GB tier when value crosses the 1-GiB boundary", () => {
      expect(formatBytes(1024 * 1024 * 1024, { includeGB: true })).toBe("1.0 GB");
      expect(formatBytes(2.5 * 1024 * 1024 * 1024, { includeGB: true })).toBe("2.5 GB");
    });
    it("still renders MB below the 1-GiB boundary even with includeGB", () => {
      expect(formatBytes(500 * 1024 * 1024, { includeGB: true })).toMatch(/MB$/);
    });
  });
});
