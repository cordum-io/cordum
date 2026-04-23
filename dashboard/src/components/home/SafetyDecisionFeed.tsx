import { useNavigate } from "react-router-dom";
import { useEffect, useMemo, useRef } from "react";
import { ShieldCheck, Wifi, WifiOff } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { Button } from "../ui/Button";
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
      return "border-status-success-border bg-status-success-bg text-success";
    case "connecting":
      return "border-status-warning-border bg-status-warning-bg text-warning";
    case "reconnecting":
      return "border-status-warning-border bg-status-warning-bg text-warning";
    default:
      return "border-status-danger-border bg-status-danger-bg text-danger";
  }
}

function FeedRow({ event }: { event: SafetyDecisionEvent }) {
  const navigate = useNavigate();

  const handleNavigate = () => {
    const decision = event.decision?.toLowerCase();
    
    if (decision === "deny" || decision === "throttle") {
      if (event.traceId) {
        navigate(`/audit?correlationId=${event.traceId}`);
      } else {
        navigate("/audit");
      }
    } else if (decision === "require_approval") {
      if (event.approvalRef) {
        navigate(`/approvals?id=${event.approvalRef}`);
      } else {
        navigate("/approvals");
      }
    } else {
      if (event.jobId) {
        navigate(`/jobs/${event.jobId}`);
      }
    }
  };

  return (
    <button
      type="button"
      role="button"
      onClick={handleNavigate}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          handleNavigate();
        }
      }}
      aria-label={`${decisionLabel[event.decision] ?? event.decision} decision for ${event.topic}`}
      className="group flex w-full items-center gap-2.5 border-b border-border/45 px-3 py-2.5 text-left text-xs transition-colors last:border-b-0 hover:bg-surface-2/55 focus-visible:bg-surface-2/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35"
    >
      <span className="w-[92px] shrink-0 font-mono text-[11px] text-muted-foreground">
        {fmtTime(event.timestamp)}
      </span>
      <span className="max-w-[190px] shrink-0 truncate text-[11px] font-medium text-foreground transition-colors group-hover:text-accent" title={event.topic}>
        {event.topic}
      </span>
      <Badge variant={decisionVariant[event.decision] ?? "default"} className="shrink-0 px-1.5 py-0.5 text-[10px] uppercase tracking-[0.08em]">
        {decisionLabel[event.decision] ?? event.decision}
      </StatusBadge>
      {event.matchedRule && (
        <span className="max-w-[190px] truncate text-[11px] text-muted-foreground" title={event.matchedRule}>
          {event.matchedRule}
        </span>
      )}
      {typeof event.evalTimeMs === "number" && (
        <span className="ml-auto shrink-0 font-mono text-[11px] text-muted-foreground">
          {event.evalTimeMs}ms
        </span>
      )}
    </button>
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

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-full bg-danger/15">
        <WifiOff className="h-6 w-6 text-danger" />
      </div>
      <p className="text-sm font-medium text-ink">Unable to load safety decisions</p>
      <p className="mt-1 text-xs text-muted">Check gateway connectivity and auth headers.</p>
      <Button type="button" variant="outline" size="sm" className="mt-4" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

export function SafetyDecisionFeed() {
  const navigate = useNavigate();
  const wsStatus = useEventStore((s) => s.status);
  const { decisions, isLoading, isError, isFetching, refetch } = useSafetyDecisions(FEED_LIMIT);
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
    <Card
      className="flex h-[420px] min-h-[420px] flex-col"
      variant={isError && decisions.length === 0 ? "danger" : "default"}
    >
      <div className="space-y-2.5 border-b border-border/70 px-4 py-3">
        <div className="flex items-start gap-2">
          <ShieldCheck className="mt-0.5 h-4 w-4 text-accent" />
          <div>
            <h2 className="font-display text-base font-semibold text-foreground">Live Safety Decisions</h2>
            <p className="text-[11px] text-muted-foreground">Recent decisions from stream and gateway history (latest {FEED_LIMIT})</p>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span
              role="status"
              aria-live="polite"
              className={`inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10px] font-medium uppercase tracking-[0.08em] ${statusClass(wsStatus)}`}
            >
              {wsStatus === "connected" ? <Wifi className="h-3 w-3" /> : <WifiOff className="h-3 w-3" />}
              {statusLabel(wsStatus)}
            </span>
            <span className="rounded-md border border-border/70 bg-surface-2/60 px-2 py-0.5 text-[10px] font-medium text-muted-foreground">
              {decisions.length}
            </span>
          </div>
        </div>

        {/* Mini KPI strip */}
        {decisions.length > 0 && (
          <div className="grid grid-cols-4 gap-2">
            <div className="rounded-md border border-border/70 bg-surface-2/35 px-2 py-1 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted-foreground">Allow</p>
              <p className="text-xs font-semibold text-foreground">{counts.allow}</p>
            </div>
            <div className="rounded-md border border-border/70 bg-surface-2/35 px-2 py-1 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted-foreground">Deny</p>
              <p className="text-xs font-semibold text-foreground">{counts.deny}</p>
            </div>
            <div className="rounded-md border border-border/70 bg-surface-2/35 px-2 py-1 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted-foreground">Approval</p>
              <p className="text-xs font-semibold text-foreground">{counts.require_approval}</p>
            </div>
            <div className="rounded-md border border-border/70 bg-surface-2/35 px-2 py-1 text-center">
              <p className="text-[10px] uppercase tracking-wide text-muted-foreground">Throttle</p>
              <p className="text-xs font-semibold text-foreground">{counts.throttle}</p>
            </div>
          </div>
        )}
      </div>

      {isLoading ? (
        <div className="space-y-2 px-5 py-4 flex-1">
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} className="skeleton h-9 rounded-md" />
          ))}
        </div>
      ) : decisions.length === 0 && isError ? (
        <ErrorState onRetry={() => { void refetch(); }} />
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
          {isError ? (
            <div className="flex items-center justify-between border-t border-status-warning-border/70 bg-status-warning-bg px-3 py-2 text-[11px] text-warning">
              <span>Live safety history refresh failed.</span>
              <button
                type="button"
                className="font-semibold underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35"
                onClick={() => { void refetch(); }}
              >
                Retry
              </button>
            </div>
          ) : null}
          {isFetching && (
            <div aria-live="polite" className="px-4 py-2 text-[11px] text-muted">Refreshing safety decisions...</div>
          )}
        </div>
      )}
    </Card>
  );
}
