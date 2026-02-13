import { useCallback, useMemo, useState } from "react";
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  Panel,
  type Node,
  type Edge,
  type NodeTypes,
} from "reactflow";
import "reactflow/dist/style.css";
import { Maximize2, Minimize2, Info, X } from "lucide-react";

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
// Dependency highlighting helpers
// ---------------------------------------------------------------------------

function getAncestors(nodeId: string, edges: Edge[]): Set<string> {
  const ancestors = new Set<string>();
  const queue = [nodeId];
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const edge of edges) {
      if (edge.target === current && !ancestors.has(edge.source)) {
        ancestors.add(edge.source);
        queue.push(edge.source);
      }
    }
  }
  return ancestors;
}

function getDescendants(nodeId: string, edges: Edge[]): Set<string> {
  const descendants = new Set<string>();
  const queue = [nodeId];
  while (queue.length > 0) {
    const current = queue.shift()!;
    for (const edge of edges) {
      if (edge.source === current && !descendants.has(edge.target)) {
        descendants.add(edge.target);
        queue.push(edge.target);
      }
    }
  }
  return descendants;
}

// ---------------------------------------------------------------------------
// Legend
// ---------------------------------------------------------------------------

function DAGLegend({ onClose }: { onClose: () => void }) {
  return (
    <div className="rounded-xl border border-border bg-surface1/95 p-3 shadow-lg backdrop-blur-sm text-xs space-y-2.5 w-56">
      <div className="flex items-center justify-between">
        <span className="font-semibold text-ink">Legend</span>
        <button onClick={onClose} className="p-0.5 text-muted hover:text-ink">
          <X className="h-3 w-3" />
        </button>
      </div>
      <div className="space-y-1.5">
        <span className="block text-[10px] font-semibold uppercase tracking-wide text-muted">Status</span>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-green-500" />Succeeded</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-red-500" />Failed</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-blue-500 animate-pulse" />Running</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-gray-300" />Pending</div>
        <div className="flex items-center gap-2"><span className="h-2.5 w-2.5 rounded-full bg-amber-500" />Waiting</div>
      </div>
      <div className="space-y-1.5">
        <span className="block text-[10px] font-semibold uppercase tracking-wide text-muted">Edges</span>
        <div className="flex items-center gap-2"><span className="h-0.5 w-5 bg-[var(--accent)]" />Critical path</div>
        <div className="flex items-center gap-2"><span className="h-0.5 w-5 bg-green-500" />Completed</div>
        <div className="flex items-center gap-2"><span className="h-0.5 w-5 bg-red-500" />To failed</div>
      </div>
    </div>
  );
}

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
  const { nodes: baseNodes, edges: baseEdges } = useMemo(
    () => buildRunGraph(workflow, run),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [workflow.steps, run?.id, run?.updatedAt],
  );

  const [highlightedNode, setHighlightedNode] = useState<string | null>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const [showLegend, setShowLegend] = useState(false);

  // Compute highlighted sets
  const { ancestors, descendants } = useMemo(() => {
    if (!highlightedNode) return { ancestors: new Set<string>(), descendants: new Set<string>() };
    return {
      ancestors: getAncestors(highlightedNode, baseEdges),
      descendants: getDescendants(highlightedNode, baseEdges),
    };
  }, [highlightedNode, baseEdges]);

  const isHighlighting = highlightedNode !== null;
  const relatedNodes = useMemo(() => {
    if (!isHighlighting) return new Set<string>();
    const set = new Set<string>([highlightedNode!, ...ancestors, ...descendants]);
    return set;
  }, [isHighlighting, highlightedNode, ancestors, descendants]);

  // Apply highlighting styles
  const nodes: Node[] = useMemo(() => {
    if (!isHighlighting) return baseNodes;
    return baseNodes.map((node) => ({
      ...node,
      style: {
        ...node.style,
        opacity: relatedNodes.has(node.id) ? 1 : 0.3,
        transition: "opacity 0.2s",
      },
    }));
  }, [baseNodes, isHighlighting, relatedNodes]);

  const edges: Edge[] = useMemo(() => {
    if (!isHighlighting) return baseEdges;
    return baseEdges.map((edge) => {
      const isAncestorEdge = ancestors.has(edge.source) && (ancestors.has(edge.target) || edge.target === highlightedNode);
      const isDescendantEdge = descendants.has(edge.target) && (descendants.has(edge.source) || edge.source === highlightedNode);

      if (isAncestorEdge) {
        return {
          ...edge,
          style: { ...(edge.style ?? {}), strokeWidth: 2.5, stroke: "var(--info, #3b82f6)" },
          animated: true,
        };
      }
      if (isDescendantEdge) {
        return {
          ...edge,
          style: { ...(edge.style ?? {}), strokeWidth: 2.5, stroke: "var(--warning, #f59e0b)" },
          animated: true,
        };
      }
      return {
        ...edge,
        style: { ...(edge.style ?? {}), opacity: 0.2 },
      };
    });
  }, [baseEdges, isHighlighting, ancestors, descendants, highlightedNode]);

  const handleNodeClick = useCallback(
    (_: React.MouseEvent, node: Node) => {
      setHighlightedNode((prev) => (prev === node.id ? null : node.id));
      onNodeClick?.(node.id);
    },
    [onNodeClick],
  );

  const handlePaneClick = useCallback(() => {
    setHighlightedNode(null);
  }, []);

  const dagContent = (
    <div className={cn("h-full w-full", !fullscreen && className)}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodeClick={handleNodeClick}
        onPaneClick={handlePaneClick}
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
        <Panel position="top-right" className="flex gap-1">
          <button
            onClick={() => setShowLegend((v) => !v)}
            className="rounded-lg border border-border bg-surface1 p-1.5 text-muted hover:text-ink hover:bg-surface2 transition-colors"
            title="Legend"
          >
            <Info className="h-4 w-4" />
          </button>
          <button
            onClick={() => setFullscreen((v) => !v)}
            className="rounded-lg border border-border bg-surface1 p-1.5 text-muted hover:text-ink hover:bg-surface2 transition-colors"
            title={fullscreen ? "Exit fullscreen" : "Fullscreen"}
          >
            {fullscreen ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
          </button>
        </Panel>
        {showLegend && (
          <Panel position="bottom-left">
            <DAGLegend onClose={() => setShowLegend(false)} />
          </Panel>
        )}
      </ReactFlow>
    </div>
  );

  if (fullscreen) {
    return (
      <div className="fixed inset-0 z-50 bg-surface1">
        {dagContent}
      </div>
    );
  }

  return dagContent;
}
