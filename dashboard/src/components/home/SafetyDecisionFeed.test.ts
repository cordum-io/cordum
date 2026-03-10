import { describe, it, expect } from "vitest";

describe("SafetyDecisionFeed", () => {
  it("exports SafetyDecisionFeed component", async () => {
    const mod = await import("./SafetyDecisionFeed");
    expect(mod.SafetyDecisionFeed).toBeDefined();
    expect(typeof mod.SafetyDecisionFeed).toBe("function");
  });
});
