import { WorkerNode } from "./nodes/WorkerNode";
import { ApprovalNode } from "./nodes/ApprovalNode";
import { ConditionNode } from "./nodes/ConditionNode";
import { DelayNode } from "./nodes/DelayNode";
import { LoopNode } from "./nodes/LoopNode";
import { ParallelNode } from "./nodes/ParallelNode";
import { SubworkflowNode } from "./nodes/SubworkflowNode";

// React Flow node type registry
export const builderNodeTypes = {
  worker: WorkerNode,
  approval: ApprovalNode,
  condition: ConditionNode,
  delay: DelayNode,
  loop: LoopNode,
  parallel: ParallelNode,
  subworkflow: SubworkflowNode,
};
