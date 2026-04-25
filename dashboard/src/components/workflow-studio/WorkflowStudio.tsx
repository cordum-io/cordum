import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import type { Node, Edge } from "reactflow";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import type { Workflow } from "@/api/types";
import {
  useWorkflow,
  useRuns,
  useRun,
  useCreateWorkflow,
  useUpdateWorkflow,
  useDeleteWorkflow,
  useStartRun,
} from "@/hooks/useWorkflows";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { logger } from "@/lib/logger";

import type { UnifiedNodeData, StudioMode, StudioGraphData, CanvasHandle } from "./types";
import { definitionToGraph, graphToDefinition } from "./graphBridge";
import { StudioToolbar } from "./StudioToolbar";
import { StudioSidebar } from "./StudioSidebar";
import { StudioCanvas } from "./StudioCanvas";
import { StudioNodePanel } from "./StudioNodePanel";

// ---------------------------------------------------------------------------
// Empty graph for new workflows
// ---------------------------------------------------------------------------

const EMPTY_GRAPH: StudioGraphData = { nodes: [], edges: [] };

// ---------------------------------------------------------------------------
// WorkflowStudio
// ---------------------------------------------------------------------------

export function WorkflowStudio() {
  const { id: workflowId } = useParams<{ id: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();

  const isNew = !workflowId || workflowId === "new";

  // --- Mode ---
  const initialMode: StudioMode = isNew ? "edit" : (searchParams.get("mode") as StudioMode) || "view";
  const [mode, setMode] = useState<StudioMode>(initialMode);

  // --- Workflow data ---
  const {
    data: workflow,
    isLoading: isLoadingWorkflow,
    isError: isWorkflowError,
    error: workflowError,
    refetch: refetchWorkflow,
  } = useWorkflow(isNew ? null : workflowId);
  const { data: runs = [] } = useRuns(isNew ? null : workflowId, { limit: 20 });

  // --- Selected run ---
  const selectedRunId = searchParams.get("run");
  const { data: selectedRun } = useRun(selectedRunId);

  // --- Mutations ---
  const createWorkflow = useCreateWorkflow();
  const updateWorkflow = useUpdateWorkflow();
  const deleteWorkflow = useDeleteWorkflow();
  const startRun = useStartRun();

  // --- Local state ---
  const [name, setName] = useState("");
  const [selectedNode, setSelectedNode] = useState<Node<UnifiedNodeData> | null>(null);
  const graphRef = useRef<{ nodes: Node<UnifiedNodeData>[]; edges: Edge[] } | null>(null);
  const canvasHandle = useRef<CanvasHandle | null>(null);
  const isDirty = useRef(false);

  const handleGraphUpdate = useCallback((handle: CanvasHandle) => {
    canvasHandle.current = handle;
  }, []);

  // Sync name from loaded workflow
  useEffect(() => {
    if (workflow?.name && !isNew) {
      setName(workflow.name);
    }
  }, [workflow?.name, isNew]);

  // Log workflow load errors
  useEffect(() => {
    if (isWorkflowError && workflowError) {
      logger.error("workflow-studio", "Failed to load workflow", {
        workflowId,
        error: workflowError instanceof Error ? workflowError.message : String(workflowError),
      });
    }
  }, [isWorkflowError, workflowError, workflowId]);

  // Warn before leaving with unsaved changes
  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (isDirty.current && mode === "edit") {
        e.preventDefault();
      }
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [mode]);

  // --- Graph computation ---
  const graph = useMemo<StudioGraphData>(() => {
    if (isNew) return EMPTY_GRAPH;
    if (!workflow) return EMPTY_GRAPH;
    return definitionToGraph(workflow, mode, selectedRun);
  }, [workflow, mode, selectedRun, isNew]);

  // --- Mode change handler ---
  const handleModeChange = useCallback(
    (newMode: StudioMode) => {
      if (isDirty.current && mode === "edit" && newMode === "view") {
        if (!window.confirm("You have unsaved changes. Switch to view mode anyway?")) return;
      }
      isDirty.current = false;
      setMode(newMode);
      setSelectedNode(null);
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (newMode === "view") {
          next.delete("mode");
        } else {
          next.set("mode", newMode);
        }
        return next;
      });
    },
    [setSearchParams],
  );

  // --- Run selection handler ---
  const handleSelectRun = useCallback(
    (runId: string | null) => {
      setSelectedNode(null);
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (runId) {
          next.set("run", runId);
        } else {
          next.delete("run");
        }
        return next;
      });
    },
    [setSearchParams],
  );

  // --- Node selection handler ---
  const handleNodeSelect = useCallback(
    (node: Node<UnifiedNodeData> | null) => {
      setSelectedNode(node);
    },
    [],
  );

  // --- Build workflow payload from current graph state ---
  // Uses canvasHandle.getGraph() for guaranteed-fresh state (reads React hook
  // state via refs, not the useLayoutEffect-synced graphRef). Falls back to
  // graphRef.current if canvasHandle isn't mounted yet.
  const buildPayload = useCallback((): Partial<Workflow> => {
    const currentGraph = canvasHandle.current?.getGraph() ?? graphRef.current;
    if (!currentGraph) return { name, steps: [] };

    const definition = graphToDefinition(currentGraph.nodes, currentGraph.edges, {
      name,
      description: workflow?.description,
      timeout_sec: workflow?.timeout_sec,
      metadata: workflow?.metadata,
      input_schema: workflow?.input_schema,
      config: workflow?.config,
    });

    return definition;
  }, [name, workflow]);

  // --- Save handler ---
  const handleSave = useCallback(() => {
    const payload = buildPayload();
    if (!payload.name?.trim()) {
      toast.error("Workflow name is required");
      return;
    }

    isDirty.current = false;
    if (isNew) {
      createWorkflow.mutate(payload as Partial<Workflow> & { id?: string }, {
        onSuccess: (data) => {
          if (data?.id) {
            navigate(`/workflows/${data.id}/studio?mode=edit`, { replace: true });
          }
        },
      });
    } else if (workflowId) {
      updateWorkflow.mutate({ ...payload, id: workflowId } as Partial<Workflow> & { id: string });
    }
  }, [buildPayload, isNew, workflowId, createWorkflow, updateWorkflow, navigate]);

  // --- Deploy handler (save + switch to view) ---
  const handleDeploy = useCallback(() => {
    const payload = buildPayload();
    if (!payload.name?.trim()) {
      toast.error("Workflow name is required");
      return;
    }

    const onSuccess = (data?: { id?: string }) => {
      const id = data?.id ?? workflowId;
      if (id) {
        navigate(`/workflows/${id}/studio`, { replace: true });
        setMode("view");
      }
    };

    if (isNew) {
      createWorkflow.mutate(payload as Partial<Workflow> & { id?: string }, { onSuccess });
    } else if (workflowId) {
      updateWorkflow.mutate({ ...payload, id: workflowId } as Partial<Workflow> & { id: string }, { onSuccess });
    }
  }, [buildPayload, isNew, workflowId, createWorkflow, updateWorkflow, navigate]);

  // --- Run handler ---
  const handleRun = useCallback(() => {
    if (!workflowId || isNew) return;
    startRun.mutate(
      { workflowId },
      {
        onSuccess: (data) => {
          if (data?.run_id) {
            handleSelectRun(data.run_id);
          }
        },
      },
    );
  }, [workflowId, isNew, startRun, handleSelectRun]);

  // --- Delete handler ---
  const handleDelete = useCallback(() => {
    if (!workflowId || isNew) return;
    if (!window.confirm("Delete this workflow? This cannot be undone.")) return;
    deleteWorkflow.mutate(workflowId, {
      onSuccess: () => navigate("/workflows"),
    });
  }, [workflowId, isNew, deleteWorkflow, navigate]);

  // --- Node config save handler (edit mode) ---
  const handleNodeConfigSave = useCallback(
    (nodeId: string, data: { label: string; config: Record<string, unknown> }) => {
      if (!canvasHandle.current) return;

      canvasHandle.current.setNodes((prev) =>
        prev.map((n) => {
          if (n.id !== nodeId) return n;
          return {
            ...n,
            data: {
              ...n.data,
              label: data.label,
              topic: data.config.topic as string | undefined ?? n.data.topic,
              condition: data.config.condition as string | undefined ?? n.data.condition,
              worker_id: data.config.worker_id as string | undefined ?? n.data.worker_id,
              for_each: data.config.for_each as string | undefined ?? n.data.for_each,
              max_parallel: data.config.max_parallel as number | undefined ?? n.data.max_parallel,
              input: data.config.input as Record<string, unknown> | undefined ?? n.data.input,
              timeout_sec: data.config.timeout_sec as number | undefined ?? n.data.timeout_sec,
              delay_sec: data.config.delay_sec as number | undefined ?? n.data.delay_sec,
              delay_until: data.config.delay_until as string | undefined ?? n.data.delay_until,
              on_error: data.config.on_error as string | undefined ?? n.data.on_error,
              config: data.config,
            },
          };
        }),
      );
      isDirty.current = true;
      toast.success("Node updated");
    },
    [],
  );

  // --- Node delete handler (edit mode) ---
  const handleNodeDelete = useCallback(
    (nodeId: string) => {
      if (!canvasHandle.current) return;
      canvasHandle.current.setNodes((prev) => prev.filter((n) => n.id !== nodeId));
      canvasHandle.current.setEdges((prev) => prev.filter((e) => e.source !== nodeId && e.target !== nodeId));
      isDirty.current = true;
      setSelectedNode(null);
      toast.success("Node removed");
    },
    [],
  );

  // --- Close node panel ---
  const handleClosePanel = useCallback(() => {
    setSelectedNode(null);
  }, []);

  // --- Loading state ---
  if (!isNew && isLoadingWorkflow) {
    return (
      <div className="flex h-full w-full items-center justify-center bg-surface-0">
        <Loader2 className="h-8 w-8 animate-spin text-accent" />
      </div>
    );
  }

  // --- Error state ---
  if (!isNew && isWorkflowError) {
    return (
      <div className="flex h-full w-full items-center justify-center bg-surface-0">
        <ErrorBanner
          title="Failed to load workflow"
          message={workflowError instanceof Error ? workflowError.message : "An unexpected error occurred"}
          onRetry={() => void refetchWorkflow()}
        />
      </div>
    );
  }

  const isSaving = createWorkflow.isPending || updateWorkflow.isPending;
  const isRunning = startRun.isPending;

  return (
    <div className="flex h-full w-full flex-col bg-surface-0 overflow-hidden">
      {/* Toolbar */}
      <StudioToolbar
        mode={mode}
        workflow={workflow ?? null}
        run={selectedRun ?? null}
        name={name}
        onNameChange={setName}
        onModeChange={handleModeChange}
        onSave={handleSave}
        onDeploy={handleDeploy}
        onRun={handleRun}
        onDelete={handleDelete}
        isSaving={isSaving}
        isRunning={isRunning}
      />

      {/* Main area: sidebar + canvas + node panel */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left sidebar */}
        <StudioSidebar
          mode={mode}
          workflow={workflow ?? null}
          runs={runs}
          selectedRunId={selectedRunId}
          onSelectRun={handleSelectRun}
        />

        {/* Canvas */}
        <StudioCanvas
          key={`${workflowId}-${mode}-${selectedRunId}`}
          initialGraph={graphRef.current ?? graph}
          mode={mode}
          onNodeSelect={handleNodeSelect}
          graphRef={graphRef}
          onGraphUpdate={handleGraphUpdate}
          className="flex-1"
        />

        {/* Right panel */}
        <StudioNodePanel
          mode={mode}
          selectedNode={selectedNode}
          workflow={workflow ?? null}
          run={selectedRun ?? null}
          allNodes={graphRef.current?.nodes ?? graph.nodes}
          onClose={handleClosePanel}
          onNodeConfigSave={handleNodeConfigSave}
          onNodeDelete={handleNodeDelete}
        />
      </div>
    </div>
  );
}
