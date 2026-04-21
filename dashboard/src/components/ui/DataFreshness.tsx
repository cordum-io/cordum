import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { cn } from "../../lib/utils";

interface DataFreshnessProps {
  dataUpdatedAt: number;
  onRefresh: () => void;
  isRefetching: boolean;
  className?: string;
}

function formatRelative(ts: number): string {
  // Clamp invalid timestamps so we never render "Updated NaN ago". Callers
  // can pass 0 (no successful fetch yet) or NaN (corrupt state) via the
  // React Query dataUpdatedAt field; surface both as "Not updated".
  if (!Number.isFinite(ts) || ts <= 0) return "Not updated";
  const diff = Math.max(0, Date.now() - ts);
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return "Updated just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `Updated ${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  return `Updated ${hours}h ago`;
}

export function DataFreshness({
  dataUpdatedAt,
  onRefresh,
  isRefetching,
  className,
}: DataFreshnessProps) {
  const [label, setLabel] = useState(() => formatRelative(dataUpdatedAt));

  useEffect(() => {
    setLabel(formatRelative(dataUpdatedAt));
    // Skip the 10s ticker when the timestamp is invalid — the static
    // "Not updated" label from formatRelative never changes with time, so
    // there's no reason to churn React state.
    if (!Number.isFinite(dataUpdatedAt) || dataUpdatedAt <= 0) return;
    const id = setInterval(() => setLabel(formatRelative(dataUpdatedAt)), 10_000);
    return () => clearInterval(id);
  }, [dataUpdatedAt]);

  if (!dataUpdatedAt || !Number.isFinite(dataUpdatedAt)) return null;

  return (
    <button
      type="button"
      className={cn(
        "inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-accent transition-colors",
        className,
      )}
      onClick={onRefresh}
      aria-label="Refresh data"
      title={new Date(dataUpdatedAt).toLocaleString()}
    >
      <RefreshCw
        className={cn("h-3 w-3", isRefetching && "animate-spin")}
      />
      {label}
    </button>
  );
}
