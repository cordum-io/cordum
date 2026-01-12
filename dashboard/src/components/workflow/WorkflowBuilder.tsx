import { useCallback, useMemo, useState, useEffect } from "react";
import ReactFlow, {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  addEdge,
  useNodesState,
  useEdgesState,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
} from "reactflow";
import { Button } from "../ui/Button";
import { Input } from "../ui/Input";
import type { Workflow, Step } from "../../types/api";

type WorkflowNodeData = {
  title: string;
  type?: string;
  topic?: string;
  onDelete: (id: string) => void;
  onUpdate: (id: string, data: Partial<WorkflowNodeData>) => void;
};

const WorkflowNode = ({ id, data }: NodeProps<WorkflowNodeData>) => {
  return (
    <div className="workflow-node" data-type={(data.type || "step").toLowerCase()}>
      <Handle type="target" position={Position.Left} className="workflow-handle" />
      <div className="workflow-node__header">
        <div className="workflow-node__copy">
          <Input 
            className="text-xs font-semibold bg-transparent border-none p-0 h-auto focus:ring-0" 
            value={data.title} 
            onChange={(e) => data.onUpdate(id, { title: e.target.value })}
          />
          <div className="text-[10px] text-muted uppercase mt-1">{data.type || "worker"}</div>
        </div>
        <button 
          onClick={() => data.onDelete(id)}
          className="text-muted hover:text-danger ml-2"
        >
          ×
        </button>
      </div>
      <Handle type="source" position={Position.Right} className="workflow-handle" />
    </div>
  );
};

const nodeTypes = {
  workflowNode: WorkflowNode,
};

const defaultEdgeOptions = {
  type: "smoothstep",
  markerEnd: { type: MarkerType.ArrowClosed, color: "#9aa7b0" },
  style: { stroke: "#9aa7b0", strokeWidth: 1.4 },
};

type WorkflowBuilderProps = {
  initialWorkflow?: Partial<Workflow>;
  onChange: (workflow: Partial<Workflow>) => void;
  height?: number;
};

export function WorkflowBuilder({ initialWorkflow, onChange, height = 500 }: WorkflowBuilderProps) {
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);

  // Initialize from props
  useEffect(() => {
    if (!initialWorkflow?.steps) return;
    
    const steps = initialWorkflow.steps;
    const initialNodes: Node<WorkflowNodeData>[] = Object.entries(steps).map(([id, step], index) => ({
      id,
      type: "workflowNode",
      data: { 
        title: step.name || id, 
        type: step.type, 
        topic: step.topic,
        onDelete: (nodeId) => deleteNode(nodeId),
        onUpdate: (nodeId, newData) => updateNode(nodeId, newData)
      },
      position: { x: index * 250, y: 100 },
    }));

    const initialEdges: Edge[] = [];
    Object.entries(steps).forEach(([id, step]) => {
      step.depends_on?.forEach((dep) => {
        initialEdges.push({ id: `${dep}-${id}`, source: dep, target: id });
      });
    });

    setNodes(initialNodes);
    setEdges(initialEdges);
  }, []);

  const deleteNode = useCallback((id: string) => {
    setNodes((nds) => nds.filter((node) => node.id !== id));
    setEdges((eds) => eds.filter((edge) => edge.source !== id && edge.target !== id));
  }, [setNodes, setEdges]);

  const updateNode = useCallback((id: string, newData: Partial<WorkflowNodeData>) => {
    setNodes((nds) => 
      nds.map((node) => {
        if (node.id === id) {
          return { ...node, data: { ...node.data, ...newData } };
        }
        return node;
      })
    );
  }, [setNodes]);

  const onConnect = useCallback(
    (params: Connection) => setEdges((eds) => addEdge(params, eds)),
    [setEdges]
  );

  const addStep = () => {
    const id = `step-${nodes.length + 1}`;
    const newNode: Node<WorkflowNodeData> = {
      id,
      type: "workflowNode",
      data: { 
        title: `New Step ${nodes.length + 1}`, 
        type: "worker",
        onDelete: (nodeId) => deleteNode(nodeId),
        onUpdate: (nodeId, newData) => updateNode(nodeId, newData)
      },
      position: { x: nodes.length * 50, y: nodes.length * 50 },
    };
    setNodes((nds) => nds.concat(newNode));
  };

  const addApproval = () => {
    const id = `approval-${nodes.length + 1}`;
    const newNode: Node<WorkflowNodeData> = {
      id,
      type: "workflowNode",
      data: { 
        title: `New Approval ${nodes.length + 1}`, 
        type: "approval",
        onDelete: (nodeId) => deleteNode(nodeId),
        onUpdate: (nodeId, newData) => updateNode(nodeId, newData)
      },
      position: { x: nodes.length * 50, y: nodes.length * 50 },
    };
    setNodes((nds) => nds.concat(newNode));
  };

  // Sync changes back
  useEffect(() => {
    const steps: Record<string, Partial<Step>> = {};
    nodes.forEach((node) => {
      const stepDeps = edges
        .filter((edge) => edge.target === node.id)
        .map((edge) => edge.source);
      
      steps[node.id] = {
        id: node.id,
        name: node.data.title,
        type: node.data.type,
        topic: node.data.topic || "job.default",
        depends_on: stepDeps.length > 0 ? stepDeps : undefined,
      };
    });

    onChange({
      ...initialWorkflow,
      steps: steps as Record<string, Step>,
    });
  }, [nodes, edges]);

  return (
    <div className="space-y-4">
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={addStep}>+ Worker Step</Button>
        <Button size="sm" variant="outline" onClick={addApproval}>+ Approval Step</Button>
      </div>
      <div className="workflow-canvas border rounded-2xl overflow-hidden" style={{ height }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          nodeTypes={nodeTypes}
          defaultEdgeOptions={defaultEdgeOptions}
          fitView
        >
          <Background variant="dots" gap={22} size={1} color="#d0d7dd" />
          <Controls position="bottom-left" />
        </ReactFlow>
      </div>
    </div>
  );
}
