import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
} from "react";
import ReactFlow, {
  addEdge,
  Background,
  Controls,
  MiniMap,
  Panel,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeTypes,
} from "reactflow";
import "reactflow/dist/style.css";
import { Info, X, Maximize2, Minimize2 } from "lucide-react";

import { cn } from "@/lib/utils";
import { logger } from "@/lib/logger";
import { toast } from "sonner";
import type { UnifiedNodeData, StudioMode, StudioGraphData, CanvasHandle } from "./types";
import { UnifiedNode } from "./nodes/UnifiedNode";

// ---------------------------------------------------------------------------
// Node type registry (stable reference outside component)
// ---------------------------------------------------------------------------

const nodeTypes: NodeTypes = {
  unified: UnifiedNode,
};

// ---------------------------------------------------------------------------
// Dependency highlighting helpers
// ---------------------------------------------------------------------------

function collectRelated(
  nodeId: string,
  edges: Edge[],
  direction: "ancestors" | "descendants",
): Set<string> {
  const result = new Set<string>();
  const queue = [nodeId];
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const edge of edges) {
      const match = direction === "ancestors"
        ? edge.target === current && !result.has(edge.source)
        : edge.source === current && !result.has(edge.target);
      if (match) {
        const next = direction === "ancestors" ? edge.source : edge.target;
        result.add(next);
        queue.push(next);
      }
    }
  }
  return result;
}

function applyHighlighting(
  nodes: Node<UnifiedNodeData>[],
  edges: Edge[],
  highlightedId: string | null,
): { nodes: Node<UnifiedNodeData>[]; edges: Edge[] } {
  if (!highlightedId) return { nodes, edges };

  const ancestors = collectRelated(highlightedId, edges, "ancestors");
  const descendants = collectRelated(highlightedId, edges, "descendants");
  const related = new Set([highlightedId, ...ancestors, ...descendants]);

  const styledNodes = nodes.map((node) => ({
    ...node,
    style: {
      ...node.style,
      opacity: related.has(node.id) ? 1 : 0.25,
      transition: "opacity 0.2s ease",
    },
  }));

  const styledEdges = edges.map((edge) => {
    const isAncestorEdge = ancestors.has(edge.source)
      && (ancestors.has(edge.target) || edge.target === highlightedId);
    const isDescendantEdge = descendants.has(edge.target)
      && (descendants.has(edge.source) || edge.source === highlightedId);

    if (isAncestorEdge) {
      return { ...edge, style: { ...(edge.style ?? {}), strokeWidth: 2.5, stroke: "var(--color-info)" }, animated: true };
    }
    if (isDescendantEdge) {
      return { ...edge, style: { ...(edge.style ?? {}), strokeWidth: 2.5, stroke: "var(--color-warning)" }, animated: true };
    }
    return { ...edge, style: { ...(edge.style ?? {}), opacity: 0.15 } };
  });

  return { nodes: styledNodes, edges: styledEdges };
}

// ---------------------------------------------------------------------------
// Legend
// ---------------------------------------------------------------------------

