import { describe, expect, it } from "vitest";
import { resolveNodeMeta, normalizeStepType } from "./WorkflowCreatePage";

describe("WorkflowCreatePage node-type safety", () => {
  describe("resolveNodeMeta", () => {
    it("returns known metadata for registered types", () => {
      const meta = resolveNodeMeta("worker");
      expect(meta.type).toBe("worker");
      expect(meta.label).toBe("Worker");
      expect(meta.icon).toBeDefined();
    });

    it("returns UNKNOWN fallback for unregistered types (crash fix)", () => {
      const meta = resolveNodeMeta("http");
      expect(meta.type).toBe("unknown");
      expect(meta.label).toBe("Unknown");
      expect(meta.icon).toBeDefined();
      expect(meta.color).toContain("destructive");
    });

    it("returns UNKNOWN fallback for empty string", () => {
      expect(resolveNodeMeta("").type).toBe("unknown");
    });

    it("resolves all 7 registered node types without fallback", () => {
      const types = ["worker", "approval", "condition", "delay", "loop", "parallel", "subworkflow"];
      for (const t of types) {
        expect(resolveNodeMeta(t).type).toBe(t);
      }
    });
  });

  describe("normalizeStepType", () => {
    it("maps 'job' to 'worker'", () => {
      const { nodeType, originalType } = normalizeStepType("job");
      expect(nodeType).toBe("worker");
      expect(originalType).toBe("job");
    });

    it("maps 'sub-workflow' to 'subworkflow'", () => {
      const { nodeType, originalType } = normalizeStepType("sub-workflow");
      expect(nodeType).toBe("subworkflow");
      expect(originalType).toBe("sub-workflow");
    });

    it("passes through known types unchanged", () => {
      const { nodeType, originalType } = normalizeStepType("approval");
      expect(nodeType).toBe("approval");
      expect(originalType).toBe("approval");
    });

    it("maps unknown backend types to 'unknown' and preserves original", () => {
      const { nodeType, originalType } = normalizeStepType("fan-out");
      expect(nodeType).toBe("unknown");
      expect(originalType).toBe("fan-out");
    });

    it("handles empty/missing type gracefully", () => {
      const { nodeType } = normalizeStepType("unknown");
      expect(nodeType).toBe("unknown");
    });
  });
});
