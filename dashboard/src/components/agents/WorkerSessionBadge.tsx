import type { Worker, WorkerSessionState } from "@/api/types";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { cn } from "@/lib/utils";

/**
 * WorkerSessionBadge surfaces the dispatch-authority signal introduced
 * by the heartbeat-demotion rollout. The primary badge reflects the
 * worker's session-token state; the secondary muted line surfaces
 * heartbeat age as telemetry — never as authority.
 *
 * Reuses the existing StatusBadge primitive so the page keeps the
 * dashboard's design language.
 */
export interface WorkerSessionBadgeProps {
  worker: Pick<
    Worker,
    | "online"
    | "sessionValid"
    | "sessionState"
    | "sessionRevoked"
    | "heartbeatAgeSeconds"
    | "lastHeartbeatAt"
    | "lastHeartbeat"
    | "status"
  >;
  className?: string;
  /** When true, hide the secondary heartbeat-age line. Used for dense
   * list rows that show heartbeat age in a sibling column. */
  suppressHeartbeatLine?: boolean;
}

interface SessionLook {
  variant: BadgeVariant;
  label: string;
  pulse: boolean;
}

function resolveSessionLook(worker: WorkerSessionBadgeProps["worker"]): SessionLook {
  // Explicit backend signal wins.
  if (worker.sessionState) {
    return lookForState(worker.sessionState, !!worker.online);
  }
  // Boolean-only fallback: older gateway that returned `online` but not
  // the state enum.
  if (typeof worker.online === "boolean") {
    return worker.online
      ? { variant: "healthy", label: "Online", pulse: true }
      : { variant: "muted", label: "Offline", pulse: false };
  }
  // Legacy: no session authority — fall back to the operational status
  // pulled from the heartbeat payload.
  const legacy = (worker.status || "").toLowerCase();
  if (legacy === "busy") return { variant: "warning", label: "Busy", pulse: false };
  if (legacy === "idle") return { variant: "healthy", label: "Idle", pulse: false };
  return { variant: "muted", label: legacy || "Unknown", pulse: false };
}

function lookForState(state: WorkerSessionState, online: boolean): SessionLook {
  switch (state) {
    case "valid":
      return { variant: "healthy", label: "Trusted", pulse: online };
    case "session_revoked":
      return { variant: "danger", label: "Revoked", pulse: false };
    case "session_expired":
      return { variant: "warning", label: "Expired", pulse: false };
    case "no_session":
      return { variant: "muted", label: "No session", pulse: false };
    case "trust_store_unready":
      return { variant: "info", label: "Unknown", pulse: false };
  }
}

/**
 * Formats a heartbeat age as a compact "Xs / Xm / Xh ago" sub-line.
 * Returns null when age is not known — callers should omit the line
 * entirely rather than show "last hb ?".
 */
export function formatHeartbeatAge(
  ageSeconds: number | undefined,
): string | null {
  if (typeof ageSeconds !== "number" || !Number.isFinite(ageSeconds)) {
    return null;
  }
  const age = Math.max(0, Math.round(ageSeconds));
  if (age < 60) return `last hb ${age}s ago`;
  if (age < 3600) return `last hb ${Math.round(age / 60)}m ago`;
  if (age < 86400) return `last hb ${Math.round(age / 3600)}h ago`;
  return `last hb ${Math.round(age / 86400)}d ago`;
}

export function WorkerSessionBadge({
  worker,
  className,
  suppressHeartbeatLine,
}: WorkerSessionBadgeProps) {
  const look = resolveSessionLook(worker);
  const heartbeatLine = suppressHeartbeatLine
    ? null
    : formatHeartbeatAge(worker.heartbeatAgeSeconds);

  return (
    <div className={cn("flex flex-col items-start gap-1", className)}>
      <StatusBadge variant={look.variant} dot pulse={look.pulse}>
        {look.label}
      </StatusBadge>
      {heartbeatLine && (
        <span
          className="text-[11px] font-mono text-muted-foreground"
          title="Heartbeat freshness is telemetry only and does not gate dispatch."
          data-testid="worker-heartbeat-age"
        >
          {heartbeatLine}
        </span>
      )}
    </div>
  );
}