function DAGLegend({ onClose }: { onClose: () => void }) {
  return (
    <div className="w-56 space-y-2.5 rounded-2xl border border-border bg-[color:var(--surface-glass)] p-3 text-xs shadow-soft backdrop-blur-md">
      <div className="flex items-center justify-between">
        <span className="font-semibold text-ink">Legend</span>
        <button type="button" onClick={onClose} className="p-0.5 text-muted-foreground hover:text-ink">
          <X className="h-3 w-3" />
        </button>
      </div>
      <div className="space-y-1.5">
        <span className="block text-xs font-semibold uppercase tracking-wide text-muted-foreground">Status</span>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-[var(--color-success)]" />Succeeded</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-destructive" />Failed</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-[var(--color-info)] animate-pulse" />Running</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-muted" />Pending</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-[var(--color-warning)]" />Waiting</div>
      </div>
      <div className="space-y-1.5">
        <span className="block text-xs font-semibold uppercase tracking-wide text-muted-foreground">Edges</span>
        <div className="flex items-center gap-2"><span className="h-0.5 w-5 bg-[var(--accent)]" />Critical path</div>
        <div className="flex items-center gap-2"><span className="h-0.5 w-5 bg-[var(--color-success)]" />Completed</div>
        <div className="flex items-center gap-2"><span className="h-0.5 w-5 bg-destructive" />To failed</div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Canvas props
// ---------------------------------------------------------------------------

export interface StudioCanvasProps {
  initialGraph: StudioGraphData;
  mode: StudioMode;
  onNodeSelect?: (node: Node<UnifiedNodeData> | null) => void;
  /** Expose live graph for parent to read (save operations) */
  graphRef?: React.MutableRefObject<{ nodes: Node<UnifiedNodeData>[]; edges: Edge[] } | null>;
  /** Expose imperative setNodes/setEdges so parent can push updates without re-mounting */
  onGraphUpdate?: (handle: CanvasHandle) => void;
  className?: string;
}

// ---------------------------------------------------------------------------
// Node ID generator
// ---------------------------------------------------------------------------

function nextNodeId(): string {
  return `step-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

// ---------------------------------------------------------------------------
// Inner canvas (must be inside ReactFlowProvider)
// ---------------------------------------------------------------------------

function StudioCanvasInner({
  initialGraph,
  mode,
  onNodeSelect,
  graphRef,
  onGraphUpdate,
  className,
}: StudioCanvasProps) {
  const [nodes, setNodes, handleNodesChange] = useNodesState(initialGraph.nodes);
  const [edges, setEdges, handleEdgesChange] = useEdgesState(initialGraph.edges);
  const reactFlowInstance = useReactFlow();
  const wrapperRef = useRef<HTMLDivElement>(null);

  // Dependency highlighting (view mode)
  const [highlightedNode, setHighlightedNode] = useState<string | null>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [showLegend, setShowLegend] = useState(false);

  const isEdit = mode === "edit";

  // Keep parent ref in sync synchronously after state changes.
  // useLayoutEffect fires before paint, eliminating the stale-ref window
  // where a save callback could read an outdated graphRef.current.
  useLayoutEffect(() => {
    if (graphRef) {
      graphRef.current = { nodes, edges };
    }
  }, [graphRef, nodes, edges]);

  // Stable refs for getGraph — always point to latest state without
  // causing the onGraphUpdate effect to re-fire on every node/edge change.
  const nodesRef = useRef(nodes);
  const edgesRef = useRef(edges);
  nodesRef.current = nodes;
  edgesRef.current = edges;

  // Expose imperative handle to parent on mount.
  // getGraph reads from refs — always current, no stale-ref risk.
  useEffect(() => {
    onGraphUpdate?.({
      setNodes,
      setEdges,
      getGraph: () => ({ nodes: nodesRef.current, edges: edgesRef.current }),
    });
  }, [onGraphUpdate, setNodes, setEdges]);

  // Apply highlighting in view mode
  const { nodes: displayNodes, edges: displayEdges } = useMemo(
    () => isEdit ? { nodes, edges } : applyHighlighting(nodes, edges, highlightedNode),
    [nodes, edges, highlightedNode, isEdit],
  );

  // --- Handlers ---

  const handleNodeClick = useCallback(
    (_: React.MouseEvent, node: Node<UnifiedNodeData>) => {
      if (!isEdit) {
        setHighlightedNode((prev) => (prev === node.id ? null : node.id));
      }
      onNodeSelect?.(node);
    },
    [isEdit, onNodeSelect],
  );

  const handlePaneClick = useCallback(() => {
    setHighlightedNode(null);
    onNodeSelect?.(null);
  }, [onNodeSelect]);

  const isValidConnection = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) return false;
      if (connection.source === connection.target) return false;
      // Duplicate edge check (same source → target)
      if (edges.some((e) => e.source === connection.source && e.target === connection.target)) {
        return false;
      }
      // Cycle detection via BFS from target following outgoing edges
      const visited = new Set<string>();
      const queue = [connection.target];
      while (queue.length > 0) {
        const current = queue.shift()!;
        if (current === connection.source) return false;
        if (visited.has(current)) continue;
        visited.add(current);
        for (const edge of edges) {
          if (edge.source === current) queue.push(edge.target);
        }
      }
      return true;
    },
    [edges],
  );

  const handleConnect = useCallback(
    (connection: Connection) => {
      if (!isEdit) return;
      if (!isValidConnection(connection)) {
        logger.warn("workflow-studio", "Rejected invalid connection", {
          source: connection.source,
          target: connection.target,
        });
        toast.error("Invalid connection", {
          description: connection.source === connection.target
            ? "A node cannot connect to itself"
            : "This connection would create a cycle or already exists",
        });
        return;
      }
      setEdges((eds) => addEdge({ ...connection, type: "smoothstep" }, eds));
    },
    [isEdit, isValidConnection, setEdges],
  );

  const handleNodesDelete = useCallback(
    (deleted: Node[]) => {
      if (!isEdit) return;
      const deletable = deleted.filter((n) => n.id !== "start" && n.type !== "start");
      if (deletable.length === 0) return;
      const deletedIds = new Set(deletable.map((n) => n.id));
      setEdges((eds) => eds.filter((e) => !deletedIds.has(e.source) && !deletedIds.has(e.target)));
    },
    [isEdit, setEdges],
  );

  // --- Drag-and-drop from sidebar palette (edit mode) ---
  // Handlers are on the wrapper div, NOT on ReactFlow props, for reliable event capture.

  const handleDragOver = useCallback((event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  }, []);

  const handleDrop = useCallback(
    (event: DragEvent<HTMLDivElement>) => {
      event.preventDefault();
      if (!isEdit) return;

      const stepType = event.dataTransfer.getData("application/workflow-studio");
      if (!stepType) return;

      // Convert screen coordinates to flow coordinates
      let position = { x: 250, y: 100 };
      try {
        position = reactFlowInstance.screenToFlowPosition({
          x: event.clientX,
          y: event.clientY,
        });
      } catch {
        // Fallback: place relative to wrapper bounds
        const bounds = wrapperRef.current?.getBoundingClientRect();
        if (bounds) {
          position = {
            x: event.clientX - bounds.left,
            y: event.clientY - bounds.top,
          };
        }
      }

      const id = nextNodeId();
      const newNode: Node<UnifiedNodeData> = {
        id,
        type: "unified",
        position,
        data: {
          label: stepType.replace(/-/g, " "),
          stepId: id,
          stepType,
          mode,
          config: {},
        },
        draggable: true,
        connectable: true,
      };

      setNodes((nds) => [...nds, newNode]);
    },
    [isEdit, mode, setNodes, reactFlowInstance],
  );

  // --- Render ---

  const canvasContent = (
    <div
      ref={wrapperRef}
      className={cn("h-full w-full", !fullscreen && className)}
      onDragOver={isEdit ? handleDragOver : undefined}
      onDrop={isEdit ? handleDrop : undefined}
    >
      <ReactFlow
        nodes={displayNodes}
        edges={displayEdges}
        onNodesChange={isEdit ? handleNodesChange : undefined}
        onEdgesChange={isEdit ? handleEdgesChange : undefined}
        onConnect={handleConnect}
        isValidConnection={isValidConnection}
        onNodeClick={handleNodeClick}
        onPaneClick={handlePaneClick}
        onNodesDelete={handleNodesDelete}
        nodeTypes={nodeTypes}
        nodesDraggable={isEdit}
        nodesConnectable={isEdit}
        elementsSelectable
        deleteKeyCode={isEdit ? ["Delete", "Backspace"] : []}
        defaultEdgeOptions={{
          type: "smoothstep",
          animated: false,
          style: { stroke: "var(--border)", strokeWidth: 1.5 },
        }}
        fitView
        snapToGrid={isEdit}
        snapGrid={[20, 20]}
      >
        <Background gap={20} size={1} color="var(--muted-foreground)" style={{ opacity: 0.15 }} />
        {/* Atmospheric vignette overlay */}
        <div
          className="pointer-events-none absolute inset-0 z-[1]"
          style={{
            background: "radial-gradient(ellipse at center, transparent 50%, var(--surface-0) 100%)",
            opacity: 0.4,
          }}
        />
        <Controls showInteractive={isEdit} className="!rounded-2xl !border-border !bg-surface-1 !shadow-soft [&>button]:!border-border [&>button]:!bg-surface-1 [&>button:hover]:!bg-surface-2 [&>button]:!rounded-xl" />
        <MiniMap
          nodeStrokeWidth={3}
          className="!bg-surface-1/90 !backdrop-blur-sm !border-border !rounded-2xl !shadow-soft"
        />

        {/* Empty state for new workflows */}
        {isEdit && nodes.length === 0 && (
          <Panel position="top-center" className="mt-32">
            <div className="text-center text-muted-foreground">
              <p className="text-sm font-display font-semibold">No steps yet</p>
              <p className="text-xs mt-1">Drag steps from the sidebar to build your workflow</p>
            </div>
          </Panel>
        )}

        {/* Top-right panel buttons */}
        <Panel position="top-right" className="flex gap-1">
          {!isEdit && (
            <button
              type="button"
              onClick={() => setShowLegend((v) => !v)}
              className="rounded-full border border-border bg-surface-1 p-1.5 text-muted-foreground transition-colors hover:bg-surface-2 hover:text-ink"
              title="Legend"
            >
              <Info className="h-4 w-4" />
            </button>
          )}
          <button
            type="button"
            onClick={() => setFullscreen((v) => !v)}
            className="rounded-full border border-border bg-surface-1 p-1.5 text-muted-foreground transition-colors hover:bg-surface-2 hover:text-ink"
            title={fullscreen ? "Exit fullscreen" : "Fullscreen"}
          >
            {fullscreen ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
          </button>
        </Panel>

        {/* Legend (view mode) */}
        {showLegend && !isEdit && (
          <Panel position="bottom-left">
            <DAGLegend onClose={() => setShowLegend(false)} />
          </Panel>
        )}
      </ReactFlow>
    </div>
  );

  if (fullscreen) {
    return (
      <div className="fixed inset-0 z-50 bg-surface-1">
        {canvasContent}
      </div>
    );
  }

  return canvasContent;
}

// ---------------------------------------------------------------------------
// StudioCanvas (public — wraps inner with ReactFlowProvider)
// ---------------------------------------------------------------------------

export function StudioCanvas(props: StudioCanvasProps) {
  return (
    <ReactFlowProvider>
      <StudioCanvasInner {...props} />
    </ReactFlowProvider>
  );
}

