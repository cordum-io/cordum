import type { NodeConfig, BuilderNodeType } from "../types";

// Node configurations for the sidebar and creation
export const NODE_CONFIGS: Record<BuilderNodeType, NodeConfig> = {
  worker: {
    type: "worker",
    label: "Worker",
    description: "Execute a job via topic",
    icon: "WO",
    color: "bg-accent",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: {
      nodeType: "worker",
      label: "Worker Step",
      topic: "job.default",
    },
  },
  approval: {
    type: "approval",
    label: "Approval",
    description: "Human approval gate",
    icon: "AP",
    color: "bg-warning",
    outputs: [{ id: "approved", label: "Approved" }],
    defaultData: {
      nodeType: "approval",
      label: "Approval Gate",
    },
  },
  condition: {
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
      condition: "{{ input.value == true }}",
    },
  },
  delay: {
    type: "delay",
    label: "Delay",
    description: "Wait or schedule",
    icon: "DL",
    color: "bg-muted",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: {
      nodeType: "delay",
      label: "Delay",
      delaySec: 60,
    },
  },
  loop: {
    type: "loop",
    label: "Loop",
    description: "Iterate over items",
    icon: "LP",
    color: "bg-purple-500",
    outputs: [
      { id: "body", label: "Body" },
      { id: "done", label: "Done" },
    ],
    defaultData: {
      nodeType: "loop",
      label: "Loop",
      forEach: "{{ input.items }}",
      maxParallel: 1,
    },
  },
  parallel: {
    type: "parallel",
    label: "Parallel",
    description: "Concurrent execution",
    icon: "PA",
    color: "bg-cyan-500",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: {
      nodeType: "parallel",
      label: "Parallel",
      branches: [],
      waitAll: true,
    },
  },
  subworkflow: {
    type: "subworkflow",
    label: "Subworkflow",
    description: "Nested workflow",
    icon: "SW",
    color: "bg-indigo-500",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: {
      nodeType: "subworkflow",
      label: "Subworkflow",
    },
  },
};

// Get all node types for sidebar
export const ALL_NODE_TYPES: BuilderNodeType[] = [
  "worker",
  "approval",
  "condition",
  "delay",
  "loop",
  "parallel",
  "subworkflow",
];

// Generate unique step ID
export function generateStepId(type: BuilderNodeType): string {
  const timestamp = Date.now().toString(36);
  const random = Math.random().toString(36).substring(2, 6);
  return `${type}-${timestamp}-${random}`;
}
