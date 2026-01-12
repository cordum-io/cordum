import { memo, useMemo } from "react";
import ReactFlow, {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  Position,
  type Edge,
  type Node,
  type NodeProps,
} from "reactflow";
import type { Workflow, WorkflowRun } from "../../types/api";

type WorkflowNodeData = {
  title: string;
  status?: string;
  type?: string;
  topic?: string;
  capability?: string;
};

const statusLabel = (status?: string) => {
  if (!status) {
    return "ready";
  }
  return status.replace(/_/g, " ");
};

const statusKey = (status?: string) => (status ? status.toLowerCase() : "ready");

const iconLabelForType = (type?: string) => {
  const normalized = (type || "step").replace(/[^a-z0-9]/gi, "");
  if (!normalized) {
    return "ST";
  }
  return normalized.slice(0, 2).toUpperCase();
};

const WorkflowNode = memo(({ data }: NodeProps<WorkflowNodeData>) => {
  const displayStatus = statusLabel(data.status);
  const displayType = data.type ? data.type.replace(/_/g, " ") : "step";
  const metaLine = data.topic || data.capability;

  return (
    <div className="workflow-node" data-status={statusKey(data.status)} data-type={(data.type || "step").toLowerCase()}>
      <Handle type="target" position={Position.Left} className="workflow-handle" />
      <div className="workflow-node__header">
        <div className="workflow-node__icon">{iconLabelForType(data.type)}</div>
        <div className="workflow-node__copy">
          <div className="workflow-node__title">{data.title}</div>
          <div className="workflow-node__type">{displayType}</div>
          {metaLine ? (
            <div className="workflow-node__meta" title={metaLine}>
              {metaLine}
            </div>
          ) : null}
        </div>
      </div>
      <div className="workflow-node__status">
        <span className="workflow-node__status-dot" />
        <span className="workflow-node__status-text">{displayStatus}</span>
      </div>
      <Handle type="source" position={Position.Right} className="workflow-handle" />
    </div>
  );
});

WorkflowNode.displayName = "WorkflowNode";

const nodeTypes = {
  workflowNode: WorkflowNode,
};

const defaultEdgeOptions = {
  type: "smoothstep",
  markerEnd: { type: MarkerType.ArrowClosed, color: "#9aa7b0" },
  style: { stroke: "#9aa7b0", strokeWidth: 1.4 },
};

const buildDag = (workflow?: Workflow, run?: WorkflowRun) => {
  const steps = workflow?.steps || {};
  const levels: Record<string, number> = {};

  const computeLevel = (id: string): number => {
    if (levels[id] !== undefined) {
      return levels[id];
    }
    const deps = steps[id]?.depends_on || [];
    if (deps.length === 0) {
      levels[id] = 0;
      return 0;
    }
    const level = Math.max(...deps.map((dep) => computeLevel(dep))) + 1;
    levels[id] = level;
    return level;
  };

  Object.keys(steps).forEach((id) => computeLevel(id));
  const levelCounts: Record<number, number> = {};

  const nodes: Node<WorkflowNodeData>[] = Object.keys(steps).map((id) => {
    const step = steps[id];
    const level = levels[id] ?? 0;
    const index = levelCounts[level] || 0;
    levelCounts[level] = index + 1;
    const status = run?.steps?.[id]?.status;
    return {
      id,
      type: "workflowNode",
      data: {
        title: step?.name || id,
        status,
        type: step?.type,
        topic: step?.topic,
        capability: step?.meta?.capability,
      },
      position: { x: level * 280, y: index * 150 },
    };
  });

  const edges: Edge[] = [];
  Object.entries(steps).forEach(([id, step]) => {
    step.depends_on?.forEach((dep) => {
      edges.push({ id: `${dep}-${id}`, source: dep, target: id });
    });
  });

  return { nodes, edges };
};

type WorkflowCanvasProps = {
  workflow?: Workflow;
  run?: WorkflowRun;
  height?: number;
};

export function WorkflowCanvas({ workflow, run, height = 420 }: WorkflowCanvasProps) {
  const dag = useMemo(() => buildDag(workflow, run), [workflow, run]);

  if (!workflow || dag.nodes.length === 0) {
    return (
      <div className="workflow-canvas empty" style={{ height }}>
        <div className="workflow-canvas__empty">No steps to display.</div>
      </div>
    );
  }

  return (
    <div className="workflow-canvas" style={{ height }}>
      <ReactFlow
        nodes={dag.nodes}
        edges={dag.edges}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        defaultEdgeOptions={defaultEdgeOptions}
        nodesDraggable={false}
        nodesConnectable={false}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#d0d7dd" />
        <Controls position="bottom-left" />
      </ReactFlow>
    </div>
  );
}
