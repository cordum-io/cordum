import { memo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import {
  Briefcase,
  UserCheck,
  Clock,
  GitBranch,
  Bell,
  GitFork,
  CheckCircle,
  Loader2,
  XCircle,
  Slash,
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
// RunOverlayNode
// ---------------------------------------------------------------------------

function RunOverlayNodeInner({ data, selected }: NodeProps<RunOverlayNodeData>) {
  const style = getStatusStyle(data.runStatus);
  const typeIcon = STEP_TYPE_ICONS[data.stepType] ?? STEP_TYPE_ICONS.job;
  const safetyBadge =
    data.stepType === "job" && data.safetyDecision?.type
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
    >
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

      <Handle type="source" position={Position.Bottom} className="!bg-accent !w-2.5 !h-2.5" />
    </div>
  );
}

export const RunOverlayNode = memo(RunOverlayNodeInner);
