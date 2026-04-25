import { memo, useState, useCallback, useMemo } from "react";
import { Handle, Position, type NodeProps } from "reactflow";
import { CheckCircle, Loader2, XCircle, Clock, Slash, UserCheck, ShieldAlert } from "lucide-react";
import { cn, formatDuration, truncate } from "@/lib/utils";
import type { UnifiedNodeData } from "../types";
import {
  getStepMeta,
  getStatusVisual,
  getSafetyBadge,
  isJobType,
} from "../nodeRegistry";

// ---------------------------------------------------------------------------
// Status icon resolver (shared between tooltip and node body)
// ---------------------------------------------------------------------------

function StatusIcon({ status }: { status?: string }) {
  switch (status) {
    case "succeeded":
      return <CheckCircle className="h-3.5 w-3.5 text-[var(--color-success)]" />;
    case "running":
      return <Loader2 className="h-3.5 w-3.5 text-[var(--color-info)] animate-spin" />;
    case "failed":
      return <XCircle className="h-3.5 w-3.5 text-destructive" />;
    case "denied":
      return <ShieldAlert className="h-3.5 w-3.5 text-[var(--color-governance)]" />;
    case "waiting":
      return <UserCheck className="h-3.5 w-3.5 text-[var(--color-warning)]" />;
    case "cancelled":
      return <Slash className="h-3.5 w-3.5 text-muted-foreground" />;
    case "timed_out":
      return <Clock className="h-3.5 w-3.5 text-destructive" />;
    default:
      return null;
  }
}

// ---------------------------------------------------------------------------
// Hover tooltip
// ---------------------------------------------------------------------------

function TooltipRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-2 text-muted-foreground">
      <span className="shrink-0 text-muted-foreground/70">{label}:</span>
      <span className="font-mono truncate max-w-[200px]">{value}</span>
    </div>
  );
}

