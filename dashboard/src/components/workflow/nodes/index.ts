export { BaseNode } from "./BaseNode";
export { ApprovalNode } from "./ApprovalNode";
export { DelayNode } from "./DelayNode";
export { ConditionNode } from "./ConditionNode";
export { NotifyNode } from "./NotifyNode";
export { FanOutNode } from "./FanOutNode";
export { ParallelNode } from "./ParallelNode";
export { AgentTaskNode } from "./AgentTaskNode";
export { PackActionNode } from "./PackActionNode";
export { ToolCallNode } from "./ToolCallNode";
export { HttpNode } from "./HttpNode";
export { TransformNode } from "./TransformNode";
export { SwitchNode } from "./SwitchNode";
export { LoopNode } from "./LoopNode";
export { SubWorkflowNode } from "./SubWorkflowNode";
export { ErrorTriggerNode } from "./ErrorTriggerNode";

import type { BuilderNodeType, NodeConfig } from "../types";

// ---------------------------------------------------------------------------
// Node configuration registry
// ---------------------------------------------------------------------------

export const NODE_CONFIGS: Record<BuilderNodeType, NodeConfig> = {
  worker: {
    type: "worker",
    label: "Worker",
    description: "Dispatches a job to a worker pool",
    icon: "WO",
    color: "bg-primary/10",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: { nodeType: "worker", label: "Worker", stepId: "", topic: "job.default", onDelete: () => {}, onSelect: () => {} },
  },
  approval: {
    type: "approval",
    label: "Approval",
    description: "Human approval gate",
    icon: "AP",
    color: "bg-[var(--color-warning)]/10",
    outputs: [{ id: "approved", label: "Approved" }],
    defaultData: { nodeType: "approval", label: "Approval", stepId: "", onDelete: () => {}, onSelect: () => {} },
  },
  condition: {
    type: "condition",
    label: "Condition",
    description: "If/else branching",
    icon: "IF",
    color: "bg-[var(--color-info)]/10",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: { nodeType: "condition", label: "Condition", stepId: "", condition: "", onDelete: () => {}, onSelect: () => {} },
  },
  delay: {
    type: "delay",
    label: "Delay",
    description: "Wait/timer step",
    icon: "DL",
    color: "bg-[var(--color-warning)]/10",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: { nodeType: "delay", label: "Delay", stepId: "", delaySec: 60, onDelete: () => {}, onSelect: () => {} },
  },
  loop: {
    type: "loop",
    label: "Loop",
    description: "Iterate over items",
    icon: "LP",
    color: "bg-[var(--color-info)]/10",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: { nodeType: "loop", label: "Loop", stepId: "", forEach: "", maxParallel: 1, onDelete: () => {}, onSelect: () => {} },
  },
  parallel: {
    type: "parallel",
    label: "Parallel",
    description: "Concurrent branches",
    icon: "PA",
    color: "bg-primary/10",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: { nodeType: "parallel", label: "Parallel", stepId: "", branches: [], waitAll: true, onDelete: () => {}, onSelect: () => {} },
  },
  subworkflow: {
    type: "subworkflow",
    label: "Subworkflow",
    description: "Nested workflow call",
    icon: "SW",
    color: "bg-[var(--color-success)]/10",
    outputs: [{ id: "output", label: "Output" }],
    defaultData: { nodeType: "subworkflow", label: "Subworkflow", stepId: "", onDelete: () => {}, onSelect: () => {} },
  },
};

export const ALL_NODE_TYPES: BuilderNodeType[] = [
  "worker",
  "approval",
  "condition",
  "delay",
  "loop",
  "parallel",
  "subworkflow",
];

export const SUPPORTED_NODE_TYPES: BuilderNodeType[] = [
  "worker",
  "approval",
  "condition",
  "delay",
  "loop",
];

export function generateStepId(nodeType: string): string {
  const rand = Math.random().toString(36).slice(2, 10);
  return `${nodeType}-${rand}`;
}
