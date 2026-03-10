import { useCallback, useRef, useState } from "react";
import type { Node } from "reactflow";
import { useNavigate, useParams } from "react-router-dom";
import { Save } from "lucide-react";

import { Button } from "../ui/Button";
import { BuilderSidebar } from "./BuilderSidebar";
import { WorkflowCanvas, graphToDefinition } from "./WorkflowCanvas";
import { NodeConfigPanel } from "./NodeConfigPanel";
import {
  useWorkflow,
  useCreateWorkflow,
  useUpdateWorkflow,
} from "../../hooks/useWorkflows";
import { logger } from "../../lib/logger";
import { isValidResourceId } from "../../lib/utils";

// ---------------------------------------------------------------------------
// WorkflowBuilder — orchestrator
// ---------------------------------------------------------------------------

export function WorkflowBuilder() {
  const { id: rawWorkflowId } = useParams<{ id: string }>();
  const workflowId = isValidResourceId(rawWorkflowId) ? rawWorkflowId : rawWorkflowId === "new" ? "new" : undefined;
  const isEdit = !!workflowId && workflowId !== "new";
  const navigate = useNavigate();

  // Load existing workflow if editing
  const { data: existing, isLoading } = useWorkflow(isEdit ? workflowId : null);

  // Workflow metadata
  const [name, setName] = useState(existing?.name ?? "");
  const [description, setDescription] = useState(
    existing?.description ?? (existing?.metadata?.description as string) ?? "",
  );

  // Sync metadata from loaded workflow (only once)
  const loadedRef = useRef(false);
  if (existing && !loadedRef.current) {
    loadedRef.current = true;
    if (!name) setName(existing.name);
    if (!description) {
      const desc = existing.description ?? (typeof existing.metadata?.description === "string" ? existing.metadata.description : "");
      if (desc) setDescription(desc);
    }
  }

  // Selected node for config panel
  const [selectedNode, setSelectedNode] = useState<Node | null>(null);

  // Graph ref to read current nodes/edges
  const graphRef = useRef<{ nodes: Node[]; edges: import("reactflow").Edge[] } | null>(null);

  // Mutations
  const createWorkflow = useCreateWorkflow();
  const updateWorkflow = useUpdateWorkflow();
  const saving = createWorkflow.isPending || updateWorkflow.isPending;

  const handleSave = useCallback(() => {
    if (!graphRef.current) return;
    const { nodes, edges } = graphRef.current;
    const definition = graphToDefinition(nodes, edges, {
      name,
      description,
      metadata: { description },
      timeout_sec: existing?.timeout_sec ?? existing?.timeout ?? 3600,
      timeout: existing?.timeout ?? 3600,
    });

    logger.info("workflow-builder", "Saving workflow", {
      isEdit,
      workflowId,
      nodeCount: nodes.length,
      edgeCount: edges.length,
    });

    if (isEdit && workflowId) {
      updateWorkflow.mutate(
        { ...definition, id: workflowId } as Parameters<typeof updateWorkflow.mutate>[0],
        { onSuccess: () => navigate(`/workflows/${workflowId}`) },
      );
    } else {
      createWorkflow.mutate(definition, {
        onSuccess: () => navigate("/workflows"),
      });
    }
  }, [name, description, existing, isEdit, workflowId, createWorkflow, updateWorkflow, navigate]);

  // Handle node config save — update node data in graph
  const handleNodeConfigSave = useCallback(
    (nodeId: string, data: { label: string; config: Record<string, unknown> }) => {
      if (!graphRef.current) return;
      logger.debug("workflow-builder", "Node config saved", { nodeId, label: data.label });
      // Mutate the node data in-place (ReactFlow state is ref-synced)
      const node = graphRef.current.nodes.find((n) => n.id === nodeId);
      if (node) {
        node.data = { ...node.data, ...data };
      }
      setSelectedNode(null);
    },
    [],
  );

  // Handle node deletion — remove node + connected edges from graph
  const handleDeleteNode = useCallback(
    (nodeId: string) => {
      if (!graphRef.current) return;
      // Prevent deleting start node
      if (nodeId === "start") return;
      graphRef.current.nodes = graphRef.current.nodes.filter((n) => n.id !== nodeId);
      graphRef.current.edges = graphRef.current.edges.filter(
        (e) => e.source !== nodeId && e.target !== nodeId,
      );
      setSelectedNode(null);
    },
    [],
  );

  if (isEdit && isLoading) {
    return (
      <div className="flex h-[calc(100vh-4rem)] items-center justify-center">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-accent border-t-transparent" />
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100vh-4rem)] flex-col">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b border-border bg-surface1 px-4 py-2">
        <h1 className="font-display text-lg font-bold text-ink">
          {isEdit ? "Edit Workflow" : "New Workflow"}
        </h1>
        <Button onClick={handleSave} disabled={saving || !name.trim()} size="sm">
          <Save className="h-4 w-4" />
          {saving ? "Saving..." : "Save"}
        </Button>
      </div>

      {/* Main layout */}
      <div className="flex flex-1 min-h-0">
        <BuilderSidebar
          name={name}
          description={description}
          onNameChange={setName}
          onDescriptionChange={setDescription}
        />

        <div className="flex-1 min-h-0">
          <WorkflowCanvas
            initialWorkflow={existing ?? undefined}
            onNodeSelect={setSelectedNode}
            onNodesDelete={(deleted) => deleted.forEach((n) => handleDeleteNode(n.id))}
            graphRef={graphRef}
          />
        </div>

        {selectedNode && (
          <NodeConfigPanel
            node={selectedNode}
            onSave={handleNodeConfigSave}
            onClose={() => setSelectedNode(null)}
            onDelete={handleDeleteNode}
            allNodes={graphRef.current?.nodes ?? []}
          />
        )}
      </div>
    </div>
  );
}
