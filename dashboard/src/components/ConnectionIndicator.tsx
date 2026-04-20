import { cn } from "../lib/utils";
import { useEventStore, type WsStatus } from "../state/events";

// ---------------------------------------------------------------------------
// Status dot color mapping
// ---------------------------------------------------------------------------

const dotColor: Record<WsStatus, string> = {
  connected: "border-status-success-border bg-success",
  connecting: "border-status-warning-border bg-warning",
  reconnecting: "border-status-warning-border bg-warning animate-pulse motion-reduce:animate-none",
  disconnected: "border-status-danger-border bg-danger",
};

const labelColor: Record<WsStatus, string> = {
  connected: "text-success",
  connecting: "text-warning",
  reconnecting: "text-warning",
  disconnected: "text-danger",
};

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export function ConnectionIndicator({ className }: { className?: string }) {
  const status = useEventStore((s) => s.status);
  const label = status === "connected"
    ? "Connected"
    : status === "connecting"
      ? "Connecting"
      : status === "reconnecting"
        ? "Reconnecting"
        : "Disconnected";

  return (
    <div className={cn("flex items-center gap-2 text-[10px]", className)} role="status" aria-live="polite" aria-label={`Connection ${label}`}>
      <span
        className={cn("inline-block h-2 w-2 rounded-full border", dotColor[status])}
        aria-hidden
      />
      <span
        className={cn(
          "font-mono font-semibold uppercase tracking-wide",
          labelColor[status],
        )}
      >
        {label}
      </span>
    </div>
  );
}
