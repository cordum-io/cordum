import { describe, it, expect } from "vitest";
import { formatCount } from "./format";

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
