import { memo, useState } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import {
  Briefcase,
  UserCheck,
  Clock,
  GitBranch,
  Bell,
  GitFork,
  Layers,
  Globe,
  Code,
  Database,
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
  condition?: string;
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
  worker: <Briefcase className="h-3.5 w-3.5" />,
  approval: <UserCheck className="h-3.5 w-3.5" />,
  delay: <Clock className="h-3.5 w-3.5" />,
  condition: <GitBranch className="h-3.5 w-3.5" />,
  notify: <Bell className="h-3.5 w-3.5" />,
  "fan-out": <GitFork className="h-3.5 w-3.5" />,
  parallel: <Layers className="h-3.5 w-3.5" />,
  http: <Globe className="h-3.5 w-3.5" />,
  transform: <Code className="h-3.5 w-3.5" />,
  switch: <GitMerge className="h-3.5 w-3.5" />,
  loop: <Repeat className="h-3.5 w-3.5" />,
  "sub-workflow": <Workflow className="h-3.5 w-3.5" />,
  storage: <Database className="h-3.5 w-3.5" />,
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
      return {
        bg: "bg-[var(--color-success)]/5",
        border: "border-[var(--color-success)]/40",
        statusIcon: <CheckCircle className="h-3.5 w-3.5 text-[var(--color-success)]" />,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
    case "running":
      return {
        bg: "bg-[var(--color-info)]/5",
        border: "border-[var(--color-info)]/40",
        statusIcon: <Loader2 className="h-3.5 w-3.5 text-[var(--color-info)] animate-spin" />,
        pulse: true,
        dimmed: false,
        strikethrough: false,
      };
    case "failed":
      return {
        bg: "bg-destructive/5",
        border: "border-destructive/40",
        statusIcon: <XCircle className="h-3.5 w-3.5 text-destructive" />,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
    case "pending":
      return {
        bg: "bg-muted/30",
        border: "border-border",
        statusIcon: null,
        pulse: false,
        dimmed: true,
        strikethrough: false,
      };
    case "waiting":
      return {
        bg: "bg-[var(--color-warning)]/5",
        border: "border-[var(--color-warning)]/40",
        statusIcon: <UserCheck className="h-3.5 w-3.5 text-[var(--color-warning)]" />,
        pulse: true,
        dimmed: false,
        strikethrough: false,
      };
    case "cancelled":
      return {
        bg: "bg-muted/50",
        border: "border-muted",
        statusIcon: <Slash className="h-3.5 w-3.5 text-muted-foreground" />,
        pulse: false,
        dimmed: false,
        strikethrough: true,
      };
    case "timed_out":
      return {
        bg: "bg-destructive/5",
        border: "border-destructive/30",
        statusIcon: <Clock className="h-3.5 w-3.5 text-destructive" />,
        pulse: false,
        dimmed: false,
        strikethrough: false,
      };
    default:
      // Neutral / blueprint — no run selected
      return {
        bg: "bg-card",
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
  allow: { label: "Allowed", className: "bg-[var(--color-success)] text-primary-foreground" },
  deny: { label: "Denied", className: "bg-destructive text-primary-foreground" },
  require_approval: { label: "Approval required", className: "bg-[var(--color-warning)] text-primary-foreground" },
  throttle: { label: "Throttled", className: "bg-[var(--color-info)] text-primary-foreground" },
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
          <div className="min-w-[180px] space-y-1 whitespace-nowrap rounded-2xl border border-border bg-surface-1 px-3 py-2 text-[11px] shadow-soft">
            <div className="flex items-center gap-1.5 font-semibold text-ink">
              {typeIcon}
              {truncate(data.label, 40)}
            </div>
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <span className="capitalize">{data.stepType.replace("-", " ")}</span>
              {data.runStatus && (
                <>
                  <span className="text-border">&middot;</span>
                  <span className={cn(
                    data.runStatus === "failed" && "text-destructive",
                    data.runStatus === "succeeded" && "text-success",
                    data.runStatus === "running" && "text-info",
                  )}>
                    {STATUS_LABELS[data.runStatus] ?? data.runStatus}
                  </span>
                </>
              )}
            </div>
            {data.duration != null && (
              <div className="text-muted-foreground">Duration: {formatDuration(data.duration)}</div>
            )}
            {data.error && (
              <div className="max-w-[250px] whitespace-normal text-destructive">
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
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-surface-2 text-muted-foreground">
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

      {/* Condition subtitle */}
      {data.condition && (
        <p className="mt-1 truncate text-[9px] text-muted-foreground font-mono" title={data.condition}>
          {truncate(data.condition, 35)}
        </p>
      )}

      {/* Footer: duration + error indicator */}
      {(data.duration != null || data.error) && (
        <div className="mt-1.5 flex items-center justify-between text-[10px]">
          {data.duration != null ? (
            <span className="text-muted-foreground">{formatDuration(data.duration)}</span>
          ) : (
            <span />
          )}
          {data.error && (
            <span
              className="ml-1 h-2 w-2 shrink-0 rounded-full bg-destructive"
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
              data.conditionResult === false ? "!bg-muted !opacity-30" : "!bg-accent",
            )}
          />
          <span
            className={cn(
              "absolute right-0 translate-x-full pl-1 top-1/2 -translate-y-1/2 text-[10px] pointer-events-none whitespace-nowrap",
              data.conditionResult === false ? "text-muted-foreground" : "text-muted-foreground",
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
              data.conditionResult === true ? "!bg-muted !opacity-30" : "!bg-accent",
            )}
          />
          <span
            className={cn(
              "absolute left-0 -translate-x-full pr-1 top-1/2 -translate-y-1/2 text-[10px] pointer-events-none whitespace-nowrap",
              data.conditionResult === true ? "text-muted-foreground" : "text-muted-foreground",
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