function NodeTooltip({ data }: { data: UnifiedNodeData }) {
  const statusVisual = getStatusVisual(data.runStatus);
  const isRunMode = !!data.runStatus;
  const config = data.config as Record<string, unknown> | undefined;
  const retry = data.retry as { max_retries?: number; initial_backoff_sec?: number } | undefined;
  const deps = (data as unknown as Record<string, unknown>).depends_on as string[] | undefined;

  return (
    <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-2 z-50 pointer-events-none">
      <div className="min-w-[180px] max-w-[300px] space-y-1 whitespace-nowrap rounded-2xl border border-border bg-surface-1 px-3 py-2.5 text-xs shadow-soft">
        {/* Type header */}
        <div className="flex items-center gap-1.5 text-muted-foreground">
          <span className="capitalize font-semibold text-ink">{data.stepType.replace(/-/g, " ")}</span>
          {isRunMode && (
            <>
              <span className="text-border">&middot;</span>
              <span>{statusVisual.label}</span>
            </>
          )}
        </div>

        {/* Step ID — always useful, not shown on node card */}
        <TooltipRow label="id" value={data.stepId} />

        {isRunMode ? (
          <>
            {/* Run mode: show error, duration, safety, job context */}
            {data.error && (
              <div className="max-w-[280px] whitespace-normal text-destructive">
                {truncate(data.error, 200)}
              </div>
            )}
            {data.duration != null && (
              <TooltipRow label="duration" value={`${data.duration}ms`} />
            )}
            {data.safetyDecision && (
              <TooltipRow label="safety" value={(data.safetyDecision as { type: string }).type} />
            )}
          </>
        ) : (
          <>
            {/* Edit mode: show config not visible on the card */}
            {deps && deps.length > 0 && (
              <TooltipRow label="depends" value={`${deps.length} step${deps.length > 1 ? "s" : ""}`} />
            )}
            {data.timeout_sec != null && data.timeout_sec > 0 && (
              <TooltipRow label="timeout" value={`${data.timeout_sec}s`} />
            )}
            {retry && retry.max_retries != null && retry.max_retries > 0 && (
              <TooltipRow label="retries" value={`${retry.max_retries}× / ${retry.initial_backoff_sec ?? 1}s`} />
            )}
            {data.on_error && (
              <TooltipRow label="on_error" value={data.on_error} />
            )}
            {config?.branches && typeof config.branches === "object" && (
              <TooltipRow label="branches" value={`${Object.keys(config.branches as Record<string, unknown>).length}`} />
            )}
          </>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Subtitle line — shows the most relevant config detail
// ---------------------------------------------------------------------------

function getSubtitle(data: UnifiedNodeData): string | null {
  if (data.topic) return data.topic;
  if (data.condition) return `if: ${data.condition}`;
  if (data.delay_sec) return `wait ${data.delay_sec}s`;
  if (data.delay_until) return `until: ${data.delay_until}`;
  if (data.for_each) return `each: ${data.for_each}`;

  const config = data.config as Record<string, unknown> | undefined;
  if (config?.url) return String(config.url);
  if (config?.message) return truncate(String(config.message), 40);

  const input = data.input as Record<string, unknown> | undefined;
  if (input?.prompt) return truncate(String(input.prompt), 40);

  return null;
}

// ---------------------------------------------------------------------------
// UnifiedNode
// ---------------------------------------------------------------------------

function UnifiedNodeInner({ data, selected }: NodeProps<UnifiedNodeData>) {
  const [hovered, setHovered] = useState(false);
  const meta = getStepMeta(data.stepType);
  const statusVisual = getStatusVisual(data.runStatus);
  const safetyBadge = isJobType(data.stepType) ? getSafetyBadge(data.safetyDecision?.type) : null;
  const subtitle = getSubtitle(data);
  const Icon = meta.icon;

  // Switch node: extract case handles from config.branches
  const switchCases = useMemo(() => {
    if (data.stepType !== "switch") return [];
    const branches = (data.config as Record<string, unknown> | undefined)?.branches;
    if (branches && typeof branches === "object" && !Array.isArray(branches)) {
      return Object.keys(branches as Record<string, unknown>);
    }
    return [];
  }, [data.stepType, data.config]);
  const isEdit = data.mode === "edit";
  const hasRunOverlay = !!data.runStatus;

  const handleMouseEnter = useCallback(() => setHovered(true), []);
  const handleMouseLeave = useCallback(() => setHovered(false), []);

  // Derive accent background class from iconColor for the left stripe
  const accentStripe = meta.iconColor.replace("text-", "bg-");

  // Determine border and background based on run status or selection
  const borderClass = hasRunOverlay
    ? statusVisual.border
    : selected
      ? "border-accent ring-2 ring-accent/30"
      : "border-border/60 hover:border-accent/40";

  const bgClass = hasRunOverlay ? statusVisual.bg : "bg-card";

  const hasError = !!data.error && !statusVisual.dimmed;

  return (
    <div
      className={cn(
        "relative min-w-[180px] max-w-[220px] rounded-xl border overflow-hidden transition-all duration-200",
        "shadow-soft",
        bgClass,
        borderClass,
        statusVisual.pulse && "animate-pulse",
        statusVisual.dimmed && "opacity-60",
        hasError && "ring-2 ring-destructive/30 animate-[pulse_2s_ease-in-out_infinite]",
        isEdit && hovered && !hasRunOverlay && "shadow-md -translate-y-px",
      )}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
    >
      {/* Left accent stripe — visual identity per node category */}
      {!hasRunOverlay && (
        <div className={cn("absolute left-0 top-0 bottom-0 w-[2.5px] rounded-l-xl", accentStripe)} />
      )}

      {/* Tooltip on hover */}
      {hovered && <NodeTooltip data={data} />}

      {/* Top input handle */}
      {!meta.hideInput && (
        <Handle
          type="target"
          position={Position.Top}
          className={cn(
            "!w-3 !h-3 !border-2 !border-card !rounded-full",
            hasRunOverlay ? "!bg-accent" : "!bg-muted-foreground/50",
            isEdit && "hover:!bg-accent",
          )}
        />
      )}

      {/* Safety decision corner badge */}
      {safetyBadge && (
        <span
          className={cn(
            "absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full text-xs font-bold shadow-sm z-10",
            safetyBadge.className,
          )}
          title={safetyBadge.label}
        >
          {safetyBadge.glyph}
        </span>
      )}

      {/* Main content */}
      <div className="px-3 py-2.5">
        {/* Icon + Label row */}
        <div className="flex items-center gap-2.5">
          <div
            className={cn(
              "flex h-8 w-8 shrink-0 items-center justify-center rounded-xl border border-black/[0.04] dark:border-white/[0.06]",
              meta.accent,
            )}
            style={{ boxShadow: "inset 0 1px 2px rgba(0,0,0,0.05)" }}
          >
            <Icon className={cn("h-4 w-4", meta.iconColor)} />
          </div>
          <div className="flex-1 min-w-0">
            <span
              className={cn(
                "block text-xs font-semibold text-ink truncate",
                statusVisual.strikethrough && "line-through",
              )}
              title={data.label}
            >
              {truncate(data.label, 24)}
            </span>
            <span className="block text-xs text-muted-foreground capitalize">
              {meta.label}
            </span>
          </div>
          {/* Status icon (run mode only) */}
          <StatusIcon status={data.runStatus} />
        </div>

        {/* Subtitle */}
        {subtitle && (
          <p
            className="mt-1.5 truncate text-xs text-muted-foreground font-mono"
            title={subtitle}
          >
            {truncate(subtitle, 30)}
          </p>
        )}

        {/* Footer: duration + error indicator (run mode) */}
        {(data.duration != null || data.error) && (
          <div className="mt-1.5 flex items-center justify-between text-xs">
            {data.duration != null ? (
              <span className="text-muted-foreground">{formatDuration(data.duration)}</span>
            ) : (
              <span />
            )}
            {data.error && (
              <span
                className="ml-1 h-2.5 w-2.5 shrink-0 rounded-full bg-destructive ring-2 ring-destructive/20"
                title={truncate(data.error, 120)}
              />
            )}
          </div>
        )}
      </div>

      {/* Output handles */}
      {data.stepType === "condition" ? (
        <>
          <Handle
            type="source"
            id="true"
            position={Position.Right}
            className={cn(
              "!w-3 !h-3 !border-2 !border-card !rounded-full",
              data.conditionResult === false
                ? "!bg-muted !opacity-30"
                : data.conditionResult === true
                  ? "!bg-[var(--color-success)]"
                  : "!bg-muted-foreground/50",
            )}
          />
          <span className="absolute right-0 translate-x-full pl-1.5 top-1/2 -translate-y-1/2 pointer-events-none whitespace-nowrap">
            <span className="rounded-full bg-[var(--color-success)]/15 px-1.5 py-0.5 text-[10px] font-mono font-semibold text-[var(--color-success)]">true</span>
          </span>
          <Handle
            type="source"
            id="false"
            position={Position.Left}
            className={cn(
              "!w-3 !h-3 !border-2 !border-card !rounded-full",
              data.conditionResult === true
                ? "!bg-muted !opacity-30"
                : data.conditionResult === false
                  ? "!bg-destructive"
                  : "!bg-muted-foreground/50",
            )}
          />
          <span className="absolute left-0 -translate-x-full pr-1.5 top-1/2 -translate-y-1/2 pointer-events-none whitespace-nowrap">
            <span className="rounded-full bg-destructive/15 px-1.5 py-0.5 text-[10px] font-mono font-semibold text-destructive">false</span>
          </span>
        </>
      ) : data.stepType === "switch" && switchCases.length > 0 ? (
        <>
          {switchCases.map((caseId, idx) => {
            const total = switchCases.length;
            const offsetPct = total === 1 ? 50 : 20 + (idx / (total - 1)) * 60;
            return (
              <div key={caseId}>
                <Handle
                  type="source"
                  id={caseId}
                  position={Position.Bottom}
                  className="!w-2.5 !h-2.5 !border-2 !border-card !rounded-full !bg-accent"
                  style={{ left: `${offsetPct}%` }}
                />
                <span
                  className="absolute pointer-events-none whitespace-nowrap text-[9px] font-mono text-muted-foreground"
                  style={{ left: `${offsetPct}%`, bottom: -16, transform: "translateX(-50%)" }}
                >
                  {truncate(caseId, 8)}
                </span>
              </div>
            );
          })}
        </>
      ) : (
        <Handle
          type="source"
          position={Position.Bottom}
          className={cn(
            "!w-3 !h-3 !border-2 !border-card !rounded-full",
            hasRunOverlay ? "!bg-accent" : "!bg-muted-foreground/50",
            isEdit && "hover:!bg-accent",
          )}
        />
      )}
    </div>
  );
}

export const UnifiedNode = memo(UnifiedNodeInner);

