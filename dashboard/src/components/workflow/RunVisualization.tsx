import { useCallback, useMemo } from "react";
import ReactFlow, {
  Background,
  Controls,
  MiniMap,
  type Edge,
  type Node,
  type NodeTypes,
} from "reactflow";
import "reactflow/dist/style.css";
import { useNavigate } from "react-router-dom";

import type { WorkflowRun, RunStatus } from "../../api/types";
import { Badge } from "../ui/Badge";
import {
  Briefcase,
  ShieldCheck,
  Clock,
  GitBranch,
  Bell,
  Split,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Status → visual style
// ---------------------------------------------------------------------------

const statusBorder: Record<string, string> = {
  pending: "border-gray-300",
  queued: "border-gray-400",
  running: "border-blue-400 ring-2 ring-blue-200",
  in_progress: "border-blue-400 ring-2 ring-blue-200",
  succeeded: "border-green-400",
  completed: "border-green-400",
  failed: "border-red-400",
  timed_out: "border-red-300",
  cancelled: "border-gray-300",
  blocked: "border-amber-400",
};

const statusBg: Record<string, string> = {
  pending: "bg-gray-50",
  queued: "bg-gray-50",
  running: "bg-blue-50",
  in_progress: "bg-blue-50",
  succeeded: "bg-green-50",
  completed: "bg-green-50",
  failed: "bg-red-50",
  timed_out: "bg-red-50",
  cancelled: "bg-gray-50",
  blocked: "bg-amber-50",
};

const safetyVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

// ---------------------------------------------------------------------------
// Icon map
// ---------------------------------------------------------------------------

const typeIcons: Record<string, typeof Briefcase> = {
  job: Briefcase,
  approval: ShieldCheck,
  delay: Clock,
  condition: GitBranch,
  notify: Bell,
  "fan-out": Split,
};

const typeColors: Record<string, string> = {
  job: "text-blue-600",
  approval: "text-amber-600",
  delay: "text-purple-600",
  condition: "text-teal-600",
  notify: "text-pink-600",
  "fan-out": "text-indigo-600",
};

const typeAccents: Record<string, string> = {
  job: "bg-blue-50",
  approval: "bg-amber-50",
  delay: "bg-purple-50",
  condition: "bg-teal-50",
  notify: "bg-pink-50",
  "fan-out": "bg-indigo-50",
};

// ---------------------------------------------------------------------------
// RunNode — read-only node with status overlay
// ---------------------------------------------------------------------------

interface RunNodeData {
  label: string;
  stepType: string;
  status?: RunStatus;
  safetyDecision?: string;
  jobId?: string;
  isRunning?: boolean;
  startedAt?: string;
  completedAt?: string;
}

function buildTooltip(data: RunNodeData): string {
  const lines = [`${data.stepType}: ${data.label}`, `Status: ${data.status ?? "pending"}`];
  if (data.startedAt) lines.push(`Started: ${data.startedAt}`);
  if (data.completedAt) lines.push(`Completed: ${data.completedAt}`);
  if (typeof data.safetyDecision === "string") lines.push(`Safety: ${data.safetyDecision}`);
  return lines.join("\n");
}

function RunNode({ data }: { data: RunNodeData }) {
  const status = data.status ?? "pending";
  const Icon = typeIcons[data.stepType] ?? Briefcase;
  const border = statusBorder[status] ?? "border-border";
  const bg = statusBg[status] ?? "bg-white";

  return (
    <div
      className={`min-w-[150px] rounded-xl border-2 ${border} ${bg} px-3 py-2.5 shadow-sm transition-all ${data.isRunning ? "animate-pulse" : ""}`}
      title={buildTooltip(data)}
    >
      <div className="flex items-center gap-2">
        <div className={`flex h-7 w-7 items-center justify-center rounded-lg ${typeAccents[data.stepType] ?? "bg-gray-100"}`}>
          <Icon className={`h-4 w-4 ${typeColors[data.stepType] ?? "text-gray-600"}`} />
        </div>
        <div className="flex flex-col min-w-0">
          <span className="text-xs font-semibold text-ink truncate">{data.label}</span>
          <span className="text-[10px] text-muted capitalize">{status.replace(/_/g, " ")}</span>
        </div>
      </div>

      {/* Safety decision badge for job-type nodes */}
      {data.stepType === "job" && typeof data.safetyDecision === "string" && (
        <div className="mt-1.5">
          <Badge variant={safetyVariant[data.safetyDecision] ?? "default"} className="text-[9px] px-1.5 py-0.5">
            {data.safetyDecision}
          </Badge>
        </div>
      )}

      {/* Job link indicator for job nodes */}
      {data.stepType === "job" && typeof data.jobId === "string" && (
        <div className="mt-1 text-[10px] text-accent cursor-pointer hover:underline">
          View job &rarr;
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Node types registry
// ---------------------------------------------------------------------------

const runNodeTypes: NodeTypes = {
  job: RunNode,
  approval: RunNode,
  delay: RunNode,
  condition: RunNode,
  notify: RunNode,
  "fan-out": RunNode,
};

// ---------------------------------------------------------------------------
// Convert WorkflowRun → graph
// ---------------------------------------------------------------------------

const Y_STEP = 140;

function runToGraph(run: WorkflowRun): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = [];
  const edges: Edge[] = [];
  const idxMap = new Map<string, number>();

  run.steps.forEach((step, i) => idxMap.set(step.id, i));

  run.steps.forEach((step, i) => {
    const deps = step.depends_on ?? step.dependsOn ?? [];
    let y = i * Y_STEP + 40;
    const x = 300;

    if (deps.length > 0) {
      const parentIdx = idxMap.get(deps[0]);
      if (parentIdx !== undefined) {
        y = parentIdx * Y_STEP + Y_STEP + 40;
      }
    }

    const isRunning = step.status === "running" || step.status === "in_progress";

    nodes.push({
      id: step.id,
      type: step.type,
      position: { x, y },
      data: {
        label: step.name || step.id,
        stepType: step.type,
        status: step.status,
        safetyDecision: typeof step.output?.safetyDecision === "string" ? step.output.safetyDecision : undefined,
        jobId: typeof step.output?.jobId === "string" ? step.output.jobId : undefined,
        isRunning,
        startedAt: step.startedAt,
        completedAt: step.completedAt,
      } satisfies RunNodeData,
      draggable: false,
      selectable: true,
    });

    for (const dep of deps) {
      const depStep = run.steps.find((s) => s.id === dep);
      const depSucceeded = depStep?.status === "succeeded" || depStep?.status === "completed";
      edges.push({
        id: `e-${dep}-${step.id}`,
        source: dep,
        target: step.id,
        type: "smoothstep",
        animated: isRunning,
        style: depSucceeded ? { stroke: "#22c55e", strokeWidth: 2 } : { strokeDasharray: "5 5" },
      });
    }
  });

  return { nodes, edges };
}

// ---------------------------------------------------------------------------
// RunVisualization component
// ---------------------------------------------------------------------------

export interface RunVisualizationProps {
  run: WorkflowRun;
}

export function RunVisualization({ run }: RunVisualizationProps) {
  const navigate = useNavigate();
  const { nodes, edges } = useMemo(() => runToGraph(run), [run]);

  const onNodeClick = useCallback(
    (_: React.MouseEvent, node: Node) => {
      const data = node.data as RunNodeData;
      if (data.stepType === "job" && typeof data.jobId === "string") {
        navigate(`/jobs/${data.jobId}`);
      }
    },
    [navigate],
  );

  return (
    <div className="h-full w-full">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={runNodeTypes}
        onNodeClick={onNodeClick}
        fitView
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnDrag
        zoomOnScroll
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
