import type { Node, Edge } from "reactflow";

// Node type identifiers
export type BuilderNodeType =
  | "worker"
  | "approval"
  | "condition"
  | "delay"
  | "loop"
  | "parallel"
  | "subworkflow";

// Base data shared by all nodes
export type BuilderNodeDataBase = {
  label: string;
  stepId: string;
  description?: string;
  condition?: string;
  onDelete: (id: string) => void;
  onSelect: (id: string) => void;
  selected?: boolean;
};

// Worker node - executes a job via topic
export type WorkerNodeData = BuilderNodeDataBase & {
  nodeType: "worker";
  topic?: string;
  packId?: string;
  capability?: string;
  inputSchema?: string;
  outputSchema?: string;
  riskTags?: string[];
  requires?: string[];
  timeoutSec?: number;
  retry?: {
    maxRetries?: number;
    initialBackoffSec?: number;
    maxBackoffSec?: number;
    multiplier?: number;
  };
};

// Approval node - human approval gate
export type ApprovalNodeData = BuilderNodeDataBase & {
  nodeType: "approval";
  approverRole?: string;
  approvalPolicy?: string;
};

// Condition node - if/else branching
export type ConditionNodeData = BuilderNodeDataBase & {
  nodeType: "condition";
  condition: string; // Expression to evaluate
  // Has two outputs: "true" and "false"
};

// Delay node - wait/timer
export type DelayNodeData = BuilderNodeDataBase & {
  nodeType: "delay";
  delaySec?: number;
  delayUntil?: string; // ISO date expression or cron
};

// Loop node - forEach iteration
export type LoopNodeData = BuilderNodeDataBase & {
  nodeType: "loop";
  forEach: string; // Expression yielding array
  maxParallel?: number;
  // Has two outputs: "body" (executed per item) and "done" (after all items)
};

// Parallel node - concurrent branches
export type ParallelNodeData = BuilderNodeDataBase & {
  nodeType: "parallel";
  branches: string[]; // IDs of branch nodes
  waitAll?: boolean;
};

// Subworkflow node - nested workflow call
export type SubworkflowNodeData = BuilderNodeDataBase & {
  nodeType: "subworkflow";
  subworkflowId?: string;
  input?: Record<string, unknown>;
};

// Union of all node data types
export type BuilderNodeData =
  | WorkerNodeData
  | ApprovalNodeData
  | ConditionNodeData
  | DelayNodeData
  | LoopNodeData
  | ParallelNodeData
  | SubworkflowNodeData;

// Typed node for React Flow
export type BuilderNode = Node<BuilderNodeData>;

// Edge with optional label for condition branches
export type BuilderEdgeData = {
  label?: string;
  condition?: "true" | "false" | "body" | "done";
};

export type BuilderEdge = Edge<BuilderEdgeData>;

// Node configuration for the sidebar
export type NodeConfig = {
  type: BuilderNodeType;
  label: string;
  description: string;
  icon: string; // 2-letter icon code
  color: string; // Tailwind color class
  outputs: { id: string; label: string }[];
  defaultData: Partial<BuilderNodeData>;
};

// Pack topic for drag-and-drop from sidebar
export type PackTopic = {
  packId: string;
  packTitle?: string;
  topicName: string;
  capability?: string;
  riskTags?: string[];
  requires?: string[];
};

// Drag transfer data
export type DragData =
  | { type: "node"; nodeType: BuilderNodeType }
  | { type: "pack"; topic: PackTopic };
