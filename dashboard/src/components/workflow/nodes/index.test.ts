import { describe, it, expect } from "vitest";
import { NODE_CONFIGS, ALL_NODE_TYPES, SUPPORTED_NODE_TYPES, generateStepId } from "./index";
import type { BuilderNodeType } from "../types";

describe("NODE_CONFIGS", () => {
  it("should have configuration for all node types", () => {
    ALL_NODE_TYPES.forEach((type) => {
      expect(NODE_CONFIGS[type]).toBeDefined();
      expect(NODE_CONFIGS[type].type).toBe(type);
    });
  });

  it("should have required fields for each config", () => {
    Object.values(NODE_CONFIGS).forEach((config) => {
      expect(config.type).toBeDefined();
      expect(config.label).toBeDefined();
      expect(config.description).toBeDefined();
      expect(config.icon).toBeDefined();
      expect(config.icon).toHaveLength(2); // 2-letter icon
      expect(config.color).toBeDefined();
      expect(config.outputs).toBeDefined();
      expect(Array.isArray(config.outputs)).toBe(true);
      expect(config.outputs.length).toBeGreaterThan(0);
      expect(config.defaultData).toBeDefined();
    });
  });

  describe("worker node", () => {
    it("should have correct configuration", () => {
      const config = NODE_CONFIGS.worker;
      expect(config.label).toBe("Worker");
      expect(config.icon).toBe("WO");
      expect(config.outputs).toHaveLength(1);
      expect(config.outputs[0].id).toBe("output");
      expect(config.defaultData.nodeType).toBe("worker");
      expect(config.defaultData.topic).toBe("job.default");
    });
  });

  describe("approval node", () => {
    it("should have correct configuration", () => {
      const config = NODE_CONFIGS.approval;
      expect(config.label).toBe("Approval");
      expect(config.icon).toBe("AP");
      expect(config.outputs).toHaveLength(1);
      expect(config.outputs[0].id).toBe("approved");
      expect(config.defaultData.nodeType).toBe("approval");
    });
  });

  describe("condition node", () => {
    it("should have a single output", () => {
      const config = NODE_CONFIGS.condition;
      expect(config.label).toBe("Condition");
      expect(config.icon).toBe("IF");
      expect(config.outputs).toHaveLength(1);
      expect(config.outputs[0].id).toBe("output");
      expect(config.defaultData.nodeType).toBe("condition");
      expect(config.defaultData.condition).toBeDefined();
    });
  });

  describe("delay node", () => {
    it("should have correct configuration", () => {
      const config = NODE_CONFIGS.delay;
      expect(config.label).toBe("Delay");
      expect(config.icon).toBe("DL");
      expect(config.outputs).toHaveLength(1);
      expect(config.defaultData.nodeType).toBe("delay");
      expect(config.defaultData.delaySec).toBe(60);
    });
  });

  describe("loop node", () => {
    it("should have a single output", () => {
      const config = NODE_CONFIGS.loop;
      expect(config.label).toBe("Loop");
      expect(config.icon).toBe("LP");
      expect(config.outputs).toHaveLength(1);
      expect(config.outputs[0].id).toBe("output");
      expect(config.defaultData.nodeType).toBe("loop");
      expect(config.defaultData.forEach).toBeDefined();
      expect(config.defaultData.maxParallel).toBe(1);
    });
  });

  describe("parallel node", () => {
    it("should have correct configuration", () => {
      const config = NODE_CONFIGS.parallel;
      expect(config.label).toBe("Parallel");
      expect(config.icon).toBe("PA");
      expect(config.outputs).toHaveLength(1);
      expect(config.defaultData.nodeType).toBe("parallel");
      expect(config.defaultData.branches).toEqual([]);
      expect(config.defaultData.waitAll).toBe(true);
    });
  });

  describe("subworkflow node", () => {
    it("should have correct configuration", () => {
      const config = NODE_CONFIGS.subworkflow;
      expect(config.label).toBe("Subworkflow");
      expect(config.icon).toBe("SW");
      expect(config.outputs).toHaveLength(1);
      expect(config.defaultData.nodeType).toBe("subworkflow");
    });
  });
});

describe("ALL_NODE_TYPES", () => {
  it("should contain all expected node types", () => {
    const expectedTypes: BuilderNodeType[] = [
      "worker",
      "approval",
      "condition",
      "delay",
      "loop",
      "parallel",
      "subworkflow",
    ];

    expect(ALL_NODE_TYPES).toHaveLength(expectedTypes.length);
    expectedTypes.forEach((type) => {
      expect(ALL_NODE_TYPES).toContain(type);
    });
  });
});

describe("SUPPORTED_NODE_TYPES", () => {
  it("should contain only engine-backed node types", () => {
    const expectedTypes: BuilderNodeType[] = [
      "worker",
      "approval",
      "condition",
      "delay",
      "loop",
    ];

    expect(SUPPORTED_NODE_TYPES).toHaveLength(expectedTypes.length);
    expectedTypes.forEach((type) => {
      expect(SUPPORTED_NODE_TYPES).toContain(type);
    });
  });
});

describe("generateStepId", () => {
  it("should generate unique IDs", () => {
    const id1 = generateStepId("worker");
    const id2 = generateStepId("worker");
    expect(id1).not.toBe(id2);
  });

  it("should include node type in ID", () => {
    const workerId = generateStepId("worker");
    const approvalId = generateStepId("approval");
    const conditionId = generateStepId("condition");

    expect(workerId).toMatch(/^worker-/);
    expect(approvalId).toMatch(/^approval-/);
    expect(conditionId).toMatch(/^condition-/);
  });

  it("should generate valid string IDs", () => {
    ALL_NODE_TYPES.forEach((type) => {
      const id = generateStepId(type);
      expect(typeof id).toBe("string");
      expect(id.length).toBeGreaterThan(0);
      // Should not contain spaces or special characters
      expect(id).toMatch(/^[a-z0-9-]+$/);
    });
  });
});
