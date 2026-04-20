import { describe, it, expect } from "vitest";
import { cn, isValidResourceId } from "./utils";

describe("cn", () => {
  it("merges class names", () => {
    expect(cn("foo", "bar")).toBe("foo bar");
  });

  it("handles conditional classes", () => {
    expect(cn("base", false && "hidden", "visible")).toBe("base visible");
  });

  it("resolves tailwind conflicts", () => {
    // twMerge resolves p-4 vs p-2 to the last one
    expect(cn("p-4", "p-2")).toBe("p-2");
  });

  it("handles empty input", () => {
    expect(cn()).toBe("");
  });
});

describe("isValidResourceId", () => {
  it("accepts valid alphanumeric IDs", () => {
    expect(isValidResourceId("job-123")).toBe(true);
    expect(isValidResourceId("abc")).toBe(true);
    expect(isValidResourceId("a")).toBe(true);
  });

  it("accepts IDs with dots, hyphens, underscores, and colons", () => {
    expect(isValidResourceId("my-job.v2")).toBe(true);
    expect(isValidResourceId("run_001")).toBe(true);
    expect(isValidResourceId("wf:step-1")).toBe(true);
  });

  it("accepts UUID-style IDs", () => {
    expect(isValidResourceId("550e8400-e29b-41d4-a716-446655440000")).toBe(true);
  });

  it("rejects path traversal attempts", () => {
    expect(isValidResourceId("../../admin")).toBe(false);
    expect(isValidResourceId("../etc/passwd")).toBe(false);
  });

  it("rejects whitespace-only strings", () => {
    expect(isValidResourceId("   ")).toBe(false);
  });

  it("rejects empty string and undefined", () => {
    expect(isValidResourceId("")).toBe(false);
    expect(isValidResourceId(undefined)).toBe(false);
  });

  it("rejects special characters", () => {
    expect(isValidResourceId("<script>")).toBe(false);
    expect(isValidResourceId("id&x=1")).toBe(false);
    expect(isValidResourceId("id/path")).toBe(false);
  });

  it("rejects IDs starting with non-alphanumeric", () => {
    expect(isValidResourceId(".hidden")).toBe(false);
    expect(isValidResourceId("-flag")).toBe(false);
  });

  it("rejects IDs over 128 characters", () => {
    expect(isValidResourceId("a".repeat(128))).toBe(true);
    expect(isValidResourceId("a".repeat(129))).toBe(false);
  });
});
