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
  Layers,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Status → visual style
// ---------------------------------------------------------------------------

const statusBorder: Record<string, string> = {
  pending: "border-muted",
  queued: "border-muted-foreground",
  running: "border-[var(--color-info)]/40 ring-2 ring-[var(--color-info)]/20",
  in_progress: "border-[var(--color-info)]/40 ring-2 ring-[var(--color-info)]/20",
  succeeded: "border-[var(--color-success)]/40",
  completed: "border-[var(--color-success)]/40",
  failed: "border-destructive/40",
  timed_out: "border-destructive/30",
  cancelled: "border-muted",
  blocked: "border-[var(--color-warning)]/40",
};

const statusBg: Record<string, string> = {
  pending: "bg-muted/30",
  queued: "bg-muted/30",
  running: "bg-[var(--color-info)]/5",
  in_progress: "bg-[var(--color-info)]/5",
  succeeded: "bg-[var(--color-success)]/5",
  completed: "bg-[var(--color-success)]/5",
  failed: "bg-destructive/5",
  timed_out: "bg-destructive/5",
  cancelled: "bg-muted/30",
  blocked: "bg-[var(--color-warning)]/5",
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
  parallel: Layers,
};

const typeColors: Record<string, string> = {
  job: "text-[var(--color-info)]",
  approval: "text-[var(--color-warning)]",
  delay: "text-primary",
  condition: "text-[var(--color-info)]",
  notify: "text-primary",
  "fan-out": "text-primary",
  parallel: "text-[var(--color-info)]",
};

const typeAccents: Record<string, string> = {
  job: "bg-[var(--color-info)]/5",
  approval: "bg-[var(--color-warning)]/5",
  delay: "bg-primary/5",
  condition: "bg-[var(--color-info)]/5",
  notify: "bg-primary/5",
  "fan-out": "bg-primary/5",
  parallel: "bg-[var(--color-info)]/5",
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
  const bg = statusBg[status] ?? "bg-card";

  return (
    <div
      className={`min-w-[150px] rounded-xl border-2 ${border} ${bg} px-3 py-2.5 shadow-sm transition-all ${data.isRunning ? "animate-pulse" : ""}`}
      title={buildTooltip(data)}
    >
      <div className="flex items-center gap-2">
        <div className={`flex h-7 w-7 items-center justify-center rounded-lg ${typeAccents[data.stepType] ?? "bg-muted/50"}`}>
          <Icon className={`h-4 w-4 ${typeColors[data.stepType] ?? "text-muted-foreground"}`} />
        </div>
        <div className="flex flex-col min-w-0">
          <span className="text-xs font-semibold text-ink truncate">{data.label}</span>
          <span className="text-[10px] text-muted-foreground capitalize">{status.replace(/_/g, " ")}</span>
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
  parallel: RunNode,
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

    const isRunning = step.status === "running";

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
      const depSucceeded = depStep?.status === "succeeded";
      edges.push({
        id: `e-${dep}-${step.id}`,
        source: dep,
        target: step.id,
        type: "smoothstep",
        animated: isRunning,
        style: depSucceeded ? { stroke: "#1f7a57", strokeWidth: 2 } : { strokeDasharray: "5 5" },
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
