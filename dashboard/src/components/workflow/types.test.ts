import { describe, it, expect } from "vitest";
import type {
  BuilderNodeType,
  BuilderNodeData,
  WorkerNodeData,
  ApprovalNodeData,
  ConditionNodeData,
  DelayNodeData,
  LoopNodeData,
  ParallelNodeData,
  SubworkflowNodeData,
  NodeConfig,
  PackTopic,
  DragData,
} from "./types";

describe("BuilderNodeType", () => {
  it("should include all expected node types", () => {
    const types: BuilderNodeType[] = [
      "worker",
      "approval",
      "condition",
      "delay",
      "loop",
      "parallel",
      "subworkflow",
    ];

    types.forEach((type) => {
      expect(type).toBeDefined();
    });
  });
});

describe("WorkerNodeData", () => {
  it("should accept valid worker node data", () => {
    const workerData: WorkerNodeData = {
      nodeType: "worker",
      label: "Process Data",
      stepId: "step-1",
      topic: "data.process",
      packId: "data-pack",
      capability: "transform",
      riskTags: ["data-access"],
      requires: ["read", "write"],
      timeoutSec: 300,
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(workerData.nodeType).toBe("worker");
    expect(workerData.topic).toBe("data.process");
    expect(workerData.packId).toBe("data-pack");
    expect(workerData.riskTags).toContain("data-access");
  });

  it("should support retry configuration", () => {
    const workerData: WorkerNodeData = {
      nodeType: "worker",
      label: "Retry Worker",
      stepId: "step-1",
      onDelete: () => {},
      onSelect: () => {},
      retry: {
        maxRetries: 3,
        initialBackoffSec: 1,
        maxBackoffSec: 60,
        multiplier: 2,
      },
    };

    expect(workerData.retry?.maxRetries).toBe(3);
    expect(workerData.retry?.multiplier).toBe(2);
  });
});

describe("ConditionNodeData", () => {
  it("should require condition expression", () => {
    const conditionData: ConditionNodeData = {
      nodeType: "condition",
      label: "Check Status",
      stepId: "step-1",
      condition: "{{ input.status == 'active' }}",
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(conditionData.nodeType).toBe("condition");
    expect(conditionData.condition).toContain("status");
  });
});

describe("DelayNodeData", () => {
  it("should support delay in seconds", () => {
    const delayData: DelayNodeData = {
      nodeType: "delay",
      label: "Wait 5 minutes",
      stepId: "step-1",
      delaySec: 300,
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(delayData.delaySec).toBe(300);
  });

  it("should support delay until expression", () => {
    const delayData: DelayNodeData = {
      nodeType: "delay",
      label: "Wait until",
      stepId: "step-1",
      delayUntil: "{{ input.scheduledTime }}",
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(delayData.delayUntil).toBeDefined();
  });
});

describe("LoopNodeData", () => {
  it("should require forEach expression", () => {
    const loopData: LoopNodeData = {
      nodeType: "loop",
      label: "Process Items",
      stepId: "step-1",
      forEach: "{{ input.items }}",
      maxParallel: 5,
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(loopData.nodeType).toBe("loop");
    expect(loopData.forEach).toContain("items");
    expect(loopData.maxParallel).toBe(5);
  });
});

describe("ParallelNodeData", () => {
  it("should support branch configuration", () => {
    const parallelData: ParallelNodeData = {
      nodeType: "parallel",
      label: "Fan Out",
      stepId: "step-1",
      branches: ["branch-1", "branch-2", "branch-3"],
      waitAll: true,
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(parallelData.branches).toHaveLength(3);
    expect(parallelData.waitAll).toBe(true);
  });
});

describe("SubworkflowNodeData", () => {
  it("should support subworkflow configuration", () => {
    const subworkflowData: SubworkflowNodeData = {
      nodeType: "subworkflow",
      label: "Call Nested",
      stepId: "step-1",
      subworkflowId: "workflow-123",
      input: { param1: "value1" },
      onDelete: () => {},
      onSelect: () => {},
    };

    expect(subworkflowData.subworkflowId).toBe("workflow-123");
    expect(subworkflowData.input?.param1).toBe("value1");
  });
});

describe("PackTopic", () => {
  it("should contain pack and topic information", () => {
    const topic: PackTopic = {
      packId: "ai-pack",
      packTitle: "AI Capabilities",
      topicName: "ai.generate",
      capability: "text-generation",
      riskTags: ["ai", "cost"],
      requires: ["api-key"],
    };

    expect(topic.packId).toBe("ai-pack");
    expect(topic.topicName).toBe("ai.generate");
    expect(topic.capability).toBe("text-generation");
    expect(topic.riskTags).toContain("ai");
  });
});

describe("DragData", () => {
  it("should support node drag data", () => {
    const nodeDrag: DragData = {
      type: "node",
      nodeType: "worker",
    };

    expect(nodeDrag.type).toBe("node");
    if (nodeDrag.type === "node") {
      expect(nodeDrag.nodeType).toBe("worker");
    }
  });

  it("should support pack drag data", () => {
    const packTopic: PackTopic = {
      packId: "test-pack",
      topicName: "test.topic",
    };

    const packDrag: DragData = {
      type: "pack",
      topic: packTopic,
    };

    expect(packDrag.type).toBe("pack");
    if (packDrag.type === "pack") {
      expect(packDrag.topic.topicName).toBe("test.topic");
    }
  });
});

describe("NodeConfig", () => {
  it("should define node configuration structure", () => {
    const config: NodeConfig = {
      type: "worker",
      label: "Worker",
      description: "Execute a job",
      icon: "WO",
      color: "bg-accent",
      outputs: [{ id: "output", label: "Output" }],
      defaultData: {
        nodeType: "worker",
        label: "Worker Step",
      },
    };

    expect(config.type).toBe("worker");
    expect(config.icon).toHaveLength(2);
    expect(config.outputs[0].id).toBe("output");
  });

  it("should support multiple outputs for branching nodes", () => {
    const config: NodeConfig = {
      type: "condition",
      label: "Condition",
      description: "If/else branching",
      icon: "IF",
      color: "bg-info",
      outputs: [
        { id: "true", label: "True" },
        { id: "false", label: "False" },
      ],
      defaultData: {
        nodeType: "condition",
        label: "Condition",
        condition: "",
      },
    };

    expect(config.outputs).toHaveLength(2);
    expect(config.outputs.find((o) => o.id === "true")).toBeDefined();
    expect(config.outputs.find((o) => o.id === "false")).toBeDefined();
  });
});
