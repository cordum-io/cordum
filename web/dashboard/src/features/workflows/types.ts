import type { Edge, Node } from "reactflow";

export const defaultTopics = [
  "job.echo",
  "job.chat.simple",
  "job.chat.advanced",
  "job.code.llm",
  "job.workflow.demo",
] as const;

export type WorkflowNodeType = "input" | "task" | "output" | "memory";

export type InputNodeData = {
  kind: "input";
  name: string;
  promptDefault: string;
  includeFilePath: boolean;
  filePathDefault: string;
  includeInstruction: boolean;
  instructionDefault: string;
};

export type TaskNodeData = {
  kind: "task";
  name: string;
  topic: string;
  promptTemplate: string;
  timeoutMs: number;
  retries: number;
};

export type WorkflowOutput = {
  key: string;
  template: string;
};

export type OutputNodeData = {
  kind: "output";
  name: string;
  outputs: WorkflowOutput[];
};

export type MemoryStrategy = "run" | "workflow" | "custom";

export type MemoryNodeData = {
  kind: "memory";
  name: string;
  strategy: MemoryStrategy;
  customMemoryId: string;
};

export type WorkflowNodeData = InputNodeData | TaskNodeData | OutputNodeData | MemoryNodeData;

export type WorkflowNode = Node<WorkflowNodeData, WorkflowNodeType>;
export type WorkflowEdge = Edge;

export type Workflow = {
  id: string;
  name: string;
  updatedAt: number;
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
  variablesSchema?: Record<string, unknown>;
};
