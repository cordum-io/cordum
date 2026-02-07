import {
  useCallback,
  useRef,
  type DragEvent,
} from "react";
import ReactFlow, {
  addEdge,
  Background,
  Controls,
  MiniMap,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type Node,
  type NodeTypes,
  type ReactFlowInstance,
} from "reactflow";
import "reactflow/dist/style.css";

import type { Workflow, WorkflowStep } from "../../api/types";
import { JobNode } from "./nodes/JobNode";
import { ApprovalNode } from "./nodes/ApprovalNode";
import { DelayNode } from "./nodes/DelayNode";
import { ConditionNode } from "./nodes/ConditionNode";
import { NotifyNode } from "./nodes/NotifyNode";
import { FanOutNode } from "./nodes/FanOutNode";

// ---------------------------------------------------------------------------
// Node type registry
// ---------------------------------------------------------------------------

const nodeTypes: NodeTypes = {
  job: JobNode,
  approval: ApprovalNode,
  delay: DelayNode,
  condition: ConditionNode,
  notify: NotifyNode,
  "fan-out": FanOutNode,
};

// ---------------------------------------------------------------------------
// Conversion: Workflow definition <-> ReactFlow graph (bidirectional)
// ---------------------------------------------------------------------------

export interface GraphData {
  nodes: Node[];
  edges: Edge[];
}

const GRID = 200;
const Y_STEP = 140;

export function definitionToGraph(workflow: Workflow): GraphData {
  const nodes: Node[] = [];
  const edges: Edge[] = [];
  const idxMap = new Map<string, number>();

  workflow.steps.forEach((step, i) => {
    idxMap.set(step.id, i);
  });

  workflow.steps.forEach((step, i) => {
    // Lay out vertically; spread columns for fan-out siblings
    const deps = step.dependsOn ?? [];
    let x = 300;
    let y = i * Y_STEP + 40;

    if (deps.length > 0) {
      // Position below the first dependency
      const parentIdx = idxMap.get(deps[0]);
      if (parentIdx !== undefined) {
        y = parentIdx * Y_STEP + Y_STEP + 40;
      }
    }

    // Spread siblings horizontally
    const siblings = workflow.steps.filter(
      (s) =>
        s.id !== step.id &&
        JSON.stringify(s.dependsOn) === JSON.stringify(deps),
    );
    const sibIdx = siblings.findIndex((s) => s.id === step.id);
    if (sibIdx > 0) {
      x += sibIdx * GRID;
    }

    nodes.push({
      id: step.id,
      type: step.type,
      position: { x, y },
      data: {
        label: step.name || step.id,
        stepId: step.id,
        stepType: step.type,
        config: step.config ?? {},
      },
    });

    for (const dep of deps) {
      edges.push({
        id: `e-${dep}-${step.id}`,
        source: dep,
        target: step.id,
        type: "smoothstep",
        animated: false,
      });
    }
  });

  return { nodes, edges };
}

export function graphToDefinition(
  nodes: Node[],
  edges: Edge[],
  baseMeta?: Partial<Workflow>,
): Partial<Workflow> {
  const steps: WorkflowStep[] = nodes.map((n) => {
    const incoming = edges.filter((e) => e.target === n.id).map((e) => e.source);
    return {
      id: n.id,
      name: (n.data?.label as string) ?? n.id,
      type: n.type ?? "job",
      config: (n.data?.config as Record<string, unknown>) ?? {},
      dependsOn: incoming.length > 0 ? incoming : undefined,
    };
  });

  return {
    ...baseMeta,
    steps,
  };
}

// ---------------------------------------------------------------------------
// Canvas component
// ---------------------------------------------------------------------------

let nodeId = 0;
function nextNodeId() {
  nodeId += 1;
  return `node-${nodeId}`;
}

export interface WorkflowCanvasProps {
  initialWorkflow?: Workflow;
  onNodesChange?: (nodes: Node[]) => void;
  onEdgesChange?: (edges: Edge[]) => void;
  onNodeSelect?: (node: Node | null) => void;
  /** Expose nodes/edges via ref for parent to read */
  graphRef?: React.MutableRefObject<{ nodes: Node[]; edges: Edge[] } | null>;
}

export function WorkflowCanvas({
  initialWorkflow,
  onNodesChange: onNodesChangeProp,
  onEdgesChange: onEdgesChangeProp,
  onNodeSelect,
  graphRef,
}: WorkflowCanvasProps) {
  const initial = initialWorkflow ? definitionToGraph(initialWorkflow) : { nodes: [], edges: [] };
  const [nodes, setNodes, handleNodesChange] = useNodesState(initial.nodes);
  const [edges, setEdges, handleEdgesChange] = useEdgesState(initial.edges);
  const rfInstance = useRef<ReactFlowInstance | null>(null);

  // Keep parent ref in sync
  if (graphRef) {
    graphRef.current = { nodes, edges };
  }

  // Notify parent of changes
  const onNodesChangeWrap = useCallback(
    (changes: Parameters<typeof handleNodesChange>[0]) => {
      handleNodesChange(changes);
      onNodesChangeProp?.(nodes);
    },
    [handleNodesChange, onNodesChangeProp, nodes],
  );

  const onEdgesChangeWrap = useCallback(
    (changes: Parameters<typeof handleEdgesChange>[0]) => {
      handleEdgesChange(changes);
      onEdgesChangeProp?.(edges);
    },
    [handleEdgesChange, onEdgesChangeProp, edges],
  );

  const onConnect = useCallback(
    (connection: Connection) => {
      setEdges((eds) => addEdge({ ...connection, type: "smoothstep" }, eds));
    },
    [setEdges],
  );

  // Drag-and-drop from palette
  const onDragOver = useCallback((event: DragEvent) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  }, []);

  const onDrop = useCallback(
    (event: DragEvent) => {
      event.preventDefault();
      const type = event.dataTransfer.getData("application/reactflow");
      if (!type || !rfInstance.current) return;

      const position = rfInstance.current.screenToFlowPosition({
        x: event.clientX,
        y: event.clientY,
      });

      const id = nextNodeId();
      const newNode: Node = {
        id,
        type,
        position,
        data: {
          label: `${type} ${id.split("-")[1]}`,
          stepId: id,
          stepType: type,
          config: {},
        },
      };

      setNodes((nds) => [...nds, newNode]);
    },
    [setNodes],
  );

  const onNodeClick = useCallback(
    (_: React.MouseEvent, node: Node) => {
      onNodeSelect?.(node);
    },
    [onNodeSelect],
  );

  const onPaneClick = useCallback(() => {
    onNodeSelect?.(null);
  }, [onNodeSelect]);

  return (
    <div className="h-full w-full">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChangeWrap}
        onEdgesChange={onEdgesChangeWrap}
        onConnect={onConnect}
        onDragOver={onDragOver}
        onDrop={onDrop}
        onInit={(instance) => {
          rfInstance.current = instance;
        }}
        onNodeClick={onNodeClick}
        onPaneClick={onPaneClick}
        nodeTypes={nodeTypes}
        defaultEdgeOptions={{ type: "smoothstep", animated: false }}
        fitView
        snapToGrid
        snapGrid={[20, 20]}
      >
        <Background gap={20} size={1} />
        <Controls />
        <MiniMap
          nodeStrokeWidth={3}
          className="!bg-surface1 !border-border"
        />
      </ReactFlow>
    </div>
  );
}
