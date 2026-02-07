import { useCallback, useMemo } from "react";
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  type Node,
  type NodeTypes,
} from "reactflow";
import "reactflow/dist/style.css";

import type { Workflow, WorkflowRun } from "../../../api/types";
import { RunOverlayNode } from "./RunOverlayNode";
import { buildRunGraph } from "./buildRunGraph";
import { cn } from "../../../lib/utils";

// ---------------------------------------------------------------------------
// Node type registry (stable reference outside component)
// ---------------------------------------------------------------------------

const nodeTypes: NodeTypes = {
  runOverlay: RunOverlayNode,
};

// ---------------------------------------------------------------------------
// RunDAG
// ---------------------------------------------------------------------------

interface RunDAGProps {
  workflow: Workflow;
  run?: WorkflowRun | null;
  onNodeClick?: (stepId: string) => void;
  className?: string;
}

export function RunDAG({ workflow, run, onNodeClick, className }: RunDAGProps) {
  const { nodes, edges } = useMemo(
    () => buildRunGraph(workflow, run),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [workflow.steps, run?.id, run?.updatedAt],
  );

  const handleNodeClick = useCallback(
    (_: React.MouseEvent, node: Node) => {
      const stepId = node.id;
      onNodeClick?.(stepId);
    },
    [onNodeClick],
  );

  return (
    <div className={cn("h-full w-full", className)}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={handleNodeClick}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable
        fitView
        defaultEdgeOptions={{ type: "smoothstep" }}
      >
        <Background gap={20} size={1} />
        <Controls showInteractive={false} />
        <MiniMap
          nodeStrokeWidth={3}
          className="!bg-surface1 !border-border"
        />
      </ReactFlow>
    </div>
  );
}
