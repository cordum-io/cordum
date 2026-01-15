import { useCallback, useEffect, useState, useRef } from "react";
import ReactFlow, {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  MarkerType,
  addEdge,
  useNodesState,
  useEdgesState,
  type Connection,
  type Edge,
  type Node,
  type ReactFlowInstance,
  type XYPosition,
} from "reactflow";
import "reactflow/dist/style.css";

import { Button } from "../ui/Button";
import { BuilderSidebar } from "./BuilderSidebar";
import { NodeConfigPanel } from "./NodeConfigPanel";
import { builderNodeTypes } from "./nodeTypes";
import { NODE_CONFIGS, SUPPORTED_NODE_TYPES, generateStepId } from "./nodes";
import type {
  BuilderNode,
  BuilderNodeData,
  BuilderEdge,
  DragData,
  BuilderNodeType,
  WorkerNodeData,
} from "./types";
import type { Workflow, Step } from "../../types/api";

const defaultEdgeOptions = {
  type: "smoothstep",
  markerEnd: { type: MarkerType.ArrowClosed, color: "#9aa7b0" },
  style: { stroke: "#9aa7b0", strokeWidth: 1.4 },
  animated: false,
};

type WorkflowBuilderProps = {
  initialWorkflow?: Partial<Workflow>;
  onChange: (workflow: Partial<Workflow>) => void;
  height?: number;
};

