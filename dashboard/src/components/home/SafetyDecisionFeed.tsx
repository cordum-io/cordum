/**
 * DESIGN: "Control Surface" — Live Safety Decision Feed
 * Compact rows, semantic badges, keyboard-accessible, retry on error.
 */
import { useEffect, useMemo, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { motion, AnimatePresence } from "framer-motion";
import { ShieldCheck, Wifi, WifiOff, RefreshCw } from "lucide-react";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import { Button } from "@/components/ui/Button";
import { useSafetyDecisions } from "@/hooks/useSafetyDecisions";
import { useEventStore, type SafetyDecisionEvent } from "@/state/events";
import { cn } from "@/lib/utils";

const FEED_LIMIT = 40;

const decisionVariant: Record<string, "healthy" | "danger" | "warning" | "info" | "muted" | "governance"> = {
  allow: "healthy",
  deny: "governance",
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
    <motion.button
      layout
      initial={{ opacity: 0, x: -4 }}
      animate={{ opacity: 1, x: 0 }}
      exit={{ opacity: 0, scale: 0.95 }}
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
    </motion.button>
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
    <div className="instrument-card flex flex-col h-[520px]">
      {/* Header */}
      <div className="space-y-3 border-b border-border/50 px-5 py-4 glass-panel rounded-t-2xl">
        <div className="flex items-start gap-2.5">
          <ShieldCheck className="mt-0.5 w-4 h-4 text-cordum shrink-0" />
          <div className="flex-1 min-w-0">
            <h2 className="font-display text-sm font-semibold text-foreground tracking-tight">Live Safety Decisions</h2>
            <p className="text-xs text-muted-foreground mt-0.5">
              Stream resolution: 200ms
            </p>
          </div>
          <div className="flex items-center gap-2 shrink-0">
            <span
              className={cn(
                "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium",
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
          </div>
        </div>

        {/* Mini KPI strip */}
        {decisions.length > 0 && (
          <div className="grid grid-cols-4 gap-2">
            {(["allow", "deny", "require_approval", "throttle"] as const).map((key) => (
              <div key={key} className="rounded-xl border border-border/10 bg-surface-0/30 px-2 py-1.5 text-center">
                <p className="text-[10px] font-mono uppercase tracking-widest text-muted-foreground/60">
                  {decisionLabel[key].slice(0, 5)}
                </p>
                <p className="text-xs font-semibold font-mono text-foreground tabular-nums">{counts[key]}</p>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="space-y-2 px-5 py-4 flex-1">
          {Array.from({ length: 8 }, (_, i) => (
            <div key={i} className="skeleton h-9 rounded-xl" />
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
        <div ref={listRef} className="min-h-0 flex-1 overflow-y-auto scrollbar-thin">
          <AnimatePresence initial={false}>
            {decisions.map((event) => (
              <FeedRow key={event.id} event={event} onClick={() => handleRowClick(event)} />
            ))}
          </AnimatePresence>
          {isFetching && !isError && (
            <div className="px-5 py-2 text-xs text-muted-foreground font-mono italic">
              Streaming...
            </div>
          )}
        </div>
      )}
    </div>
  );
}

