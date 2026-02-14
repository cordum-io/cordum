import { useEffect, useMemo, useRef } from "react";
import { ShieldCheck, Wifi, WifiOff } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { useSafetyDecisions } from "../../hooks/useSafetyDecisions";
import { useEventStore, type SafetyDecisionEvent } from "../../state/events";

const FEED_LIMIT = 40;

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
      return "Stream Offline";
  }
}

function statusClass(status: string): string {
  switch (status) {
    case "connected":
      return "border-emerald-300/70 bg-emerald-100/70 text-emerald-900";
    case "connecting":
      return "border-amber-300/70 bg-amber-100/70 text-amber-900";
    case "reconnecting":
      return "border-amber-300/70 bg-amber-100/70 text-amber-900";
    default:
      return "border-border bg-surface2 text-muted";
  }
}

function FeedRow({ event }: { event: SafetyDecisionEvent }) {
  return (
    <div className="flex items-center gap-3 border-b border-border/40 px-4 py-3 text-xs last:border-b-0 hover:bg-surface1/50 transition-colors">
      <span className="shrink-0 w-[92px] font-mono text-muted">
        {fmtTime(event.timestamp)}
      </span>
      <span className="shrink-0 truncate max-w-[190px] font-medium text-ink" title={event.topic}>
        {event.topic}
      </span>
      <Badge variant={decisionVariant[event.decision] ?? "default"} className="shrink-0">
        {decisionLabel[event.decision] ?? event.decision}
      </Badge>
      {event.matchedRule && (
        <span className="truncate text-muted max-w-[190px]" title={event.matchedRule}>
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

function LoadingState() {
  return (
    <div className="space-y-2 px-4 py-4">
      {Array.from({ length: 4 }, (_, i) => (
        <div key={i} className="h-9 animate-pulse rounded-lg bg-surface2" />
      ))}
    </div>
  );
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-surface2">
        <ShieldCheck className="h-6 w-6 text-muted" />
      </div>
      <p className="text-sm font-medium text-ink">No safety decisions yet</p>
      <p className="mt-1 text-xs text-muted">Waiting for live stream or recent job history.</p>
    </div>
  );
}

function ErrorState() {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-red-100/60">
        <WifiOff className="h-6 w-6 text-red-700" />
      </div>
      <p className="text-sm font-medium text-ink">Unable to load safety decisions</p>
      <p className="mt-1 text-xs text-muted">Check gateway connectivity and auth headers.</p>
    </div>
  );
}

export function SafetyDecisionFeed() {
  const wsStatus = useEventStore((s) => s.status);
  const { decisions, isLoading, isError, isFetching } = useSafetyDecisions(FEED_LIMIT);
  const listRef = useRef<HTMLDivElement>(null);

  const counts = useMemo(() => {
    const out = {
      allow: 0,
      deny: 0,
      require_approval: 0,
      throttle: 0,
    };
    for (const decision of decisions) {
      if (decision.decision in out) {
        out[decision.decision as keyof typeof out] += 1;
      }
    }
    return out;
  }, [decisions]);

  useEffect(() => {
    if (listRef.current) {
      listRef.current.scrollTop = 0;
    }
  }, [decisions.length]);

  return (
    <Card className="flex h-[430px] min-h-[430px] flex-col">
      <div className="space-y-3 border-b border-border px-4 pb-3">
        <div className="flex items-start gap-2">
          <ShieldCheck className="mt-0.5 h-5 w-5 text-accent" />
          <div>
            <h2 className="font-display text-base font-semibold text-ink">Live Safety Decisions</h2>
            <p className="text-[11px] text-muted">Recent decisions from stream and gateway history (latest {FEED_LIMIT})</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] font-medium ${statusClass(wsStatus)}`}>
              {wsStatus === "connected" ? <Wifi className="h-3 w-3" /> : <WifiOff className="h-3 w-3" />}
              {statusLabel(wsStatus)}
            </span>
            <span className="rounded-full bg-surface2 px-2 py-0.5 text-[10px] font-medium text-muted">
              {decisions.length}
            </span>
          </div>
        </div>
        {decisions.length > 0 && (
          <div className="grid grid-cols-4 gap-2">
            <div className="rounded-lg border border-border/70 bg-surface2/30 px-2.5 py-1.5 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted">Allow</p>
              <p className="text-xs font-semibold text-ink">{counts.allow}</p>
            </div>
            <div className="rounded-lg border border-border/70 bg-surface2/30 px-2.5 py-1.5 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted">Deny</p>
              <p className="text-xs font-semibold text-ink">{counts.deny}</p>
            </div>
            <div className="rounded-lg border border-border/70 bg-surface2/30 px-2.5 py-1.5 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted">Approval</p>
              <p className="text-xs font-semibold text-ink">{counts.require_approval}</p>
            </div>
            <div className="rounded-lg border border-border/70 bg-surface2/30 px-2.5 py-1.5 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted">Throttle</p>
              <p className="text-xs font-semibold text-ink">{counts.throttle}</p>
            </div>
          </div>
        )}
      </div>

      {isLoading ? (
        <LoadingState />
      ) : decisions.length === 0 && isError ? (
        <ErrorState />
      ) : decisions.length === 0 ? (
        <EmptyState />
      ) : (
        <div ref={listRef} className="min-h-0 flex-1 overflow-y-auto">
          {decisions.map((event) => (
            <FeedRow key={event.id} event={event} />
          ))}
          {isFetching && (
            <div className="px-4 py-2 text-[11px] text-muted">Refreshing safety decisions...</div>
          )}
        </div>
      )}
    </Card>
  );
}
