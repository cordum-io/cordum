import { useEffect, useState } from "react";
import { AlertTriangle, X } from "lucide-react";
import { useGeneralConfig } from "../../hooks/useSettings";

function formatElapsed(startIso: string): string {
  const ms = Date.now() - new Date(startIso).getTime();
  const mins = Math.floor(ms / 60_000);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ${mins % 60}m`;
  const days = Math.floor(hrs / 24);
  return `${days}d ${hrs % 24}h`;
}

export function MaintenanceBanner() {
  const { data: config } = useGeneralConfig();
  const [dismissed, setDismissed] = useState(false);
  const [, setTick] = useState(0);

  // Update elapsed every minute
  useEffect(() => {
    if (!config?.maintenanceMode) return;
    const id = setInterval(() => setTick((t) => t + 1), 60_000);
    return () => clearInterval(id);
  }, [config?.maintenanceMode]);

  if (!config?.maintenanceMode || dismissed) return null;

  return (
    <div
      className="sticky top-0 z-30 flex items-center gap-2 border-b border-status-warning-border bg-status-warning-bg px-4 py-2 text-xs"
      role="status"
      aria-live="polite"
    >
      <AlertTriangle className="h-4 w-4 shrink-0 text-warning" />
      <span className="font-semibold text-warning">System is in maintenance mode</span>
      {config.maintenanceMessage && (
        <span className="text-foreground">&mdash; {config.maintenanceMessage}</span>
      )}
      {config.maintenanceStartedAt && (
        <span className="ml-auto font-mono text-muted-foreground">
          {formatElapsed(config.maintenanceStartedAt)}
        </span>
      )}
      <button
        type="button"
        onClick={() => setDismissed(true)}
        className="ml-2 rounded-full p-0.5 text-muted-foreground transition-colors duration-micro hover:bg-surface-2/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/35"
        aria-label="Dismiss maintenance banner"
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
