import { memo, useState } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import {
  Briefcase,
  UserCheck,
  Clock,
  GitBranch,
  Bell,
  GitFork,
  Globe,
  Code,
  GitMerge,
  Repeat,
  Workflow,
  AlertTriangle,
  CheckCircle,
  Loader2,
  XCircle,
  Slash,
  MessageSquare,
  Package,
  Wrench,
} from "lucide-react";
import { cn } from "../../../lib/utils";
import type { RunStatus } from "../../../api/types";

// ---------------------------------------------------------------------------
// Data shape injected via ReactFlow node.data
// ---------------------------------------------------------------------------

export interface RunOverlayNodeData {
  label: string;
  stepType: string;
  runStatus?: RunStatus;
  duration?: number;
  safetyDecision?: { type: string };
  error?: string;
  /** For condition steps: which branch was taken (true/false) */
  conditionResult?: boolean;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatDuration(ms: number): string {
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  const rem = secs % 60;
  return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`;
}

function truncate(str: string, max: number): string {
  return str.length > max ? str.slice(0, max) + "\u2026" : str;
}

// ---------------------------------------------------------------------------
// Step type icons
// ---------------------------------------------------------------------------

const STEP_TYPE_ICONS: Record<string, React.ReactNode> = {
  job: <Briefcase className="h-3.5 w-3.5" />,
  approval: <UserCheck className="h-3.5 w-3.5" />,
  delay: <Clock className="h-3.5 w-3.5" />,
  condition: <GitBranch className="h-3.5 w-3.5" />,
  notify: <Bell className="h-3.5 w-3.5" />,
  "fan-out": <GitFork className="h-3.5 w-3.5" />,
  http: <Globe className="h-3.5 w-3.5" />,
  transform: <Code className="h-3.5 w-3.5" />,
  switch: <GitMerge className="h-3.5 w-3.5" />,
  loop: <Repeat className="h-3.5 w-3.5" />,
  "sub-workflow": <Workflow className="h-3.5 w-3.5" />,
  "error-trigger": <AlertTriangle className="h-3.5 w-3.5" />,
  "agent-task": <MessageSquare className="h-3.5 w-3.5" />,
  "pack-action": <Package className="h-3.5 w-3.5" />,
  "tool-call": <Wrench className="h-3.5 w-3.5" />,
};

// ---------------------------------------------------------------------------
// Status visual config
// ---------------------------------------------------------------------------

interface StatusStyle {
  bg: string;
  border: string;
  statusIcon: React.ReactNode | null;
  pulse: boolean;
  dimmed: boolean;
  strikethrough: boolean;
}

function getStatusStyle(status?: RunStatus): StatusStyle {
  switch (status) {
    case "succeeded":
    case "completed":
      return {
        bg: "bg-green-50",
        border: "border-green-400",
        statusIcon: <CheckCircle className="h-3.5 w-3.5 text-green-600" />,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
    case "running":
    case "in_progress":
      return {
        bg: "bg-blue-50",
        border: "border-blue-400",
        statusIcon: <Loader2 className="h-3.5 w-3.5 text-blue-600 animate-spin" />,
        pulse: true,
        dimmed: false,
        strikethrough: false,
      };
    case "failed":
      return {
        bg: "bg-red-50",
        border: "border-red-400",
        statusIcon: <XCircle className="h-3.5 w-3.5 text-red-600" />,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
    case "pending":
    case "queued":
      return {
        bg: "bg-gray-50",
        border: "border-gray-200",
        statusIcon: null,
        pulse: false,
        dimmed: true,
        strikethrough: false,
      };
    case "waiting":
    case "blocked":
      return {
        bg: "bg-amber-50",
        border: "border-amber-400",
        statusIcon: <UserCheck className="h-3.5 w-3.5 text-amber-600" />,
        pulse: true,
        dimmed: false,
        strikethrough: false,
      };
    case "cancelled":
      return {
        bg: "bg-gray-100",
        border: "border-gray-300",
        statusIcon: <Slash className="h-3.5 w-3.5 text-gray-500" />,
        pulse: false,
        dimmed: false,
        strikethrough: true,
      };
    case "timed_out":
      return {
        bg: "bg-red-50",
        border: "border-red-300",
        statusIcon: <Clock className="h-3.5 w-3.5 text-red-500" />,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
    default:
      // Neutral / blueprint — no run selected
      return {
        bg: "bg-white",
        border: "border-border",
        statusIcon: null,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
  }
}

// ---------------------------------------------------------------------------
// Safety decision badge
// ---------------------------------------------------------------------------

const SAFETY_BADGE: Record<string, { label: string; className: string }> = {
  allow: { label: "Allowed", className: "bg-green-500 text-white" },
  deny: { label: "Denied", className: "bg-red-500 text-white" },
  require_approval: { label: "Approval required", className: "bg-amber-500 text-white" },
  throttle: { label: "Throttled", className: "bg-blue-500 text-white" },
};

// ---------------------------------------------------------------------------
// Status label
// ---------------------------------------------------------------------------

const STATUS_LABELS: Record<string, string> = {
  succeeded: "Succeeded",
  completed: "Completed",
  running: "Running",
  in_progress: "In Progress",
  failed: "Failed",
  pending: "Pending",
  queued: "Queued",
  waiting: "Waiting",
  blocked: "Blocked",
  cancelled: "Cancelled",
  timed_out: "Timed Out",
};

// ---------------------------------------------------------------------------
// RunOverlayNode
// ---------------------------------------------------------------------------

function RunOverlayNodeInner({ data, selected }: NodeProps<RunOverlayNodeData>) {
  const [hovered, setHovered] = useState(false);
  const style = getStatusStyle(data.runStatus);
  const typeIcon = STEP_TYPE_ICONS[data.stepType] ?? STEP_TYPE_ICONS.job;
  const isJobType = ["job", "agent-task", "pack-action", "tool-call"].includes(data.stepType);
  const safetyBadge =
    isJobType && data.safetyDecision?.type
      ? SAFETY_BADGE[data.safetyDecision.type]
      : null;

  return (
    <div
      className={cn(
        "relative min-w-[160px] rounded-xl border-2 px-3 py-2.5 shadow-sm transition-all duration-300",
        style.bg,
        style.border,
        style.pulse && "animate-pulse",
        style.dimmed && "opacity-60",
        selected && "ring-2 ring-accent/40",
      )}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      {/* Rich hover tooltip */}
      {hovered && (
        <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 z-50 pointer-events-none">
          <div className="rounded-lg border border-border bg-surface1 px-3 py-2 shadow-lg text-[11px] whitespace-nowrap space-y-1 min-w-[180px]">
            <div className="flex items-center gap-1.5 font-semibold text-ink">
              {typeIcon}
              {truncate(data.label, 40)}
            </div>
            <div className="flex items-center gap-1.5 text-muted">
              <span className="capitalize">{data.stepType.replace("-", " ")}</span>
              {data.runStatus && (
                <>
                  <span className="text-border">&middot;</span>
                  <span className={cn(
                    data.runStatus === "failed" && "text-danger",
                    (data.runStatus === "succeeded" || data.runStatus === "completed") && "text-success",
                    (data.runStatus === "running" || data.runStatus === "in_progress") && "text-info",
                  )}>
                    {STATUS_LABELS[data.runStatus] ?? data.runStatus}
                  </span>
                </>
              )}
            </div>
            {data.duration != null && (
              <div className="text-muted">Duration: {formatDuration(data.duration)}</div>
            )}
            {data.error && (
              <div className="text-danger whitespace-normal max-w-[250px]">
                {truncate(data.error, 80)}
              </div>
            )}
          </div>
        </div>
      )}
      <Handle type="target" position={Position.Top} className="!bg-accent !w-2.5 !h-2.5" />

      {/* Safety decision corner badge */}
      {safetyBadge && (
        <span
          className={cn(
            "absolute -right-1.5 -top-1.5 flex h-4 w-4 items-center justify-center rounded-full text-[8px]",
            safetyBadge.className,
          )}
          aria-label={safetyBadge.label}
          title={safetyBadge.label}
        >
          {data.safetyDecision?.type === "allow" && "\u2713"}
          {data.safetyDecision?.type === "deny" && "\u2717"}
          {data.safetyDecision?.type === "require_approval" && "\u270B"}
          {data.safetyDecision?.type === "throttle" && "\u23F3"}
        </span>
      )}

      {/* Main content */}
      <div className="flex items-center gap-2">
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-surface2 text-muted">
          {typeIcon}
        </div>
        <span
          className={cn(
            "flex-1 truncate text-xs font-semibold text-ink",
            style.strikethrough && "line-through",
          )}
          title={data.label}
        >
          {truncate(data.label, 40)}
        </span>
        {style.statusIcon}
      </div>

      {/* Footer: duration + error indicator */}
      {(data.duration != null || data.error) && (
        <div className="mt-1.5 flex items-center justify-between text-[10px]">
          {data.duration != null ? (
            <span className="text-muted">{formatDuration(data.duration)}</span>
          ) : (
            <span />
          )}
          {data.error && (
            <span
              className="ml-1 h-2 w-2 shrink-0 rounded-full bg-red-500"
              title={truncate(data.error, 120)}
            />
          )}
        </div>
      )}

      {/* Condition nodes: dual output handles with branch highlighting */}
      {data.stepType === "condition" ? (
        <>
          <Handle
            type="source"
            id="true"
            position={Position.Right}
            className={cn(
              "!w-2.5 !h-2.5",
              data.conditionResult === false ? "!bg-gray-300 !opacity-30" : "!bg-accent",
            )}
          />
          <span
            className={cn(
              "absolute right-0 translate-x-full pl-1 top-1/2 -translate-y-1/2 text-[10px] pointer-events-none whitespace-nowrap",
              data.conditionResult === false ? "text-gray-300" : "text-muted",
            )}
          >
            {"\u2713"} True
          </span>
          <Handle
            type="source"
            id="false"
            position={Position.Left}
            className={cn(
              "!w-2.5 !h-2.5",
              data.conditionResult === true ? "!bg-gray-300 !opacity-30" : "!bg-accent",
            )}
          />
          <span
            className={cn(
              "absolute left-0 -translate-x-full pr-1 top-1/2 -translate-y-1/2 text-[10px] pointer-events-none whitespace-nowrap",
              data.conditionResult === true ? "text-gray-300" : "text-muted",
            )}
          >
            {"\u2717"} False
          </span>
        </>
      ) : (
        <Handle type="source" position={Position.Bottom} className="!bg-accent !w-2.5 !h-2.5" />
      )}
    </div>
  );
}

export const RunOverlayNode = memo(RunOverlayNodeInner);
