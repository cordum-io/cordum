import { useState } from "react";
import {
  ChevronDown,
  CheckCircle2,
  XCircle,
  Clock,
  AlertTriangle,
  Shield,
  Play,
  Loader2,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { Badge } from "../ui/Badge";
import type {
  TimelineEvent,
  TimelineEventKind,
  JobEventContext,
  JobProgressEvent,
  Job,
  UrgencyLevel,
} from "../../api/types";

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface UnifiedTimelineProps {
  events: TimelineEvent[];
  job: Job;
  progressEvents: JobProgressEvent[];
  latestPercent: number | null;
  isStreaming: boolean;
  lastHeartbeat: string | null;
  onApprove?: (note?: string) => void;
  onReject?: (reason: string) => void;
  onRemediate?: () => void;
  onQuickRemediate?: () => void;
  canApprove: boolean;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const DOT_COLOR: Record<TimelineEvent["status"], string> = {
  completed: "bg-emerald-500",
  current: "bg-[hsl(var(--accent))]",
  error: "bg-red-500",
  warning: "bg-amber-500",
  pending: "bg-gray-400",
};

const STATUS_ICON: Partial<Record<TimelineEventKind, typeof CheckCircle2>> = {
  succeeded: CheckCircle2,
  failed: XCircle,
  denied: XCircle,
  timeout: Clock,
  cancelled: XCircle,
  approval_requested: AlertTriangle,
  policy_evaluated: Shield,
  executing: Play,
  quarantined: AlertTriangle,
};

const URGENCY_THRESHOLDS: { max: number; level: UrgencyLevel }[] = [
  { max: 2 * 60 * 1000, level: "fresh" },
  { max: 15 * 60 * 1000, level: "aging" },
  { max: 60 * 60 * 1000, level: "critical" },
];

const URGENCY_BADGE_VARIANT: Record<UrgencyLevel, "success" | "warning" | "danger"> = {
  fresh: "success",
  aging: "warning",
  critical: "danger",
  breach: "danger",
};

const URGENCY_LABELS: Record<UrgencyLevel, string> = {
  fresh: "Fresh",
  aging: "Aging",
  critical: "Critical",
  breach: "SLA Breach",
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function getUrgencyLevel(eventTimestamp: string): UrgencyLevel {
  const elapsed = Date.now() - new Date(eventTimestamp).getTime();
  for (const t of URGENCY_THRESHOLDS) {
    if (elapsed < t.max) return t.level;
  }
  return "breach";
}

function formatMs(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

function formatTimestamp(iso: string): string {
  try {
    return new Date(iso).toLocaleTimeString(undefined, {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  } catch {
    return iso;
  }
}

// ---------------------------------------------------------------------------
// ExpandedContext — key-value display for event context data
// ---------------------------------------------------------------------------

function ExpandedContext({ data }: { data: JobEventContext }) {
  const entries = Object.entries(data).filter(
    ([, v]) => v !== undefined && v !== null && v !== "",
  );
  if (entries.length === 0) return null;

  return (
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
      {entries.map(([key, value]) => (
        <div key={key} className="contents">
          <dt className="text-xs text-muted-foreground">{key}</dt>
          <dd className="font-mono text-xs">
            {Array.isArray(value) ? (
              <span className="flex flex-wrap gap-1">
                {value.map((v, i) => (
                  <span
                    key={i}
                    className="rounded bg-surface2 px-1.5 py-0.5 text-[10px]"
                  >
                    {String(v)}
                  </span>
                ))}
              </span>
            ) : typeof value === "number" ? (
              String(value)
            ) : (
              String(value)
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

// ---------------------------------------------------------------------------
// ExecutingSection — progress bar, status messages, heartbeat warning
// ---------------------------------------------------------------------------

function ExecutingSection({
  progressEvents,
  latestPercent,
  isStreaming,
  lastHeartbeat,
}: {
  progressEvents: JobProgressEvent[];
  latestPercent: number | null;
  isStreaming: boolean;
  lastHeartbeat: string | null;
}) {
  const heartbeatAge = lastHeartbeat
    ? Math.round((Date.now() - new Date(lastHeartbeat).getTime()) / 1000)
    : null;

  return (
    <div className="mt-2 rounded border border-border bg-surface1 border-t-2 border-t-[hsl(var(--accent))]">
      <div className="p-3 space-y-3">
        {/* Progress bar */}
        <div className="space-y-1">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>Progress</span>
            {latestPercent !== null && (
              <span className="font-mono">{latestPercent}%</span>
            )}
          </div>
          <div className="h-2 w-full rounded-full bg-surface2 overflow-hidden">
            {latestPercent !== null ? (
              <div
                className="h-full rounded-full bg-[hsl(var(--accent))] transition-all duration-300 ease-out"
                style={{ width: `${Math.min(100, Math.max(0, latestPercent))}%` }}
              />
            ) : (
              <div className="h-full w-1/3 rounded-full bg-[hsl(var(--accent))] animate-pulse" />
            )}
          </div>
        </div>

        {/* Status messages */}
        {progressEvents.length > 0 && (
          <div className="max-h-48 overflow-y-auto space-y-0.5">
            {progressEvents.map((evt, i) => (
              <div key={i} className="flex gap-2 font-mono text-xs">
                <span className="shrink-0 text-muted-foreground">
                  {formatTimestamp(evt.timestamp)}
                </span>
                <span>{evt.message}</span>
              </div>
            ))}
          </div>
        )}

        {/* Heartbeat warning */}
        {heartbeatAge !== null && heartbeatAge > 15 && (
          <div className="flex items-center gap-1.5 text-xs text-amber-500">
            <AlertTriangle className="h-3 w-3" />
            <span>Last heartbeat {heartbeatAge}s ago</span>
          </div>
        )}

        {/* Streaming indicator */}
        {isStreaming && (
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span>Receiving updates...</span>
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// RemediationInline — compact remediation for denied/failed entries
// ---------------------------------------------------------------------------

function RemediationInline({
  job,
  onRemediate,
  onQuickRemediate,
}: {
  job: Job;
  onRemediate?: () => void;
  onQuickRemediate?: () => void;
}) {
  const decision = job.safetyDecision;
  if (!decision && !job.errorMessage) return null;
  if (job.status !== "failed" && job.status !== "denied" && job.status !== "output_quarantined") {
    return null;
  }

  return (
    <div className="mt-2 rounded border border-border bg-surface1 border-t-2 border-t-red-500">
      <div className="p-3 space-y-2">
        {decision?.reason && (
          <p className="text-xs text-muted-foreground">{decision.reason}</p>
        )}
        {decision?.matchedRule && (
          <p className="text-xs">
            <span className="text-muted-foreground">Matched rule: </span>
            <span className="font-mono">{decision.matchedRule}</span>
          </p>
        )}
        <div className="flex gap-2">
          {onQuickRemediate && (
            <button
              type="button"
              onClick={onQuickRemediate}
              className="rounded bg-[hsl(var(--accent))] px-3 py-1.5 text-xs font-medium text-white transition-colors hover:opacity-90"
            >
              Apply Remediation
            </button>
          )}
          {onRemediate && (
            <button
              type="button"
              onClick={onRemediate}
              className="rounded border border-border bg-surface2 px-3 py-1.5 text-xs font-medium transition-colors hover:bg-surface3"
            >
              Customize &rarr;
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ApprovalActions — inline approve/reject for approval_requested entries
// ---------------------------------------------------------------------------

function ApprovalActions({
  event,
  canApprove,
  onApprove,
  onReject,
}: {
  event: TimelineEvent;
  canApprove: boolean;
  onApprove?: (note?: string) => void;
  onReject?: (reason: string) => void;
}) {
  const [mode, setMode] = useState<"idle" | "reject" | "comment">("idle");
  const [inputValue, setInputValue] = useState("");

  if (!canApprove) return null;

  const urgency = getUrgencyLevel(event.timestamp);

  function handleRejectSubmit() {
    if (inputValue.trim() && onReject) {
      onReject(inputValue.trim());
      setMode("idle");
      setInputValue("");
    }
  }

  function handleApproveWithComment() {
    if (onApprove) {
      onApprove(inputValue.trim() || undefined);
      setMode("idle");
      setInputValue("");
    }
  }

  return (
    <div className="mt-2 space-y-2">
      <Badge
        variant={URGENCY_BADGE_VARIANT[urgency]}
        className={cn(urgency === "breach" && "animate-pulse")}
      >
        {URGENCY_LABELS[urgency]}
      </Badge>

      {mode === "idle" && (
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => onApprove?.()}
            className="rounded bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-emerald-700"
          >
            Approve
          </button>
          <button
            type="button"
            onClick={() => setMode("reject")}
            className="rounded bg-red-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-red-700"
          >
            Reject
          </button>
          <button
            type="button"
            onClick={() => setMode("comment")}
            className="rounded border border-border bg-surface2 px-3 py-1.5 text-xs font-medium transition-colors hover:bg-surface3"
          >
            Approve with comment &rarr;
          </button>
        </div>
      )}

      {mode === "reject" && (
        <div className="flex gap-2">
          <input
            type="text"
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleRejectSubmit()}
            placeholder="Reason for rejection (required)"
            className="flex-1 rounded border border-border bg-surface1 px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-1 focus:ring-[hsl(var(--accent))]"
            autoFocus
          />
          <button
            type="button"
            onClick={handleRejectSubmit}
            disabled={!inputValue.trim()}
            className="rounded bg-red-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-red-700 disabled:opacity-50"
          >
            Reject
          </button>
          <button
            type="button"
            onClick={() => { setMode("idle"); setInputValue(""); }}
            className="rounded border border-border px-3 py-1.5 text-xs transition-colors hover:bg-surface2"
          >
            Cancel
          </button>
        </div>
      )}

      {mode === "comment" && (
        <div className="flex gap-2">
          <input
            type="text"
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && handleApproveWithComment()}
            placeholder="Approval comment (optional)"
            className="flex-1 rounded border border-border bg-surface1 px-2 py-1.5 text-xs font-mono focus:outline-none focus:ring-1 focus:ring-[hsl(var(--accent))]"
            autoFocus
          />
          <button
            type="button"
            onClick={handleApproveWithComment}
            className="rounded bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-emerald-700"
          >
            Approve
          </button>
          <button
            type="button"
            onClick={() => { setMode("idle"); setInputValue(""); }}
            className="rounded border border-border px-3 py-1.5 text-xs transition-colors hover:bg-surface2"
          >
            Cancel
          </button>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// TimelineEntry — single entry in the vertical timeline
// ---------------------------------------------------------------------------

function TimelineEntry({
  event,
  index,
  isLast,
  children,
  isStreaming,
}: {
  event: TimelineEvent;
  index: number;
  isLast: boolean;
  children?: React.ReactNode;
  isStreaming?: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const Icon = STATUS_ICON[event.kind];
  const hasExpandedData =
    event.expandedData && Object.keys(event.expandedData).length > 0;

  return (
    <div
      className="animate-fade-in relative pl-8"
      style={{ animationDelay: `${index * 60}ms` }}
    >
      {/* Connector line */}
      {!isLast && (
        <div className="absolute left-[9px] top-6 bottom-0 w-px bg-border" />
      )}

      {/* Status dot */}
      <div
        className={cn(
          "absolute left-0 top-1 h-[18px] w-[18px] rounded-full border-2 border-surface1 flex items-center justify-center",
          DOT_COLOR[event.status],
          event.kind === "executing" && isStreaming && "animate-pulse",
        )}
      >
        {Icon && (
          <Icon className="h-2.5 w-2.5 text-white" strokeWidth={3} />
        )}
      </div>

      {/* Content */}
      <div className="pb-6">
        <div
          className={cn(
            "group",
            hasExpandedData && "cursor-pointer",
          )}
          onClick={() => hasExpandedData && setExpanded((v) => !v)}
          role={hasExpandedData ? "button" : undefined}
          tabIndex={hasExpandedData ? 0 : undefined}
          onKeyDown={
            hasExpandedData
              ? (e) => (e.key === "Enter" || e.key === " ") && setExpanded((v) => !v)
              : undefined
          }
        >
          <p className="font-mono text-xs text-muted-foreground">
            {formatTimestamp(event.timestamp)}
          </p>
          <div className="flex items-center gap-1.5">
            <p className="text-sm font-semibold">{event.title}</p>
            {hasExpandedData && (
              <ChevronDown
                className={cn(
                  "h-3.5 w-3.5 text-muted-foreground transition-transform duration-150 ease-out",
                  expanded && "rotate-180",
                )}
              />
            )}
          </div>
          {event.detail && (
            <p className="text-xs text-muted-foreground">{event.detail}</p>
          )}
        </div>

        {/* Expanded context */}
        {expanded && event.expandedData && (
          <div className="overflow-hidden transition-all duration-150 ease-out">
            <ExpandedContext data={event.expandedData} />
          </div>
        )}

        {/* Kind-specific content */}
        {children}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Specialized entry detail renderers
// ---------------------------------------------------------------------------

function ApprovedDetail({ data }: { data?: JobEventContext }) {
  if (!data) return null;
  return (
    <div className="mt-1 space-y-0.5 text-xs text-muted-foreground">
      {data.approvedBy && (
        <p>
          Approved by <span className="font-mono font-medium text-ink">{data.approvedBy}</span>
          {data.approvalRole && (
            <span className="ml-1">({data.approvalRole})</span>
          )}
        </p>
      )}
      {data.waitMs !== undefined && data.waitMs > 0 && (
        <p>Wait time: <span className="font-mono">{formatMs(data.waitMs)}</span></p>
      )}
      {data.approvalNote && (
        <p className="italic">&ldquo;{data.approvalNote}&rdquo;</p>
      )}
    </div>
  );
}

function OutputScannedDetail({ data }: { data?: JobEventContext }) {
  if (!data) return null;
  return (
    <div className="mt-1 flex items-center gap-2">
      {data.findings !== undefined && (
        <Badge variant={data.findings > 0 ? "warning" : "success"}>
          {data.findings} finding{data.findings !== 1 ? "s" : ""}
        </Badge>
      )}
      {data.scanner && (
        <span className="font-mono text-xs text-muted-foreground">{data.scanner}</span>
      )}
    </div>
  );
}

function SucceededDetail({ data }: { data?: JobEventContext }) {
  if (!data) return null;
  return (
    <div className="mt-1 space-y-0.5 text-xs text-muted-foreground">
      {data.durationMs !== undefined && data.durationMs > 0 && (
        <p>Duration: <span className="font-mono">{formatMs(data.durationMs)}</span></p>
      )}
      {data.resultPtr && (
        <p>Result: <span className="font-mono">{data.resultPtr}</span></p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// UnifiedTimeline — main component
// ---------------------------------------------------------------------------

export function UnifiedTimeline({
  events,
  job,
  progressEvents,
  latestPercent,
  isStreaming,
  lastHeartbeat,
  onApprove,
  onReject,
  onRemediate,
  onQuickRemediate,
  canApprove,
}: UnifiedTimelineProps) {
  if (events.length === 0) {
    return (
      <div className="py-8 text-center text-sm text-muted-foreground">
        No timeline events available.
      </div>
    );
  }

  return (
    <div className="relative">
      {events.map((event, i) => {
        const isLast = i === events.length - 1;

        return (
          <TimelineEntry
            key={`${event.timestamp}-${event.kind}-${i}`}
            event={event}
            index={i}
            isLast={isLast}
            isStreaming={isStreaming}
          >
            {/* Executing — progress section */}
            {event.kind === "executing" && (
              <ExecutingSection
                progressEvents={progressEvents}
                latestPercent={latestPercent}
                isStreaming={isStreaming}
                lastHeartbeat={lastHeartbeat}
              />
            )}

            {/* Denied / Failed — inline remediation */}
            {(event.kind === "denied" || event.kind === "failed") && (
              <RemediationInline
                job={job}
                onRemediate={onRemediate}
                onQuickRemediate={onQuickRemediate}
              />
            )}

            {/* Approval requested — inline actions */}
            {event.kind === "approval_requested" && (
              <ApprovalActions
                event={event}
                canApprove={canApprove}
                onApprove={onApprove}
                onReject={onReject}
              />
            )}

            {/* Approved — approver details */}
            {event.kind === "approved" && (
              <ApprovedDetail data={event.expandedData} />
            )}

            {/* Output scanned — findings badge */}
            {event.kind === "output_scanned" && (
              <OutputScannedDetail data={event.expandedData} />
            )}

            {/* Succeeded — duration and result */}
            {event.kind === "succeeded" && (
              <SucceededDetail data={event.expandedData} />
            )}
          </TimelineEntry>
        );
      })}
    </div>
  );
}
