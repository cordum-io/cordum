import { cn } from "../lib/utils";
import { useEventStore, type WsStatus } from "../state/events";

// ---------------------------------------------------------------------------
// Status dot color mapping
// ---------------------------------------------------------------------------

const dotColor: Record<WsStatus, string> = {
  connected: "bg-success",
  connecting: "bg-warning",
  reconnecting: "bg-warning animate-pulse",
  disconnected: "bg-danger",
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

  return (
    <div className={cn("flex items-center gap-2", className)}>
      <span
        className={cn("inline-block h-2 w-2 rounded-full", dotColor[status])}
      />
      <span
        className={cn(
          "text-[10px] font-semibold uppercase tracking-wide",
          labelColor[status],
        )}
      >
        {status}
      </span>
    </div>
  );
}