export function WorkflowBuilder({
  initialWorkflow,
  onChange,
  height = 600,
}: WorkflowBuilderProps) {
  const reactFlowWrapper = useRef<HTMLDivElement>(null);
  const [reactFlowInstance, setReactFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [nodes, setNodes, onNodesChange] = useNodesState<BuilderNodeData>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [isDragging, setIsDragging] = useState(false);

  // Get selected node
  const selectedNode = selectedNodeId
    ? (nodes.find((n) => n.id === selectedNodeId) as BuilderNode | undefined)
    : null;

  // Delete node handler
  const deleteNode = useCallback(
    (id: string) => {
      setNodes((nds) => nds.filter((node) => node.id !== id));
      setEdges((eds) => eds.filter((edge) => edge.source !== id && edge.target !== id));
      if (selectedNodeId === id) {
        setSelectedNodeId(null);
      }
    },
    [setNodes, setEdges, selectedNodeId]
  );

  const handleKeyDown = useCallback(
    (event: KeyboardEvent) => {
      if (!selectedNodeId) {
        return;
      }
      const target = event.target as HTMLElement | null;
      if (target) {
        const tag = target.tagName.toLowerCase();
        if (tag === "input" || tag === "textarea" || tag === "select" || target.isContentEditable) {
          return;
        }
      }
      if (event.key === "Delete" || event.key === "Backspace") {
        event.preventDefault();
        deleteNode(selectedNodeId);
      }
    },
    [deleteNode, selectedNodeId]
  );

  // Select node handler
  const selectNode = useCallback((id: string) => {
    setSelectedNodeId(id);
    setNodes((nds) =>
      nds.map((node) => ({
        ...node,
        data: { ...node.data, selected: node.id === id },
      }))
    );
  }, [setNodes]);

  // Update node handler
  const updateNode = useCallback(
    (id: string, newData: Partial<BuilderNodeData>) => {
      setNodes((nds) =>
        nds.map((node) => {
          if (node.id === id) {
            return { ...node, data: { ...node.data, ...newData } };
          }
          return node;
        })
      );
    },
    [setNodes]
  );

  // Create node from type
  const createNode = useCallback(
    (type: BuilderNodeType, position: XYPosition, extraData?: Partial<BuilderNodeData>): BuilderNode => {
      const config = NODE_CONFIGS[type];
      const stepId = generateStepId(type);
      return {
        id: stepId,
        type,
        position,
        data: {
          ...config.defaultData,
          ...extraData,
          stepId,
          label: extraData?.label || config.defaultData.label || config.label,
          onDelete: deleteNode,
          onSelect: selectNode,
        } as BuilderNodeData,
      };
    },
    [deleteNode, selectNode]
  );

  // Initialize from workflow
  useEffect(() => {
    if (!initialWorkflow?.steps) return;

    const steps = initialWorkflow.steps;
    const stepEntries = Object.entries(steps);

    // Calculate positions in a grid layout
    const initialNodes: BuilderNode[] = stepEntries.map(([id, step], index) => {
      const row = Math.floor(index / 3);
      const col = index % 3;
      const nodeType = mapStepTypeToNodeType(step.type);

      return {
        id,
        type: nodeType,
        position: { x: col * 300 + 100, y: row * 200 + 100 },
        data: {
          nodeType,
          stepId: id,
          label: step.name || id,
          topic: step.topic,
          packId: step.meta?.pack_id,
          capability: step.meta?.capability,
          riskTags: step.meta?.risk_tags,
          requires: step.meta?.requires,
          timeoutSec: step.timeout_sec,
          condition: step.condition,
          delaySec: step.delay_sec,
          delayUntil: step.delay_until,
          forEach: step.for_each,
          maxParallel: step.max_parallel,
          onDelete: deleteNode,
          onSelect: selectNode,
        } as BuilderNodeData,
      };
    });

    // Create edges from depends_on
    const initialEdges: BuilderEdge[] = [];
    stepEntries.forEach(([id, step]) => {
      step.depends_on?.forEach((dep) => {
        initialEdges.push({
          id: `${dep}-${id}`,
          source: dep,
          target: id,
        });
      });
    });

    setNodes(initialNodes);
    setEdges(initialEdges);
  }, []);

  useEffect(() => {
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [handleKeyDown]);

  // Handle connection
  const onConnect = useCallback(
    (params: Connection) => {
      // Add edge with proper source handle for condition/loop nodes
      setEdges((eds) =>
        addEdge(
          {
            ...params,
            data: params.sourceHandle ? { condition: params.sourceHandle } : undefined,
          },
          eds
        )
      );
    },
    [setEdges]
  );

  // Handle drag over
  const onDragOver = useCallback((event: React.DragEvent) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  }, []);

  // Handle drop
  const onDrop = useCallback(
    (event: React.DragEvent) => {
      event.preventDefault();
      setIsDragging(false);

      if (!reactFlowInstance || !reactFlowWrapper.current) return;

      const dataStr = event.dataTransfer.getData("application/json");
      if (!dataStr) return;

      try {
        const dragData: DragData = JSON.parse(dataStr);
        const bounds = reactFlowWrapper.current.getBoundingClientRect();
        const position = reactFlowInstance.screenToFlowPosition({
          x: event.clientX - bounds.left,
          y: event.clientY - bounds.top,
        });

        let newNode: BuilderNode;

        if (dragData.type === "node") {
          newNode = createNode(dragData.nodeType, position);
        } else if (dragData.type === "pack") {
          // Create worker node with pack topic data
          newNode = createNode("worker", position, {
            label: dragData.topic.topicName,
            topic: dragData.topic.topicName,
            packId: dragData.topic.packId,
            capability: dragData.topic.capability,
            riskTags: dragData.topic.riskTags,
            requires: dragData.topic.requires,
          } as Partial<WorkerNodeData>);
        } else {
          return;
        }

        setNodes((nds) => nds.concat(newNode));
        selectNode(newNode.id);
      } catch {
        // Invalid drag data
      }
    },
    [reactFlowInstance, createNode, setNodes, selectNode]
  );

  // Sync changes back to workflow
  useEffect(() => {
    const steps: Record<string, Partial<Step>> = {};

    nodes.forEach((node) => {
      const data = node.data as BuilderNodeData;
      const isWorker = data.nodeType === "worker";
      const retry = (data as WorkerNodeData).retry;
      const meta = isWorker
        ? {
            pack_id: (data as WorkerNodeData).packId,
            capability: (data as WorkerNodeData).capability,
            risk_tags: (data as WorkerNodeData).riskTags,
            requires: (data as WorkerNodeData).requires,
          }
        : undefined;
      const hasMeta = meta
        ? Object.values(meta).some((value) => value !== undefined && value !== null)
        : false;
      const deps = edges
        .filter((edge) => edge.target === node.id)
        .map((edge) => edge.source);

      steps[node.id] = {
        id: node.id,
        name: data.label,
        type: mapNodeTypeToStepType(data.nodeType),
        topic: isWorker ? (data as WorkerNodeData).topic || "job.default" : undefined,
        depends_on: deps.length > 0 ? deps : undefined,
        condition: (data as { condition?: string }).condition,
        delay_sec: (data as { delaySec?: number }).delaySec,
        delay_until: (data as { delayUntil?: string }).delayUntil,
        for_each: (data as { forEach?: string }).forEach,
        max_parallel: (data as { maxParallel?: number }).maxParallel,
        timeout_sec: (data as { timeoutSec?: number }).timeoutSec,
        retry: retry,
        meta: hasMeta ? meta : undefined,
      };
    });

    onChange({
      ...initialWorkflow,
      steps: steps as Record<string, Step>,
    });
  }, [nodes, edges]);

  // Add node from toolbar
  const addNodeFromType = (type: BuilderNodeType) => {
    const position = {
      x: Math.random() * 200 + 100,
      y: Math.random() * 200 + 100,
    };
    const newNode = createNode(type, position);
    setNodes((nds) => nds.concat(newNode));
    selectNode(newNode.id);
  };

  return (
    <div className="workflow-builder">
      {/* Toolbar */}
      <div className="workflow-builder__toolbar">
        {SUPPORTED_NODE_TYPES.map((type) => (
          <Button key={type} size="sm" variant="outline" onClick={() => addNodeFromType(type)}>
            + {NODE_CONFIGS[type].label}
          </Button>
        ))}
      </div>

      {/* Main content */}
      <div className="workflow-builder__content" style={{ height }}>
        {/* Sidebar */}
        <BuilderSidebar
          onDragStart={() => setIsDragging(true)}
          onDragEnd={() => setIsDragging(false)}
        />

        {/* Canvas */}
        <div
          ref={reactFlowWrapper}
          className={`workflow-builder__canvas ${isDragging ? "workflow-builder__canvas--dragging" : ""}`}
        >
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onInit={setReactFlowInstance}
            onDrop={onDrop}
            onDragOver={onDragOver}
            nodeTypes={builderNodeTypes}
            defaultEdgeOptions={defaultEdgeOptions}
            fitView
            snapToGrid
            snapGrid={[15, 15]}
          >
            <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#d0d7dd" />
            <Controls position="bottom-left" />
            <MiniMap
              nodeColor={(node) => {
                const data = node.data as BuilderNodeData;
                switch (data.nodeType) {
                  case "worker":
                    return "#0f7f7a";
                  case "approval":
                    return "#f59e0b";
                  case "condition":
                    return "#3b82f6";
                  case "delay":
                    return "#9aa7b0";
                  case "loop":
                    return "#a855f7";
                  case "parallel":
                    return "#06b6d4";
                  case "subworkflow":
                    return "#6366f1";
                  default:
                    return "#9aa7b0";
                }
              }}
              position="bottom-right"
            />
          </ReactFlow>
        </div>

        {/* Config Panel */}
        <NodeConfigPanel
          node={selectedNode || null}
          onUpdate={updateNode}
          onClose={() => setSelectedNodeId(null)}
        />
      </div>
    </div>
  );
}

// Map step type to builder node type
function mapStepTypeToNodeType(stepType?: string): BuilderNodeType {
  switch (stepType?.toLowerCase()) {
    case "approval":
      return "approval";
    case "condition":
    case "if":
      return "condition";
    case "delay":
    case "wait":
    case "timer":
      return "delay";
    case "loop":
    case "foreach":
    case "for_each":
      return "loop";
    case "parallel":
    case "fan_out":
      return "parallel";
    case "subworkflow":
    case "workflow":
    case "call":
      return "subworkflow";
    default:
      return "worker";
  }
}

// Map builder node type to step type
function mapNodeTypeToStepType(nodeType: BuilderNodeType): string {
  switch (nodeType) {
    case "worker":
      return "worker";
    case "approval":
      return "approval";
    case "condition":
      return "condition";
    case "delay":
      return "delay";
    case "loop":
      return "loop";
    case "parallel":
      return "parallel";
    case "subworkflow":
      return "subworkflow";
    default:
      return "worker";
  }
}
