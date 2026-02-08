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
import { AgentTaskNode } from "./nodes/AgentTaskNode";
import { PackActionNode } from "./nodes/PackActionNode";
import { ToolCallNode } from "./nodes/ToolCallNode";
import { ApprovalNode } from "./nodes/ApprovalNode";
import { DelayNode } from "./nodes/DelayNode";
import { ConditionNode } from "./nodes/ConditionNode";
import { NotifyNode } from "./nodes/NotifyNode";
import { FanOutNode } from "./nodes/FanOutNode";
import { HttpNode } from "./nodes/HttpNode";
import { TransformNode } from "./nodes/TransformNode";
import { SwitchNode } from "./nodes/SwitchNode";
import { LoopNode } from "./nodes/LoopNode";
import { SubWorkflowNode } from "./nodes/SubWorkflowNode";
import { ErrorTriggerNode } from "./nodes/ErrorTriggerNode";

// ---------------------------------------------------------------------------
// Node type registry
// ---------------------------------------------------------------------------

const nodeTypes: NodeTypes = {
  job: AgentTaskNode,
  "agent-task": AgentTaskNode,
  "pack-action": PackActionNode,
  "tool-call": ToolCallNode,
  approval: ApprovalNode,
  delay: DelayNode,
  condition: ConditionNode,
  notify: NotifyNode,
  "fan-out": FanOutNode,
  http: HttpNode,
  transform: TransformNode,
  switch: SwitchNode,
  loop: LoopNode,
  "sub-workflow": SubWorkflowNode,
  "error-trigger": ErrorTriggerNode,
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
    const deps = step.depends_on ?? step.dependsOn ?? [];
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
        JSON.stringify(s.depends_on ?? s.dependsOn) === JSON.stringify(deps),
    );
    const sibIdx = siblings.findIndex((s) => s.id === step.id);
    if (sibIdx > 0) {
      x += sibIdx * GRID;
    }

    // Store direct step fields in node data (+ legacy config for backward compat)
    nodes.push({
      id: step.id,
      type: step.type,
      position: { x, y },
      data: {
        label: step.name || step.id,
        stepId: step.id,
        stepType: step.type,
        // Direct backend fields
        topic: step.topic,
        condition: step.condition,
        worker_id: step.worker_id,
        for_each: step.for_each,
        max_parallel: step.max_parallel,
        input: step.input,
        input_schema: step.input_schema,
        input_schema_id: step.input_schema_id,
        output_path: step.output_path,
        output_schema: step.output_schema,
        output_schema_id: step.output_schema_id,
        meta: step.meta,
        on_error: step.on_error,
        retry: step.retry,
        timeout_sec: step.timeout_sec,
        delay_sec: step.delay_sec,
        delay_until: step.delay_until,
        route_labels: step.route_labels,
        // Legacy config bag — kept for backward compat with node components
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

    // Reconstruct branched edges from config.branches (e.g. condition true/false)
    const branches = (step.config as Record<string, unknown>)?.branches as
      | Record<string, string>
      | undefined;
    if (branches) {
      for (const [handleId, targetId] of Object.entries(branches)) {
        const edgeId = `e-${step.id}-${targetId}-${handleId}`;
        // Avoid duplicating edges already created from depends_on
        if (!edges.some((e) => e.source === step.id && e.target === targetId)) {
          edges.push({
            id: edgeId,
            source: step.id,
            sourceHandle: handleId,
            target: targetId,
            type: "smoothstep",
            animated: false,
          });
        } else {
          // Patch existing edge with sourceHandle
          const existing = edges.find(
            (e) => e.source === step.id && e.target === targetId,
          );
          if (existing) {
            existing.sourceHandle = handleId;
            existing.id = edgeId;
          }
        }
      }
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
    const d = n.data ?? {};

    // Build branches map from outgoing edges that have a sourceHandle (e.g. condition true/false)
    const outgoing = edges.filter((e) => e.source === n.id && e.sourceHandle);
    const branches: Record<string, string> = {};
    for (const edge of outgoing) {
      branches[edge.sourceHandle!] = edge.target;
    }

    // Preserve legacy config with branches
    const config = { ...((d.config as Record<string, unknown>) ?? {}) };
    if (Object.keys(branches).length > 0) {
      config.branches = branches;
    }

    const step: WorkflowStep = {
      id: n.id,
      name: (d.label as string) ?? n.id,
      type: n.type ?? "job",
      depends_on: incoming.length > 0 ? incoming : undefined,
      // Direct backend fields from node data
      topic: d.topic as string | undefined,
      condition: d.condition as string | undefined,
      worker_id: d.worker_id as string | undefined,
      for_each: d.for_each as string | undefined,
      max_parallel: d.max_parallel as number | undefined,
      input: d.input as Record<string, unknown> | undefined,
      input_schema: d.input_schema as Record<string, unknown> | undefined,
      input_schema_id: d.input_schema_id as string | undefined,
      output_path: d.output_path as string | undefined,
      output_schema: d.output_schema as Record<string, unknown> | undefined,
      output_schema_id: d.output_schema_id as string | undefined,
      meta: d.meta as Record<string, unknown> | undefined,
      on_error: d.on_error as string | undefined,
      retry: d.retry as WorkflowStep["retry"],
      timeout_sec: d.timeout_sec as number | undefined,
      delay_sec: d.delay_sec as number | undefined,
      delay_until: d.delay_until as string | undefined,
      route_labels: d.route_labels as Record<string, string> | undefined,
      config: Object.keys(config).length > 0 ? config : undefined,
    };

    return step;
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
  onNodesDelete?: (nodes: Node[]) => void;
  /** Expose nodes/edges via ref for parent to read */
  graphRef?: React.MutableRefObject<{ nodes: Node[]; edges: Edge[] } | null>;
}

export function WorkflowCanvas({
  initialWorkflow,
  onNodesChange: onNodesChangeProp,
  onEdgesChange: onEdgesChangeProp,
  onNodeSelect,
  onNodesDelete: onNodesDeleteProp,
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

  const onNodesDelete = useCallback(
    (deleted: Node[]) => {
      // Filter out start nodes — they cannot be deleted
      const deletable = deleted.filter((n) => n.id !== "start" && n.type !== "start");
      if (deletable.length > 0) {
        onNodesDeleteProp?.(deletable);
      }
    },
    [onNodesDeleteProp],
  );

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
        onNodesDelete={onNodesDelete}
        nodeTypes={nodeTypes}
        deleteKeyCode={["Delete", "Backspace"]}
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
