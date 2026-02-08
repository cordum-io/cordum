import { useEffect, useRef } from "react";
import { ShieldCheck } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { useEventStore, type SafetyDecisionEvent } from "../../state/events";

// ---------------------------------------------------------------------------
// Decision badge variant mapping
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, "success" | "danger" | "warning" | "info"> = {
  allow: "success",
  deny: "danger",
  require_approval: "warning",
  throttle: "info",
};

const decisionLabel: Record<string, string> = {
  allow: "Allow",
  deny: "Deny",
  require_approval: "Approval",
  throttle: "Throttle",
};

// ---------------------------------------------------------------------------
// Timestamp formatter — HH:mm:ss.SSS
// ---------------------------------------------------------------------------

function fmtTime(iso: string): string {
  try {
    const d = new Date(iso);
    const h = String(d.getHours()).padStart(2, "0");
    const m = String(d.getMinutes()).padStart(2, "0");
    const s = String(d.getSeconds()).padStart(2, "0");
    const ms = String(d.getMilliseconds()).padStart(3, "0");
    return `${h}:${m}:${s}.${ms}`;
  } catch {
    return iso;
  }
}

// ---------------------------------------------------------------------------
// Feed row
// ---------------------------------------------------------------------------

function FeedRow({ event }: { event: SafetyDecisionEvent }) {
  return (
    <div className="flex items-center gap-3 border-b border-border/40 px-4 py-2.5 text-xs last:border-b-0 hover:bg-surface1/50 transition-colors">
      <span className="shrink-0 font-mono text-muted w-[90px]">
        {fmtTime(event.timestamp)}
      </span>
      <span className="shrink-0 truncate max-w-[160px] font-medium text-ink" title={event.topic}>
        {event.topic}
      </span>
      <Badge variant={decisionVariant[event.decision] ?? "default"} className="shrink-0">
        {decisionLabel[event.decision] ?? event.decision}
      </Badge>
      {event.matchedRule && (
        <span className="truncate text-muted max-w-[180px]" title={event.matchedRule}>
          {event.matchedRule}
        </span>
      )}
      {typeof event.evalTimeMs === "number" && (
        <span className="ml-auto shrink-0 font-mono text-muted">
          {event.evalTimeMs}ms
        </span>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Empty state
// ---------------------------------------------------------------------------

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-surface2">
        <ShieldCheck className="h-6 w-6 text-muted" />
      </div>
      <p className="text-sm font-medium text-ink">No safety decisions yet</p>
      <p className="mt-1 text-xs text-muted">Waiting for events...</p>
      <div className="mt-3 flex gap-1">
        {[0, 1, 2].map((i) => (
          <span
            key={i}
            className="h-1.5 w-1.5 rounded-full bg-accent animate-pulse"
            style={{ animationDelay: `${i * 200}ms` }}
          />
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// SafetyDecisionFeed
// ---------------------------------------------------------------------------

export function SafetyDecisionFeed() {
  const decisions = useEventStore((s) => s.safetyDecisions);
  const listRef = useRef<HTMLDivElement>(null);

  // Auto-scroll to top when new events arrive (newest first)
  useEffect(() => {
    if (listRef.current) {
      listRef.current.scrollTop = 0;
    }
  }, [decisions.length]);

  return (
    <Card className="flex flex-col min-h-[400px]">
      {/* Header */}
      <div className="flex items-center gap-2 border-b border-border px-4 pb-3">
        <ShieldCheck className="h-5 w-5 text-accent" />
        <h2 className="font-display text-base font-semibold text-ink">
          Live Safety Decisions
        </h2>
        {decisions.length > 0 && (
          <span className="ml-auto rounded-full bg-surface2 px-2 py-0.5 text-[10px] font-medium text-muted">
            {decisions.length}
          </span>
        )}
      </div>

      {/* Feed */}
      {decisions.length === 0 ? (
        <EmptyState />
      ) : (
        <div ref={listRef} className="flex-1 overflow-y-auto">
          {decisions.map((event) => (
            <FeedRow key={event.id} event={event} />
          ))}
        </div>
      )}
    </Card>
  );
}
