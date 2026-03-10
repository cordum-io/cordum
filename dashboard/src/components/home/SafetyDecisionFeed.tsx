/**
 * DESIGN: "Control Surface" — Live Safety Decision Feed
 * Compact rows, semantic badges, keyboard-accessible, retry on error.
 */
import { useEffect, useMemo, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { ShieldCheck, Wifi, WifiOff, RefreshCw } from "lucide-react";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { Button } from "@/components/ui/Button";
import { useSafetyDecisions } from "@/hooks/useSafetyDecisions";
import { useEventStore, type SafetyDecisionEvent } from "@/state/events";
import { cn } from "@/lib/utils";

const FEED_LIMIT = 40;

const decisionVariant: Record<string, "healthy" | "danger" | "warning" | "info" | "muted"> = {
  allow: "healthy",
  deny: "danger",
  require_approval: "warning",
  allow_with_constraints: "info",
  throttle: "info",
};

const decisionLabel: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Approval",
  allow_with_constraints: "Constrained",
  throttle: "Throttle",
};

function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const h = String(d.getHours()).padStart(2, "0");
  const m = String(d.getMinutes()).padStart(2, "0");
  const s = String(d.getSeconds()).padStart(2, "0");
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return `${h}:${m}:${s}.${ms}`;
}

function statusLabel(status: string): string {
  switch (status) {
    case "connected":
      return "Stream Live";
    case "connecting":
      return "Connecting";
    case "reconnecting":
      return "Reconnecting";
    default:
      return "Offline";
  }
}

function FeedRow({ event, onClick }: { event: SafetyDecisionEvent; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      aria-label={`${decisionLabel[event.decision] ?? event.decision} decision on ${event.topic} at ${fmtTime(event.timestamp)}`}
      className="flex items-center gap-3 w-full text-left border-b border-border/40 px-4 py-2.5 text-xs last:border-b-0 hover:bg-surface-1/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-cordum transition-colors"
    >
      <span className="shrink-0 w-[88px] font-mono text-muted-foreground tabular-nums">
        {fmtTime(event.timestamp)}
      </span>
      <span className="shrink-0 truncate max-w-[180px] font-medium text-foreground" title={event.topic}>
        {event.topic}
      </span>
      <StatusBadge variant={decisionVariant[event.decision] ?? "muted"}>
        {decisionLabel[event.decision] ?? event.decision}
      </StatusBadge>
      {event.matchedRule && (
        <span className="truncate text-muted-foreground max-w-[160px] font-mono" title={event.matchedRule}>
          {event.matchedRule}
        </span>
      )}
      {typeof event.evalTimeMs === "number" && (
        <span className="ml-auto shrink-0 font-mono text-muted-foreground tabular-nums">
          {event.evalTimeMs}ms
        </span>
      )}
    </button>
  );
}

export function SafetyDecisionFeed() {
  const navigate = useNavigate();
  const wsStatus = useEventStore((s) => s.status);
  const { decisions, isLoading, isError, isFetching, refetch } = useSafetyDecisions(FEED_LIMIT);
  const listRef = useRef<HTMLDivElement>(null);

  const counts = useMemo(() => {
    const out = { allow: 0, deny: 0, require_approval: 0, throttle: 0 };
    for (const d of decisions) {
      if (d.decision in out) {
        out[d.decision as keyof typeof out] += 1;
      }
    }
    return out;
  }, [decisions]);

  useEffect(() => {
    if (listRef.current) {
      listRef.current.scrollTop = 0;
    }
  }, [decisions.length]);

  const handleRowClick = (event: SafetyDecisionEvent) => {
    if (event.decision === "require_approval") {
      navigate("/approvals");
    } else if (event.decision === "deny") {
      navigate("/audit");
    } else {
      navigate("/jobs");
    }
  };

  return (
    <div className="instrument-card flex flex-col h-[420px]">
      {/* Header */}
      <div className="space-y-3 border-b border-border px-5 py-4">
        <div className="flex items-start gap-2.5">
          <ShieldCheck className="mt-0.5 w-4 h-4 text-cordum shrink-0" />
          <div className="flex-1 min-w-0">
            <h3 className="font-display text-sm font-semibold text-foreground">Live Safety Decisions</h3>
            <p className="text-[11px] text-muted-foreground mt-0.5">
              Recent decisions from stream and history (latest {FEED_LIMIT})
            </p>
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <span
              className={cn(
                "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium",
                wsStatus === "connected"
                  ? "border-[var(--color-success)]/30 bg-[var(--color-success)]/10 text-[var(--color-success)]"
                  : wsStatus === "connecting" || wsStatus === "reconnecting"
                    ? "border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 text-[var(--color-warning)]"
                    : "border-border bg-surface-2 text-muted-foreground",
              )}
            >
              {wsStatus === "connected" ? <Wifi className="w-3 h-3" /> : <WifiOff className="w-3 h-3" />}
              {statusLabel(wsStatus)}
            </span>
            <span className="rounded-full bg-surface-2 px-2 py-0.5 text-[10px] font-mono text-muted-foreground">
              {decisions.length}
            </span>
          </div>
        </div>

        {/* Mini KPI strip */}
        {decisions.length > 0 && (
          <div className="grid grid-cols-4 gap-2">
            {(["allow", "deny", "require_approval", "throttle"] as const).map((key) => (
              <div key={key} className="rounded-md border border-border/50 bg-surface-2/30 px-2.5 py-1.5 text-center">
                <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
                  {decisionLabel[key]}
                </p>
                <p className="text-xs font-semibold font-mono text-foreground">{counts[key]}</p>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="space-y-2 px-5 py-4 flex-1">
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} className="skeleton h-9 rounded-md" />
          ))}
        </div>
      ) : decisions.length === 0 && isError ? (
        <div className="flex-1 flex flex-col items-center justify-center px-5">
          <EmptyState
            icon={<WifiOff className="w-5 h-5" />}
            title="Unable to load safety decisions"
            description="Check gateway connectivity and auth headers."
            action={
              <Button variant="outline" size="sm" onClick={() => refetch()}>
                <RefreshCw className="w-3 h-3 mr-1" />
                Retry
              </Button>
            }
          />
        </div>
      ) : decisions.length === 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <EmptyState
            icon={<ShieldCheck className="w-5 h-5" />}
            title="No safety decisions yet"
            description="Waiting for live stream or recent job history."
          />
        </div>
      ) : (
        <div ref={listRef} className="min-h-0 flex-1 overflow-y-auto">
          {decisions.map((event) => (
            <FeedRow key={event.id} event={event} onClick={() => handleRowClick(event)} />
          ))}
          {isFetching && !isError && (
            <div className="px-5 py-2 text-[11px] text-muted-foreground font-mono">
              Refreshing safety decisions...
            </div>
          )}
          {isError && decisions.length > 0 && (
            <div className="px-5 py-2 flex items-center gap-2 text-[11px] text-[var(--color-warning)]">
              <AlertTriangleIcon />
              Refresh failed — showing cached data
              <button onClick={() => refetch()} className="underline hover:no-underline ml-1">
                Retry
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function AlertTriangleIcon() {
  return (
    <svg className="w-3 h-3 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </svg>
  );
}
